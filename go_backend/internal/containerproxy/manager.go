package containerproxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"narrafork-go/internal/config"
)

const defaultProxyPort = 7780

const websocketReadLimitBytes = 16 * 1024 * 1024

type Desired struct {
	Enabled bool
	Port    int
}

type State struct {
	Running bool `json:"running"`
	Port    int  `json:"port"`
}

type Manager struct {
	DB       *sql.DB
	Settings *config.Store

	mu     sync.Mutex
	server *http.Server
	port   int
}

type proxyTarget struct {
	ContainerIP   string
	ContainerPort int
	ChapterID     string
	ServiceName   string
}

func New(db *sql.DB, settings *config.Store) *Manager {
	return &Manager{DB: db, Settings: settings}
}

func DesiredFromSettings(raw map[string]any) Desired {
	containers, _ := raw["containers"].(map[string]any)
	proxy, _ := containers["proxy"].(map[string]any)
	return Desired{Enabled: boolValue(proxy["enabled"]), Port: intSetting(proxy["port"], defaultProxyPort)}
}

func (m *Manager) EnsureCurrent(ctx context.Context) error {
	if m == nil || m.Settings == nil {
		return nil
	}
	return m.Ensure(ctx, DesiredFromSettings(m.Settings.Raw()))
}

func (m *Manager) EnsureContainerProxy(ctx context.Context, enabled bool, port int) error {
	return m.Ensure(ctx, Desired{Enabled: enabled, Port: port})
}

func (m *Manager) Ensure(ctx context.Context, desired Desired) error {
	if m == nil {
		return nil
	}
	if desired.Port <= 0 {
		desired.Port = defaultProxyPort
	}
	state := m.State()
	if !desired.Enabled {
		if state.Running {
			return m.Stop(ctx)
		}
		return nil
	}
	if !state.Running {
		return m.Start(ctx, desired.Port)
	}
	if state.Port != desired.Port {
		if err := m.Stop(ctx); err != nil {
			return err
		}
		return m.Start(ctx, desired.Port)
	}
	return nil
}

func (m *Manager) Start(ctx context.Context, port int) error {
	if m == nil || m.DB == nil {
		return nil
	}
	if port <= 0 {
		port = defaultProxyPort
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		return nil
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	actualPort := port
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok && tcp.Port > 0 {
		actualPort = tcp.Port
	}
	server := &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second}
	m.server = server
	m.port = actualPort
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("container proxy stopped with error", "error", err)
		}
	}()
	_ = ctx
	slog.Info("container proxy started", "port", actualPort)
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	server := m.server
	m.server = nil
	m.port = 0
	m.mu.Unlock()
	if server == nil {
		return nil
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	err := server.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (m *Manager) State() State {
	if m == nil {
		return State{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{Running: m.server != nil, Port: m.port}
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = strings.TrimSpace(r.Header.Get("Host"))
	}
	if host == "" {
		writeErrorResponse(w, http.StatusBadRequest, "Bad Request", "Missing Host header.")
		return
	}
	target, err := m.resolveTarget(r.Context(), host)
	if err != nil {
		slog.Warn("container proxy target lookup failed", "host", host, "error", err)
		writeErrorResponse(w, http.StatusBadGateway, "Service Not Found", "The proxy target could not be resolved.")
		return
	}
	if target == nil {
		writeErrorResponse(w, http.StatusBadGateway, "Service Not Found", fmt.Sprintf("No container is mapped to <code>%s</code>. The service may not be running.", escapeHTML(host)))
		return
	}
	if isWebSocketRequest(r) {
		m.proxyWebSocket(w, r, host, target)
		return
	}
	m.proxyHTTP(w, r, host, target)
}

func (m *Manager) resolveTarget(ctx context.Context, host string) (*proxyTarget, error) {
	if m == nil || m.DB == nil {
		return nil, nil
	}
	hostname := strings.ToLower(stripHostPort(host))
	if hostname == "" {
		return nil, nil
	}
	label, domain, err := m.resolveLabelDomain(ctx, hostname)
	if err != nil || label == "" || domain == "" {
		return nil, err
	}
	return m.targetForLabel(ctx, label, domain)
}

func (m *Manager) resolveLabelDomain(ctx context.Context, hostname string) (string, string, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT proxy_domain FROM projects WHERE proxy_domain IS NOT NULL`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	bestLabel := ""
	bestDomain := ""
	for rows.Next() {
		var rawDomain sql.NullString
		if err := rows.Scan(&rawDomain); err != nil {
			return "", "", err
		}
		if !rawDomain.Valid {
			continue
		}
		domain := strings.ToLower(strings.TrimSpace(rawDomain.String))
		if domain == "" {
			continue
		}
		suffix := "." + domain
		if !strings.HasSuffix(hostname, suffix) {
			continue
		}
		label := strings.TrimSuffix(hostname, suffix)
		if label == "" || strings.Contains(label, ".") {
			continue
		}
		if len(domain) > len(bestDomain) {
			bestLabel = label
			bestDomain = domain
		}
	}
	return bestLabel, bestDomain, rows.Err()
}

func (m *Manager) targetForLabel(ctx context.Context, label, domain string) (*proxyTarget, error) {
	row := m.DB.QueryRowContext(ctx, `SELECT ci.container_ip, ci.container_port, ci.chapter_id, ci.service_name
FROM container_instances ci
INNER JOIN chapters c ON c.id = ci.chapter_id
INNER JOIN projects p ON p.id = c.project_id
WHERE ci.status = 'running'
  AND LOWER(ci.proxy_label) = ?
  AND LOWER(p.proxy_domain) = ?
  AND ci.container_port IS NOT NULL
  AND ci.container_ip IS NOT NULL
  AND TRIM(ci.container_ip) != ''
LIMIT 1`, strings.ToLower(label), strings.ToLower(domain))
	var containerIP, chapterID, serviceName string
	var containerPort int
	if err := row.Scan(&containerIP, &containerPort, &chapterID, &serviceName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	containerIP = strings.TrimSpace(containerIP)
	if containerIP == "" || containerPort <= 0 {
		return nil, nil
	}
	return &proxyTarget{ContainerIP: containerIP, ContainerPort: containerPort, ChapterID: chapterID, ServiceName: serviceName}, nil
}

func (m *Manager) proxyHTTP(w http.ResponseWriter, r *http.Request, originalHost string, target *proxyTarget) {
	targetHost := net.JoinHostPort(target.ContainerIP, strconv.Itoa(target.ContainerPort))
	proxy := &httputil.ReverseProxy{
		Director: func(out *http.Request) {
			out.URL.Scheme = "http"
			out.URL.Host = targetHost
			out.Host = targetHost
			out.Header.Set("X-Forwarded-Host", originalHost)
			out.Header.Set("X-Forwarded-Proto", requestProto(r))
			out.Header.Set("X-NarraFork-Chapter", target.ChapterID)
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			slog.Warn("container proxy forward failed", "host", originalHost, "target", targetHost, "error", err)
			writeErrorResponse(rw, http.StatusBadGateway, "Container Unreachable", fmt.Sprintf("Could not connect to <code>%s</code> (%s). The container may have stopped.", escapeHTML(targetHost), escapeHTML(target.ServiceName)))
		},
	}
	proxy.ServeHTTP(w, r)
}

func (m *Manager) proxyWebSocket(w http.ResponseWriter, r *http.Request, originalHost string, target *proxyTarget) {
	targetURL := url.URL{Scheme: "ws", Host: net.JoinHostPort(target.ContainerIP, strconv.Itoa(target.ContainerPort)), Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	requestHeader := http.Header{}
	for key, values := range r.Header {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "sec-websocket-") || lower == "upgrade" || lower == "connection" || lower == "host" {
			continue
		}
		for _, value := range values {
			requestHeader.Add(key, value)
		}
	}
	requestHeader.Set("X-Forwarded-Host", originalHost)
	requestHeader.Set("X-Forwarded-Proto", requestProto(r))
	requestHeader.Set("X-NarraFork-Chapter", target.ChapterID)
	upstream, _, err := websocket.DefaultDialer.DialContext(r.Context(), targetURL.String(), requestHeader)
	if err != nil {
		slog.Warn("container websocket proxy dial failed", "host", originalHost, "target", targetURL.String(), "error", err)
		writeErrorResponse(w, http.StatusBadGateway, "Container Unreachable", fmt.Sprintf("Could not connect to <code>%s</code> (%s). The container may have stopped.", escapeHTML(targetURL.Host), escapeHTML(target.ServiceName)))
		return
	}
	defer upstream.Close()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()
	client.SetReadLimit(websocketReadLimitBytes)
	upstream.SetReadLimit(websocketReadLimitBytes)
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	done := make(chan struct{}, 2)
	go copyWebSocket(upstream, client, closeBoth, done)
	go copyWebSocket(client, upstream, closeBoth, done)
	<-done
	closeBoth()
}

func copyWebSocket(dst, src *websocket.Conn, closeBoth func(), done chan<- struct{}) {
	defer func() {
		closeBoth()
		done <- struct{}{}
	}()
	for {
		messageType, data, err := src.ReadMessage()
		if err != nil {
			return
		}
		if err := dst.WriteMessage(messageType, data); err != nil {
			return
		}
	}
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && headerContains(r.Header.Get("Connection"), "upgrade")
}

func headerContains(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func requestProto(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func stripHostPort(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(strings.ToLower(parsed), "[]")
	}
	if idx := strings.LastIndex(host, ":"); idx > -1 && !strings.Contains(host[idx+1:], "]") {
		if _, err := strconv.Atoi(host[idx+1:]); err == nil {
			return strings.Trim(strings.ToLower(host[:idx]), "[]")
		}
	}
	return strings.Trim(strings.ToLower(host), "[]")
}

func writeErrorResponse(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:system-ui;max-width:600px;margin:80px auto;color:#c9d1d9;background:#0d1117}h1{color:#f85149}code{background:#161b22;padding:2px 6px;border-radius:4px}</style></head>
<body><h1>%s</h1><p>%s</p></body></html>`, escapeHTML(title), escapeHTML(title), detail)
}

func escapeHTML(s string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(s)
}

func boolValue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func intSetting(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed
		}
	case int64:
		if typed > 0 {
			return int(typed)
		}
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
