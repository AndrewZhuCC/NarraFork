package ws

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/terminaldb"
	"narrafork-go/internal/vnet"
)

type TerminalRuntime interface {
	Ensure(ctx context.Context, terminalID string) error
	Write(ctx context.Context, terminalID string, data string) error
	Resize(ctx context.Context, terminalID string, cols, rows int) error
	Kill(ctx context.Context, terminalID string) error
}

type terminalRuntimeSizer interface {
	EnsureSize(ctx context.Context, terminalID string, cols, rows int) error
}

type terminalRuntimeSnapshotter interface {
	Snapshot(ctx context.Context, terminalID string) (map[string]any, error)
}

type Hub struct {
	mu                     sync.RWMutex
	narrators              map[*client]struct{}
	terminals              map[*client]struct{}
	terminalRuntime        TerminalRuntime
	terminalDB             *sql.DB
	browserSessionDB       *sql.DB
	mergeDB                *sql.DB
	narratorSync           NarratorSyncProvider
	presence               map[string]map[*client]viewerInfo
	streamingSnapshots     map[string]*streamingSnapshot
	activeTerminalClients  map[string]*client
	vnetRelay              *vnet.RelayHub
	narratorBroadcastHooks []func(narratorID string, payload any, data []byte)
	upgrader               websocket.Upgrader
}

// NarratorSyncProvider supplies the lightweight message-version and catch-up
// operations used by the narrator WebSocket reconnect path. It is intentionally
// defined in the ws package with JSON-compatible payloads to avoid a package
// cycle with internal/narrators.
type NarratorSyncProvider interface {
	NarratorMessageVersion(ctx context.Context, narratorID string) (int, error)
	NarratorMessagesAfter(ctx context.Context, narratorID string, anchor any, limit int) (map[string]any, error)
}

type streamingSnapshot struct {
	StreamingBlocks  []map[string]any
	ToolChunks       map[string]map[string]any
	NKPQueuePosition *int
	NKPQueueDepth    *int
	QueueMessage     string
}

type client struct {
	conn        *websocket.Conn
	sendMu      sync.Mutex
	sendSlots   chan struct{}
	kind        string
	userID      string
	viewer      viewerInfo
	subscribers map[string]struct{}
}

type viewerInfo struct {
	UserID        string  `json:"userId"`
	Username      string  `json:"username"`
	AvatarColor   *string `json:"avatarColor"`
	AvatarImageID *string `json:"avatarImageId"`
}

type pendingBroadcast struct {
	clients []*client
	data    []byte
}

const (
	websocketWriteTimeout     = 5 * time.Second
	websocketSendBacklog      = 64
	websocketHeartbeatTimeout = 90 * time.Second
)

var websocketHeartbeatInterval = 30 * time.Second

func NewHub() *Hub {
	return &Hub{
		narrators:             map[*client]struct{}{},
		terminals:             map[*client]struct{}{},
		presence:              map[string]map[*client]viewerInfo{},
		streamingSnapshots:    map[string]*streamingSnapshot{},
		activeTerminalClients: map[string]*client{},
		vnetRelay:             vnet.NewRelayHub(vnet.DefaultOptions()),
		upgrader:              websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
	}
}

func (h *Hub) SetTerminalRuntime(runtime TerminalRuntime) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.terminalRuntime = runtime
}

func (h *Hub) SetTerminalDB(sqlDB *sql.DB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.terminalDB = sqlDB
	if h.mergeDB == nil {
		h.mergeDB = sqlDB
	}
}

func (h *Hub) SetMergeDB(sqlDB *sql.DB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mergeDB = sqlDB
}

func (h *Hub) SetBrowserSessionDB(sqlDB *sql.DB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.browserSessionDB = sqlDB
}

func (h *Hub) NarratorIDsWithPresence() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.presence))
	for narratorID, viewers := range h.presence {
		if len(viewers) > 0 {
			ids = append(ids, narratorID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (h *Hub) SetNarratorSyncProvider(provider NarratorSyncProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.narratorSync = provider
}

func (h *Hub) SetNarratorBroadcastHook(hook func(narratorID string, payload any, data []byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hook == nil {
		h.narratorBroadcastHooks = nil
		return
	}
	h.narratorBroadcastHooks = []func(narratorID string, payload any, data []byte){hook}
}

func (h *Hub) AddNarratorBroadcastHook(hook func(narratorID string, payload any, data []byte)) {
	if hook == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.narratorBroadcastHooks = append(h.narratorBroadcastHooks, hook)
}

func (h *Hub) NarratorHandler(authService *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := h.authorize(w, r, authService)
		if !ok {
			return
		}
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("upgrade narrator websocket", "error", err)
			return
		}
		viewer := loadViewerInfo(r.Context(), authService.DB, claims.Subject)
		c := &client{conn: conn, kind: "narrator", userID: claims.Subject, viewer: viewer, subscribers: map[string]struct{}{}}
		h.add(c)
		go h.heartbeatLoop(c)
		go h.readLoop(c)
	}
}

func (h *Hub) TerminalHandler(authService *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := h.authorize(w, r, authService)
		if !ok {
			return
		}
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("upgrade terminal websocket", "error", err)
			return
		}
		c := &client{conn: conn, kind: "terminal", userID: claims.Subject, subscribers: map[string]struct{}{}}
		h.add(c)
		go h.heartbeatLoop(c)
		go h.readLoop(c)
	}
}

func (h *Hub) VNetHandler(authService *auth.Service, settings *config.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options := h.ConfigureVNetFromSettings(settings)
		if !websocket.IsWebSocketUpgrade(r) {
			status := http.StatusOK
			payload := h.VNetStats(settings)
			if !options.Enabled {
				status = http.StatusServiceUnavailable
			}
			httpx.WriteJSON(w, status, payload)
			return
		}
		if !options.Enabled {
			conn, err := h.upgrader.Upgrade(w, r, nil)
			if err != nil {
				slog.Warn("upgrade disabled vnet websocket", "error", err)
				return
			}
			reason := "VNet is disabled"
			payload := map[string]any{"type": "error", "ok": false, "supported": true, "fallback": true, "code": "VNET_DISABLED", "feature": "vnet", "reason": reason, "error": reason, "message": reason}
			data, _ := json.Marshal(payload)
			_ = conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout))
			_ = conn.WriteMessage(websocket.TextMessage, data)
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "vnet disabled"), time.Now().Add(websocketWriteTimeout))
			_ = conn.Close()
			return
		}
		authInfo, ok := resolveVNetRelayAuth(r, authService, options)
		if !ok {
			httpx.WriteError(w, httpx.Unauthorized("VNet relay authentication required"))
			return
		}
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("upgrade vnet websocket", "error", err)
			return
		}
		c := &client{conn: conn, kind: "vnet", userID: authInfo.UserID, subscribers: map[string]struct{}{}}
		h.add(c)
		if h.vnetRelay != nil {
			h.vnetRelay.AttachSocket(c, authInfo)
		}
		go h.heartbeatLoop(c)
		go h.readLoop(c)
	}
}

func (h *Hub) VNetDisabledHandler() http.HandlerFunc {
	reason := "VNet relay/UDP WebSocket runtime is not bundled in go_backend"
	payload := map[string]any{
		"type":      "error",
		"ok":        false,
		"supported": false,
		"fallback":  true,
		"code":      "VNET_DISABLED",
		"feature":   "vnet",
		"reason":    reason,
		"error":     reason,
		"message":   reason,
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			httpx.WriteJSON(w, http.StatusNotImplemented, payload)
			return
		}
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("upgrade disabled vnet websocket", "error", err)
			return
		}
		data, _ := json.Marshal(payload)
		_ = conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout))
		_ = conn.WriteMessage(websocket.TextMessage, data)
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "vnet disabled"), time.Now().Add(websocketWriteTimeout))
		_ = conn.Close()
	}
}

func (h *Hub) ConfigureVNetFromSettings(settings *config.Store) vnet.Options {
	options := vnet.DefaultOptions()
	options.Enabled = true
	if settings != nil {
		raw := settings.Raw()
		if cfg, ok := raw["vnet"].(map[string]any); ok {
			if enabled, ok := cfg["enabled"]; ok {
				options.Enabled = boolValue(enabled)
			}
			options.AllowAnonymous = boolValue(cfg["allowAnonymousRelay"])
			options.RelayToken = strings.TrimSpace(stringValue(cfg["relayToken"]))
			if maxPeers := intNumber(cfg["maxPeersPerNetwork"]); maxPeers > 0 {
				options.MaxPeersPerNetwork = maxPeers
			}
			if maxBytes := intNumber(cfg["maxMessageBytes"]); maxBytes > 0 {
				options.MaxMessageBytes = maxBytes
			}
			if udp, ok := cfg["udp"].(map[string]any); ok {
				options.UDP = vnet.UDPInfo{Enabled: boolValue(udp["enabled"]), Host: strings.TrimSpace(stringValue(udp["host"])), Port: intNumber(udp["port"])}
			}
		}
	}
	if h != nil && h.vnetRelay != nil {
		h.vnetRelay.Configure(options)
	}
	return options
}

func (h *Hub) VNetStats(settings *config.Store) map[string]any {
	options := h.ConfigureVNetFromSettings(settings)
	stats := vnet.Stats{MaxPeersPerNetwork: options.MaxPeersPerNetwork, MaxMessageBytes: options.MaxMessageBytes, UDP: options.UDP}
	if h != nil && h.vnetRelay != nil {
		stats = h.vnetRelay.Stats()
	}
	payload := map[string]any{
		"ok":                 options.Enabled,
		"supported":          true,
		"fallback":           !options.Enabled,
		"mode":               "relay",
		"ws":                 true,
		"peerCleanup":        true,
		"udpRendezvous":      false,
		"networks":           stats.Networks,
		"peers":              stats.Peers,
		"connections":        stats.Connections,
		"maxPeersPerNetwork": stats.MaxPeersPerNetwork,
		"maxMessageBytes":    stats.MaxMessageBytes,
		"relay": map[string]any{
			"clients": stats.Connections,
			"rooms":   stats.Networks,
			"peers":   stats.Peers,
			"running": options.Enabled,
		},
		"udp": stats.UDP,
	}
	if !options.Enabled {
		payload["code"] = "VNET_DISABLED"
		payload["feature"] = "vnet"
		payload["reason"] = "VNet is disabled in settings"
	}
	return payload
}

func resolveVNetRelayAuth(r *http.Request, authService *auth.Service, options vnet.Options) (vnet.RelayAuth, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	bearer := ""
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		bearer = strings.TrimSpace(header[len("Bearer "):])
	}
	token := bearer
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token != "" && options.RelayToken != "" && token == options.RelayToken {
		return vnet.RelayAuth{Kind: "relay-token"}, true
	}
	if token != "" && authService != nil {
		if claims, err := authService.VerifyToken(token); err == nil {
			return vnet.RelayAuth{Kind: "jwt", UserID: claims.Subject}, true
		}
	}
	if options.AllowAnonymous {
		return vnet.RelayAuth{Kind: "anonymous"}, true
	}
	return vnet.RelayAuth{}, false
}

func (h *Hub) BroadcastTerminal(terminalID string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := make([]*client, 0, len(h.terminals))
	for c := range h.terminals {
		if _, ok := c.subscribers[terminalID]; ok {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		c.queueSend(data)
	}
	if event := mapValue(payload); stringValue(event["type"]) == "exit" {
		h.broadcastTerminalCountForTerminal(context.Background(), terminalID)
	}
}

func (h *Hub) broadcastTerminalCountForTerminal(ctx context.Context, terminalID string) {
	h.mu.RLock()
	sqlDB := h.terminalDB
	h.mu.RUnlock()
	if sqlDB == nil || strings.TrimSpace(terminalID) == "" {
		return
	}
	var narratorID, chapterID sql.NullString
	if err := sqlDB.QueryRowContext(ctx, `SELECT narrator_id, chapter_id FROM terminals WHERE id = ?`, terminalID).Scan(&narratorID, &chapterID); err != nil {
		return
	}
	h.broadcastTerminalCountForNarrator(ctx, sqlString(narratorID))
	h.broadcastTerminalCountForChapter(ctx, sqlString(chapterID))
}

func (h *Hub) broadcastTerminalCountForChapter(ctx context.Context, chapterID string) {
	chapterID = strings.TrimSpace(chapterID)
	if chapterID == "" {
		return
	}
	h.mu.RLock()
	sqlDB := h.terminalDB
	h.mu.RUnlock()
	if sqlDB == nil {
		return
	}
	rows, err := sqlDB.QueryContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ?`, chapterID)
	if err != nil {
		return
	}
	narratorIDs := []string{}
	for rows.Next() {
		var narratorID string
		if err := rows.Scan(&narratorID); err == nil && strings.TrimSpace(narratorID) != "" {
			narratorIDs = append(narratorIDs, narratorID)
		}
	}
	_ = rows.Close()
	for _, narratorID := range narratorIDs {
		h.broadcastTerminalCountForNarrator(ctx, narratorID)
	}
}

func (h *Hub) broadcastTerminalCountForNarrator(ctx context.Context, narratorID string) {
	narratorID = strings.TrimSpace(narratorID)
	if narratorID == "" {
		return
	}
	h.mu.RLock()
	sqlDB := h.terminalDB
	h.mu.RUnlock()
	if sqlDB == nil {
		return
	}
	count := 0
	fallbacks := []string{}
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM terminals WHERE narrator_id = ? AND status = 'running'`, narratorID).Scan(&count); err != nil {
		fallbacks = append(fallbacks, "terminal_count_query_failed")
	}
	h.BroadcastNarrator(narratorID, map[string]any{
		"type":                "terminal_count_changed",
		"narratorId":          narratorID,
		"activeTerminalCount": count,
		"count":               count,
		"degraded":            len(fallbacks) > 0,
		"fallbacks":           fallbacks,
	})
}

func (h *Hub) BroadcastMergeProgress(payload any) {
	h.BroadcastProjectEvent(payload)
}

func (h *Hub) BroadcastProjectEvent(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	projectID := ""
	if body, ok := payload.(map[string]any); ok {
		projectID = strings.TrimSpace(stringValue(body["projectId"]))
	}
	h.mu.RLock()
	clients := make([]*client, 0, len(h.narrators))
	for c := range h.narrators {
		clients = append(clients, c)
	}
	sqlDB := h.mergeDB
	h.mu.RUnlock()
	for _, c := range clients {
		if projectID != "" && sqlDB != nil {
			allowed, err := h.clientCanAccessMergeProject(context.Background(), sqlDB, c, projectID, true)
			if err != nil || !allowed {
				continue
			}
		}
		c.queueSend(data)
	}
}

func (h *Hub) BroadcastUser(userID string, payload any) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := make([]*client, 0)
	for c := range h.narrators {
		if c.userID == userID {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		c.queueSend(data)
	}
}

func (h *Hub) BroadcastNarrator(narratorID string, payload any) {
	payloads := narratorBroadcastPayloads(narratorID, payload)
	frames := make([][]byte, 0, len(payloads))
	encodedPayloads := make([]any, 0, len(payloads))
	for _, item := range payloads {
		data, err := json.Marshal(item)
		if err != nil {
			continue
		}
		frames = append(frames, data)
		encodedPayloads = append(encodedPayloads, item)
	}
	if len(frames) == 0 {
		return
	}
	h.mu.Lock()
	for _, item := range encodedPayloads {
		h.recordStreamingSnapshotLocked(narratorID, mapValue(item))
	}
	hooks := append([]func(narratorID string, payload any, data []byte){}, h.narratorBroadcastHooks...)
	clients := make([]*client, 0, len(h.narrators))
	for c := range h.narrators {
		if _, ok := c.subscribers[narratorID]; ok {
			clients = append(clients, c)
		}
	}
	h.mu.Unlock()
	for i, item := range encodedPayloads {
		for _, hook := range hooks {
			hook(narratorID, item, frames[i])
		}
	}
	for _, c := range clients {
		c.queueSendBatch(frames)
	}
}

func narratorBroadcastPayloads(narratorID string, payload any) []any {
	items := []any{payload}
	base, ok := payload.(map[string]any)
	if !ok {
		return items
	}
	typeValue, _ := base["type"].(string)
	if typeValue == "" {
		return items
	}
	appendPayload := func(extra map[string]any) {
		items = append(items, withNarratorID(narratorID, extra))
	}
	switch typeValue {
	case "permission_request", "permission_resolved", "tool_use_chunk":
		if _, ok := base["narratorId"]; !ok {
			compat := copyMap(base)
			compat["narratorId"] = narratorID
			items[0] = compat
		}
	case "status":
		compat := copyMap(base)
		compat["type"] = "status_change"
		appendPayload(compat)
		if substatus, ok := stringArrayValue(base["substatus"]); ok {
			appendPayload(map[string]any{"type": "substatus_change", "substatus": substatus})
		}
	case "status_change":
		if substatus, ok := stringArrayValue(base["substatus"]); ok {
			appendPayload(map[string]any{"type": "substatus_change", "substatus": substatus})
		}
	case "message":
		if messageRole(base["message"]) == "user" {
			appendPayload(map[string]any{"type": "user_message", "message": base["message"]})
		}
	case "stream_text":
		appendPayload(streamEventPayload("text_delta", stringValue(base["text"]), base["outputIndex"]))
	case "stream_reasoning":
		appendPayload(streamEventPayload("reasoning_delta", stringValue(base["text"]), base["outputIndex"]))
	case "tool_call":
		compat := map[string]any{
			"type":      "tool_started",
			"toolUseId": base["toolUseId"],
			"toolName":  base["toolName"],
		}
		copyOptionalFields(compat, base, "input", "streamStartedAt", "parentToolUseId")
		appendPayload(compat)
	case "retrying":
		appendPayload(map[string]any{
			"type":       "warning",
			"message":    stringValue(base["message"]),
			"retryCount": base["attempt"],
			"maxRetries": base["maxRetries"],
			"delayMs":    base["delayMs"],
		})
	case "tool_result":
		status := "success"
		if boolValue(base["isError"]) {
			status = "fail"
		}
		compat := map[string]any{
			"type":      "tool_completed",
			"toolUseId": base["toolUseId"],
			"toolName":  base["toolName"],
			"status":    status,
			"output":    base["output"],
		}
		copyOptionalFields(compat, base, "durationMs", "updatedInput", "metadata", "sideCars", "parentToolUseId")
		appendPayload(compat)
	}
	return items
}

func copyOptionalFields(dst, src map[string]any, fields ...string) {
	for _, field := range fields {
		if value, ok := src[field]; ok {
			dst[field] = value
		}
	}
}

func withNarratorID(narratorID string, payload map[string]any) map[string]any {
	out := copyMap(payload)
	if _, ok := out["narratorId"]; !ok {
		out["narratorId"] = narratorID
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func streamEventPayload(deltaType, text string, outputIndex any) map[string]any {
	delta := map[string]any{"type": deltaType, "text": text}
	event := map[string]any{"type": "content_block_delta", "delta": delta}
	if index, ok := outputIndexNumber(outputIndex); ok {
		event["outputIndex"] = index
		if deltaType == "reasoning_delta" {
			delta["outputIndex"] = index
		}
	}
	return map[string]any{"type": "stream_event", "event": event}
}

func outputIndexNumber(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func messageRole(value any) string {
	switch msg := value.(type) {
	case map[string]any:
		return stringValue(msg["role"])
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return ""
		}
		return stringValue(decoded["role"])
	}
}

func stringArrayValue(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func (h *Hub) authorize(w http.ResponseWriter, r *http.Request, authService *auth.Service) (*auth.Claims, bool) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if token == "" {
		httpx.WriteError(w, httpx.Unauthorized("Authentication required"))
		return nil, false
	}
	claims, err := authService.VerifyToken(token)
	if err != nil {
		httpx.WriteError(w, err)
		return nil, false
	}
	return claims, true
}

func loadViewerInfo(ctx context.Context, sqlDB *sql.DB, userID string) viewerInfo {
	viewer := viewerInfo{UserID: userID}
	if sqlDB == nil || userID == "" {
		return viewer
	}
	var avatarColor, avatarImageID sql.NullString
	err := sqlDB.QueryRowContext(ctx, `SELECT username, avatar_color, avatar_image_id FROM users WHERE id = ?`, userID).Scan(&viewer.Username, &avatarColor, &avatarImageID)
	if err != nil {
		return viewer
	}
	viewer.AvatarColor = nullStringPtr(avatarColor)
	viewer.AvatarImageID = nullStringPtr(avatarImageID)
	return viewer
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch c.kind {
	case "terminal":
		h.terminals[c] = struct{}{}
	case "narrator":
		h.narrators[c] = struct{}{}
	}
}

func (h *Hub) remove(c *client) {
	broadcasts := []pendingBroadcast{}
	if c.kind == "vnet" && h.vnetRelay != nil {
		h.vnetRelay.DetachSocket(c)
	}
	h.mu.Lock()
	delete(h.narrators, c)
	delete(h.terminals, c)
	for terminalID, active := range h.activeTerminalClients {
		if active == c {
			delete(h.activeTerminalClients, terminalID)
		}
	}
	for narratorID, viewers := range h.presence {
		if _, ok := viewers[c]; !ok {
			continue
		}
		delete(viewers, c)
		if len(viewers) == 0 {
			delete(h.presence, narratorID)
		}
		broadcasts = append(broadcasts, h.presenceBroadcastLocked(narratorID))
	}
	h.mu.Unlock()
	_ = c.conn.Close()
	h.sendBroadcasts(broadcasts)
}

func (h *Hub) subscribe(c *client, id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c.subscribers[id] = struct{}{}
}

func (h *Hub) unsubscribe(c *client, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(c.subscribers, strings.TrimSpace(id))
}

func (h *Hub) isSubscribed(c *client, id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := c.subscribers[strings.TrimSpace(id)]
	return ok
}

func (h *Hub) setActiveTerminalClient(terminalID string, c *client) bool {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := h.activeTerminalClients[terminalID]
	if previous == c {
		return false
	}
	h.activeTerminalClients[terminalID] = c
	return true
}

func (h *Hub) isActiveTerminalResizeClient(terminalID string, c *client) bool {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	active := h.activeTerminalClients[terminalID]
	return active == nil || active == c
}

func (h *Hub) clearActiveTerminalClient(c *client, terminalID string) {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.activeTerminalClients[terminalID] == c {
		delete(h.activeTerminalClients, terminalID)
	}
}

func (h *Hub) narratorSyncProvider() NarratorSyncProvider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.narratorSync
}

func (h *Hub) addPresence(c *client, narratorID string) {
	narratorID = strings.TrimSpace(narratorID)
	if narratorID == "" || c.viewer.UserID == "" {
		return
	}
	h.mu.Lock()
	viewers := h.presence[narratorID]
	if viewers == nil {
		viewers = map[*client]viewerInfo{}
		h.presence[narratorID] = viewers
	}
	viewers[c] = c.viewer
	broadcast := h.presenceBroadcastLocked(narratorID)
	h.mu.Unlock()
	h.sendBroadcast(broadcast)
}

func (h *Hub) removePresence(c *client, narratorID string) {
	narratorID = strings.TrimSpace(narratorID)
	if narratorID == "" {
		return
	}
	var broadcast pendingBroadcast
	h.mu.Lock()
	if viewers := h.presence[narratorID]; viewers != nil {
		if _, ok := viewers[c]; ok {
			delete(viewers, c)
			if len(viewers) == 0 {
				delete(h.presence, narratorID)
			}
			broadcast = h.presenceBroadcastLocked(narratorID)
		}
	}
	h.mu.Unlock()
	h.sendBroadcast(broadcast)
}

func (h *Hub) presenceBroadcastLocked(narratorID string) pendingBroadcast {
	unique := map[string]viewerInfo{}
	if viewers := h.presence[narratorID]; viewers != nil {
		for _, viewer := range viewers {
			if viewer.UserID != "" {
				unique[viewer.UserID] = viewer
			}
		}
	}
	viewerIDs := make([]string, 0, len(unique))
	for userID := range unique {
		viewerIDs = append(viewerIDs, userID)
	}
	sort.Strings(viewerIDs)
	viewers := make([]viewerInfo, 0, len(viewerIDs))
	for _, userID := range viewerIDs {
		viewers = append(viewers, unique[userID])
	}
	clients := make([]*client, 0, len(h.narrators))
	for c := range h.narrators {
		if _, ok := c.subscribers[narratorID]; ok {
			clients = append(clients, c)
		}
	}
	data, err := json.Marshal(map[string]any{"type": "presence_update", "narratorId": narratorID, "viewers": viewers})
	if err != nil {
		return pendingBroadcast{}
	}
	return pendingBroadcast{clients: clients, data: data}
}

func (h *Hub) sendBroadcasts(broadcasts []pendingBroadcast) {
	for _, broadcast := range broadcasts {
		h.sendBroadcast(broadcast)
	}
}

func (h *Hub) sendBroadcast(broadcast pendingBroadcast) {
	if len(broadcast.data) == 0 {
		return
	}
	for _, c := range broadcast.clients {
		c.queueSend(broadcast.data)
	}
}

func (h *Hub) heartbeatLoop(c *client) {
	ticker := time.NewTicker(websocketHeartbeatInterval)
	defer ticker.Stop()
	for range ticker.C {
		h.mu.RLock()
		_, inNarrators := h.narrators[c]
		_, inTerminals := h.terminals[c]
		h.mu.RUnlock()
		if !inNarrators && !inTerminals {
			return
		}
		c.queueSendJSON(map[string]any{"type": "ping"})
	}
}

func (h *Hub) readLoop(c *client) {
	defer h.remove(c)
	c.conn.SetReadLimit(1024 * 1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(websocketHeartbeatTimeout))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(websocketHeartbeatTimeout))
	})
	for {
		var msg map[string]any
		if err := c.conn.ReadJSON(&msg); err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(websocketHeartbeatTimeout))
		h.handleMessage(c, msg)
	}
}

func (h *Hub) handleMessage(c *client, msg map[string]any) {
	typeValue, _ := msg["type"].(string)
	if c.kind == "vnet" {
		if typeValue == "ping" {
			c.sendJSON(map[string]any{"type": "pong"})
			return
		}
		if h.vnetRelay != nil {
			h.vnetRelay.HandleClientMessage(c, msg)
		}
		return
	}
	switch typeValue {
	case "ping":
		c.sendJSON(map[string]any{"type": "pong"})
		return
	case "pong":
		return
	case "subscribe":
		ids := readStringArray(msg["narratorIds"])
		if c.kind == "terminal" {
			ids = readStringArray(msg["terminalIds"])
		}
		if c.kind == "narrator" {
			h.handleNarratorSubscribe(c, ids, msg)
			return
		}
		for _, id := range ids {
			h.subscribe(c, id)
			if c.kind == "terminal" {
				h.handleTerminalSubscribe(c, id)
			}
		}
	case "unsubscribe":
		ids := readStringArray(msg["narratorIds"])
		if c.kind == "terminal" {
			ids = readStringArray(msg["terminalIds"])
		}
		for _, id := range ids {
			h.unsubscribe(c, id)
			if c.kind == "terminal" {
				h.clearActiveTerminalClient(c, id)
			}
		}
	case "permission_decision":
		if c.kind == "narrator" {
			h.handlePermissionDecision(c, msg)
		}
	case "merge_decision":
		if c.kind == "narrator" {
			h.handleMergeDecision(c, msg)
		}
	case "sync_check":
		if c.kind == "narrator" {
			h.handleSyncCheck(c, msg)
		}
	case "subscribe_stats":
		if c.kind == "narrator" {
			h.handleStatsSubscription(c, msg, true)
		}
	case "unsubscribe_stats":
		if c.kind == "narrator" {
			h.handleStatsSubscription(c, msg, false)
		}
	case "buffer_message":
		if c.kind == "narrator" {
			h.handleBufferMessage(c, msg)
		}
	case "cancel_buffer":
		if c.kind == "narrator" {
			h.handleCancelBuffer(c, msg)
		}
	case "update_buffer":
		if c.kind == "narrator" {
			h.handleUpdateBuffer(c, msg)
		}
	case "remove_buffer":
		if c.kind == "narrator" {
			h.handleRemoveBuffer(c, msg)
		}
	case "update_timeout":
		if c.kind == "narrator" {
			h.handleUpdateTimeout(c, msg)
		}
	case "presence_join":

		if c.kind == "narrator" {
			narratorID, _ := msg["narratorId"].(string)
			h.addPresence(c, narratorID)
		}
	case "presence_leave":
		if c.kind == "narrator" {
			narratorID, _ := msg["narratorId"].(string)
			h.removePresence(c, narratorID)
		}
	case "input":
		if c.kind == "terminal" && h.terminalRuntime != nil {
			terminalID := strings.TrimSpace(stringValue(msg["terminalId"]))
			data, _ := msg["data"].(string)
			if terminalID == "" {
				c.sendJSON(map[string]any{"type": "error", "message": "terminalId is required"})
				return
			}
			if !h.isSubscribed(c, terminalID) {
				return
			}
			if h.setActiveTerminalClient(terminalID, c) {
				c.sendJSON(map[string]any{"type": "requestResize", "terminalId": terminalID})
			}
			if err := h.terminalRuntime.Write(context.Background(), terminalID, data); err != nil {
				c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": err.Error()})
			}
		}
	case "resize":
		if c.kind == "terminal" && h.terminalRuntime != nil {
			terminalID := strings.TrimSpace(stringValue(msg["terminalId"]))
			cols := intNumber(msg["cols"])
			rows := intNumber(msg["rows"])
			if terminalID == "" {
				c.sendJSON(map[string]any{"type": "error", "message": "terminalId is required"})
				return
			}
			// Mirror the TypeScript terminalWsMessageSchema resize bounds: cols 10..500, rows 2..200.
			if cols < 10 || cols > 500 || rows < 2 || rows > 200 {
				c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": "Invalid terminal resize request"})
				return
			}
			if !h.isSubscribed(c, terminalID) {
				return
			}
			if !h.isActiveTerminalResizeClient(terminalID, c) {
				return
			}
			if err := h.terminalRuntime.Resize(context.Background(), terminalID, cols, rows); err != nil {
				c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": err.Error()})
			}
		}
	case "kill":
		if c.kind == "terminal" && h.terminalRuntime != nil {
			terminalID := strings.TrimSpace(stringValue(msg["terminalId"]))
			if terminalID == "" {
				c.sendJSON(map[string]any{"type": "error", "message": "terminalId is required"})
				return
			}
			if err := h.terminalRuntime.Kill(context.Background(), terminalID); err != nil {
				c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": err.Error()})
				return
			}
			h.BroadcastTerminal(terminalID, map[string]any{"type": "killed", "terminalId": terminalID})
			h.broadcastTerminalCountForTerminal(context.Background(), terminalID)
		}
	case "create":
		if c.kind == "terminal" {
			terminal, err := h.createTerminal(context.Background(), msg)
			if err != nil {
				c.sendJSON(map[string]any{"type": "error", "terminalId": "", "message": err.Error()})
				return
			}
			terminalID, _ := terminal["id"].(string)
			if terminalID != "" {
				h.subscribe(c, terminalID)
			}
			requestID, _ := msg["requestId"].(string)
			c.sendJSON(map[string]any{"type": "created", "requestId": requestID, "terminal": terminal})
			if terminalID != "" {
				h.sendTerminalState(c, terminalID, terminal)
				h.broadcastTerminalCountForTerminal(context.Background(), terminalID)
			}
		}
	case "rename":
		if c.kind == "terminal" {
			terminalID, _ := msg["terminalId"].(string)
			name, _ := msg["name"].(string)
			if err := h.renameTerminal(context.Background(), terminalID, name); err != nil {
				c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": err.Error()})
				return
			}
			h.BroadcastTerminal(terminalID, map[string]any{"type": "renamed", "terminalId": terminalID, "name": strings.TrimSpace(name)})
		}
	default:
		c.sendJSON(map[string]any{"type": "error", "message": "Unknown or unsupported WebSocket message type"})
	}
}

func (h *Hub) handleTerminalSubscribe(c *client, terminalID string) {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return
	}
	ctx := context.Background()

	h.mu.RLock()
	runtime := h.terminalRuntime
	sqlDB := h.terminalDB
	h.mu.RUnlock()

	var terminal map[string]any
	if sqlDB != nil {
		loaded, err := loadTerminal(ctx, sqlDB, terminalID)
		if err != nil {
			c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": err.Error()})
			return
		}
		terminal = loaded
	}

	if terminalStatus(terminal) != "exited" && runtime != nil {
		if err := runtime.Ensure(ctx, terminalID); err != nil {
			c.sendJSON(map[string]any{"type": "error", "terminalId": terminalID, "message": err.Error()})
			return
		}
		if sqlDB != nil {
			if loaded, err := loadTerminal(ctx, sqlDB, terminalID); err == nil {
				terminal = loaded
			}
		}
	}

	h.sendTerminalState(c, terminalID, terminal)
}

func (h *Hub) sendTerminalState(c *client, terminalID string, terminal map[string]any) {
	payload := map[string]any{"type": "ready", "terminalId": terminalID}
	if terminal != nil {
		payload["terminal"] = terminal
		payload["status"] = terminalStatus(terminal)
		payload["exitCode"] = terminal["exitCode"]
	}
	c.sendJSON(payload)

	if snapshot, ok, err := h.terminalSnapshot(terminalID); err != nil {
		reason := err.Error()
		c.sendJSON(map[string]any{
			"type":       "error",
			"terminalId": terminalID,
			"reason":     reason,
			"error":      reason,
			"message":    reason,
			"code":       "TERMINAL_SNAPSHOT_UNAVAILABLE",
			"fallback":   true,
			"fatal":      false,
		})
	} else if ok {
		c.sendJSON(snapshot)
		if data, _ := snapshot["data"].(string); data != "" {
			c.sendJSON(map[string]any{
				"type":       "scrollback",
				"terminalId": terminalID,
				"data":       data,
				"cols":       intNumberDefault(snapshot["cols"], 80),
				"rows":       intNumberDefault(snapshot["rows"], 24),
			})
		}
		if _, hasMouse := snapshot["mouseTracking"]; hasMouse {
			c.sendJSON(map[string]any{
				"type":          "bufferState",
				"terminalId":    terminalID,
				"mouseTracking": boolValue(snapshot["mouseTracking"]),
				"cursorVisible": boolValueDefault(snapshot["cursorVisible"], true),
			})
		}
	}

	if terminalStatus(terminal) == "exited" {
		c.sendJSON(map[string]any{"type": "exit", "terminalId": terminalID, "code": intNumberDefault(terminal["exitCode"], 0)})
	}
}

func (h *Hub) terminalSnapshot(terminalID string) (map[string]any, bool, error) {
	h.mu.RLock()
	runtime := h.terminalRuntime
	h.mu.RUnlock()
	snapshotter, ok := runtime.(terminalRuntimeSnapshotter)
	if !ok {
		return nil, false, nil
	}
	snapshot, err := snapshotter.Snapshot(context.Background(), terminalID)
	if err != nil {
		return nil, false, err
	}
	if snapshot == nil {
		return nil, false, nil
	}
	if _, ok := snapshot["type"].(string); !ok {
		snapshot["type"] = "snapshot"
	}
	snapshot["terminalId"] = terminalID
	if _, ok := snapshot["cols"]; !ok {
		snapshot["cols"] = 80
	}
	if _, ok := snapshot["rows"]; !ok {
		snapshot["rows"] = 24
	}
	return snapshot, true, nil
}

func terminalStatus(terminal map[string]any) string {
	if terminal == nil {
		return ""
	}
	status, _ := terminal["status"].(string)
	return status
}

func (h *Hub) handleNarratorSubscribe(c *client, ids []string, msg map[string]any) {
	var catchUpAnchor any
	catchUpNarratorID := ""
	if len(ids) == 1 {
		if cursor, ok := msg["catchUpCursor"]; ok && cursor != nil {
			catchUpAnchor = cursor
		} else if lastMessageID, ok := msg["lastMessageId"].(string); ok && strings.TrimSpace(lastMessageID) != "" {
			catchUpAnchor = lastMessageID
		}
		if catchUpAnchor != nil {
			catchUpNarratorID = ids[0]
		}
	}
	for _, id := range ids {
		if id == catchUpNarratorID {
			continue
		}
		h.subscribe(c, id)
	}
	for _, id := range ids {
		h.sendStreamingSnapshot(c, id)
		h.sendTerminalCount(c, id)
		h.sendBrowserSessionCount(c, id)
	}
	if catchUpNarratorID == "" {
		return
	}
	h.sendCatchUpThenSubscribe(c, catchUpNarratorID, catchUpAnchor)
	h.sendTerminalCount(c, catchUpNarratorID)
	h.sendBrowserSessionCount(c, catchUpNarratorID)
}

func (h *Hub) sendTerminalCount(c *client, narratorID string) {
	count, fallbacks := h.activeTerminalCount(context.Background(), narratorID)
	if count <= 0 && len(fallbacks) == 0 {
		return
	}
	c.sendJSON(map[string]any{"type": "terminal_count_changed", "narratorId": narratorID, "activeTerminalCount": count, "count": count, "degraded": len(fallbacks) > 0, "fallbacks": fallbacks})
}

func (h *Hub) activeTerminalCount(ctx context.Context, narratorID string) (int, []string) {
	narratorID = strings.TrimSpace(narratorID)
	if narratorID == "" {
		return 0, nil
	}
	sqlDB := h.narratorDB()
	if sqlDB == nil {
		return 0, nil
	}
	var tableName string
	if err := sqlDB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'terminals'`).Scan(&tableName); err != nil {
		return 0, nil
	}
	var count int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM terminals WHERE narrator_id = ? AND status = 'running'`, narratorID).Scan(&count); err != nil {
		return 0, []string{"terminal_count_query_failed"}
	}
	return count, nil
}

func (h *Hub) sendBrowserSessionCount(c *client, narratorID string) {
	count := h.activeBrowserSessionCount(context.Background(), narratorID)
	if count <= 0 {
		return
	}
	c.sendJSON(map[string]any{"type": "browser_session_count", "narratorId": narratorID, "activeBrowserSessions": count})
}

func (h *Hub) activeBrowserSessionCount(ctx context.Context, narratorID string) int {
	narratorID = strings.TrimSpace(narratorID)
	if narratorID == "" {
		return 0
	}
	h.mu.RLock()
	sqlDB := h.browserSessionDB
	if sqlDB == nil {
		sqlDB = h.terminalDB
	}
	h.mu.RUnlock()
	if sqlDB == nil {
		return 0
	}
	var tableName string
	if err := sqlDB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'narrator_browser_sessions'`).Scan(&tableName); err != nil {
		return 0
	}
	var count int
	err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_browser_sessions WHERE narrator_id = ? AND status IN ('active', 'running', 'idle')`, narratorID).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func (h *Hub) handleBufferMessage(c *client, msg map[string]any) {
	ctx := context.Background()
	sqlDB := h.narratorDB()
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	narratorID := strings.TrimSpace(stringValue(msg["narratorId"]))
	text := stringValue(msg["text"])
	if narratorID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "narratorId is required"})
		return
	}
	if strings.TrimSpace(text) == "" {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": "text is required"})
		return
	}
	// Mirror the TypeScript narratorWsMessageSchema buffer_message bound: text max 100000.
	if len(text) > 100000 {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": "text must be at most 100000 characters"})
		return
	}
	if action, toolID, ok := parseWSOptionalToolSlashCommand(text); ok {
		if err := h.handleWSOptionalToolSlashCommand(ctx, sqlDB, c, narratorID, action, toolID); err != nil {
			c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		}
		return
	}
	seq := 0
	if err := sqlDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&seq); err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": "query next buffered message sequence: " + err.Error()})
		return
	}
	bufferID := id.Must(8)
	now := db.NowISO()
	_, err := sqlDB.ExecContext(ctx, `INSERT INTO narrator_buffered_messages (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL, ?, ?, ?)`, bufferID, narratorID, text, nullableString(stringValue(msg["commandText"])), nullableString(c.userID), boolIntAny(msg["priority"]), seq, now)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	h.broadcastBufferSetOrError(c, sqlDB, narratorID)
}

type wsOptionalToolDefinition struct {
	ID            string
	ToolName      string
	DescriptionEn string
	DescriptionZh string
}

func wsOptionalToolDefinitions() []wsOptionalToolDefinition {
	return []wsOptionalToolDefinition{
		{ID: "terminal", ToolName: "Terminal", DescriptionEn: "Interactive terminal — read buffer, send input, list terminals", DescriptionZh: "交互式终端 — 读取缓冲区、发送输入、列出终端"},
		{ID: "share_file", ToolName: "ShareFile", DescriptionEn: "Share files/directories — generate temporary download links", DescriptionZh: "分享文件/目录 — 生成临时下载链接"},
		{ID: "recall", ToolName: "Recall", DescriptionEn: "Search and browse all narrator conversations", DescriptionZh: "搜索和浏览所有叙述者对话"},
		{ID: "browser", ToolName: "Browser", DescriptionEn: "Interactive browser — navigate, click, fill forms, take screenshots", DescriptionZh: "交互式浏览器 — 导航、点击、填写表单、截图"},
		{ID: "fork_narrator", ToolName: "ForkNarrator", DescriptionEn: "Fork the current narrator session into a new chapter", DescriptionZh: "将当前叙述者会话分叉到新章节"},
		{ID: "narrafork_admin", ToolName: "NarraForkAdmin", DescriptionEn: "Admin settings management — get and modify all NarraFork settings", DescriptionZh: "管理员设置管理 — 获取和修改所有 NarraFork 设置"},
	}
}

func parseWSOptionalToolSlashCommand(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	body := strings.TrimPrefix(trimmed, "/")
	name, input, _ := strings.Cut(body, " ")
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "load" && name != "unload" {
		return "", "", false
	}
	toolID := strings.ToLower(strings.TrimSpace(input))
	if toolID == "" {
		return "", "", false
	}
	return name, toolID, true
}

func wsSupportedOptionalToolNames() map[string]bool {
	supported := map[string]bool{}
	for _, tool := range agentbasic.ToolMenuCapabilities() {
		if stringValue(tool["category"]) != agentbasic.ToolCategoryOptional || tool["supported"] != true {
			continue
		}
		if name := strings.TrimSpace(stringValue(tool["name"])); name != "" {
			supported[name] = true
		}
	}
	return supported
}

func wsAvailableOptionalToolIDs() []string {
	supported := wsSupportedOptionalToolNames()
	ids := []string{}
	for _, tool := range wsOptionalToolDefinitions() {
		if supported[tool.ToolName] {
			ids = append(ids, tool.ID)
		}
	}
	return ids
}

func wsResolveOptionalToolByID(toolID string) (wsOptionalToolDefinition, bool) {
	toolID = strings.ToLower(strings.TrimSpace(toolID))
	supported := wsSupportedOptionalToolNames()
	for _, tool := range wsOptionalToolDefinitions() {
		if strings.ToLower(tool.ID) == toolID && supported[tool.ToolName] {
			return tool, true
		}
	}
	return wsOptionalToolDefinition{}, false
}

func (h *Hub) handleWSOptionalToolSlashCommand(ctx context.Context, sqlDB *sql.DB, c *client, narratorID, action, toolID string) error {
	locale := wsUserLocale(ctx, sqlDB, c.userID)
	tool, ok := wsResolveOptionalToolByID(toolID)
	if !ok {
		message := fmt.Sprintf("⚠️ Unknown tool: %s. Available: %s", toolID, strings.Join(wsAvailableOptionalToolIDs(), ", "))
		if locale == "zh-CN" {
			message = fmt.Sprintf("⚠️ 未知工具：%s。可用：%s", toolID, strings.Join(wsAvailableOptionalToolIDs(), ", "))
		}
		return h.persistWSDisplayMessage(ctx, sqlDB, narratorID, message)
	}
	if action == "unload" {
		return h.handleWSUnloadTool(ctx, sqlDB, narratorID, tool, locale)
	}
	return h.handleWSLoadTool(ctx, sqlDB, narratorID, tool, locale)
}

func (h *Hub) handleWSLoadTool(ctx context.Context, sqlDB *sql.DB, narratorID string, tool wsOptionalToolDefinition, locale string) error {
	current, traits, err := wsLoadNarratorToolState(ctx, sqlDB, narratorID)
	if err != nil {
		return err
	}
	if wsDisabledToolSetFromTraits(traits)[tool.ToolName] {
		message := fmt.Sprintf("⛔ Tool disabled by this narrator's custom trait: %s", tool.ToolName)
		if locale == "zh-CN" {
			message = fmt.Sprintf("⛔ 工具已被此叙述者的自定义 trait 禁用：%s", tool.ToolName)
		}
		return h.persistWSDisplayMessage(ctx, sqlDB, narratorID, message)
	}
	if wsStringSliceContains(current, tool.ToolName) {
		return h.persistWSDisplayMessage(ctx, sqlDB, narratorID, fmt.Sprintf("🔧 Tool already loaded: %s", tool.ToolName))
	}
	next := append(append([]string{}, current...), tool.ToolName)
	payload, _ := json.Marshal(next)
	if _, err := sqlDB.ExecContext(ctx, `UPDATE narrators SET enabled_tools = ?, updated_at = ? WHERE id = ?`, string(payload), db.NowISO(), narratorID); err != nil {
		return err
	}
	if err := h.persistWSDisplayMessage(ctx, sqlDB, narratorID, fmt.Sprintf("🔧 Tool loaded: %s", tool.ToolName)); err != nil {
		return err
	}
	description := tool.DescriptionEn
	if locale == "zh-CN" {
		description = tool.DescriptionZh
	}
	text := fmt.Sprintf("[The optional tool %q has just been loaded into this session. %s. You can now use this tool when appropriate.]", tool.ToolName, description)
	if locale == "zh-CN" {
		text = fmt.Sprintf("[可选工具 %q 刚刚被加载到本次会话中。%s。你现在可以在合适的时候使用这个工具。]", tool.ToolName, description)
	}
	return h.persistWSToolAvailabilityMessage(ctx, sqlDB, narratorID, "tool_loaded", tool.ToolName, text)
}

func (h *Hub) handleWSUnloadTool(ctx context.Context, sqlDB *sql.DB, narratorID string, tool wsOptionalToolDefinition, locale string) error {
	current, _, err := wsLoadNarratorToolState(ctx, sqlDB, narratorID)
	if err != nil {
		return err
	}
	if !wsStringSliceContains(current, tool.ToolName) {
		return h.persistWSDisplayMessage(ctx, sqlDB, narratorID, fmt.Sprintf("🔧 Tool not loaded: %s", tool.ToolName))
	}
	next := []string{}
	for _, existing := range current {
		if existing != tool.ToolName {
			next = append(next, existing)
		}
	}
	payload, _ := json.Marshal(next)
	if _, err := sqlDB.ExecContext(ctx, `UPDATE narrators SET enabled_tools = ?, updated_at = ? WHERE id = ?`, string(payload), db.NowISO(), narratorID); err != nil {
		return err
	}
	if err := h.persistWSDisplayMessage(ctx, sqlDB, narratorID, fmt.Sprintf("🔧 Tool unloaded: %s", tool.ToolName)); err != nil {
		return err
	}
	text := fmt.Sprintf("[The optional tool %q has just been unloaded from this session. Do not use this tool unless it is loaded again.]", tool.ToolName)
	if locale == "zh-CN" {
		text = fmt.Sprintf("[可选工具 %q 刚刚从本次会话中卸载。除非它再次被加载，否则不要使用这个工具。]", tool.ToolName)
	}
	return h.persistWSToolAvailabilityMessage(ctx, sqlDB, narratorID, "tool_unloaded", tool.ToolName, text)
}

func wsLoadNarratorToolState(ctx context.Context, sqlDB *sql.DB, narratorID string) ([]string, []string, error) {
	var enabledRaw, traitsRaw sql.NullString
	err := sqlDB.QueryRowContext(ctx, `SELECT enabled_tools, traits FROM narrators WHERE id = ?`, narratorID).Scan(&enabledRaw, &traitsRaw)
	if err != nil {
		return nil, nil, err
	}
	return wsStringSliceFromJSON(enabledRaw), wsStringSliceFromJSON(traitsRaw), nil
}

func wsStringSliceFromJSON(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw.String), &values); err == nil {
		return wsCleanStringSlice(values)
	}
	var anyValues []any
	if err := json.Unmarshal([]byte(raw.String), &anyValues); err != nil {
		return nil
	}
	out := []string{}
	for _, value := range anyValues {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func wsCleanStringSlice(values []string) []string {
	out := []string{}
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func wsStringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func wsDisabledToolSetFromTraits(traits []string) map[string]bool {
	set := map[string]bool{}
	const prefix = "custom-disabled-tools:"
	for _, trait := range traits {
		if !strings.HasPrefix(trait, prefix) {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trait, prefix))
		if err != nil {
			continue
		}
		var decoded struct {
			Version int      `json:"version"`
			Tools   []string `json:"tools"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Version != 1 {
			continue
		}
		for _, tool := range decoded.Tools {
			if name := strings.TrimSpace(tool); name != "" {
				set[name] = true
			}
		}
	}
	return set
}

func wsUserLocale(ctx context.Context, sqlDB *sql.DB, userID string) string {
	if strings.TrimSpace(userID) == "" {
		return "en"
	}
	var locale sql.NullString
	if err := sqlDB.QueryRowContext(ctx, `SELECT language FROM user_preferences WHERE user_id = ?`, userID).Scan(&locale); err != nil {
		return "en"
	}
	if strings.TrimSpace(locale.String) == "zh-CN" {
		return "zh-CN"
	}
	return "en"
}

func (h *Hub) persistWSDisplayMessage(ctx context.Context, sqlDB *sql.DB, narratorID, text string) error {
	msg, err := insertWSNarratorMessage(ctx, sqlDB, narratorID, "disp", "[Info] "+text, []map[string]any{{"type": "info", "message": text}})
	if err != nil {
		return err
	}
	h.BroadcastNarrator(narratorID, map[string]any{"type": "message", "narratorId": narratorID, "message": msg})
	return nil
}

func (h *Hub) persistWSToolAvailabilityMessage(ctx context.Context, sqlDB *sql.DB, narratorID, blockType, toolName, text string) error {
	msg, err := insertWSNarratorMessage(ctx, sqlDB, narratorID, "user", text, []map[string]any{{"type": blockType, "toolName": toolName, "text": text}})
	if err != nil {
		return err
	}
	h.BroadcastNarrator(narratorID, map[string]any{"type": "user_message", "narratorId": narratorID, "message": msg})
	return nil
}

func insertWSNarratorMessage(ctx context.Context, sqlDB *sql.DB, narratorID, role, contentText string, content any) (map[string]any, error) {
	msgID := id.New()
	refID := id.New()
	now := db.NowISO()
	contentJSON, _ := json.Marshal(content)
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, ?, ?, ?, ?)`, msgID, narratorID, role, string(contentJSON), contentText, now); err != nil {
		return nil, err
	}
	seq := 1
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM narrator_message_refs WHERE narrator_id = ?`, narratorID).Scan(&seq); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, refID, narratorID, msgID, seq); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = COALESCE(message_count, 0) + 1, message_version = message_version + 1, last_message_at = ?, updated_at = ? WHERE id = ?`, now, now, narratorID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": msgID, "narratorId": narratorID, "role": role, "contentJson": content, "contentText": contentText, "createdAt": now, "seq": seq, "children": []any{}}, nil
}

func (h *Hub) handleCancelBuffer(c *client, msg map[string]any) {
	sqlDB := h.narratorDB()
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	narratorID := strings.TrimSpace(stringValue(msg["narratorId"]))
	if narratorID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "narratorId is required"})
		return
	}
	if _, err := sqlDB.ExecContext(context.Background(), `DELETE FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID); err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	h.BroadcastNarrator(narratorID, map[string]any{"type": "buffer_cleared", "narratorId": narratorID, "reason": "cancelled"})
}

func (h *Hub) handleUpdateBuffer(c *client, msg map[string]any) {
	sqlDB := h.narratorDB()
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	narratorID := strings.TrimSpace(stringValue(msg["narratorId"]))
	messageID := strings.TrimSpace(stringValue(msg["messageId"]))
	if narratorID == "" || messageID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "narratorId and messageId are required"})
		return
	}
	sets := []string{}
	args := []any{}
	if text, ok := msg["text"].(string); ok {
		// Mirror the TypeScript narratorWsMessageSchema update_buffer bound: text min 1 / max 100000.
		if strings.TrimSpace(text) == "" {
			c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": "text is required"})
			return
		}
		if len(text) > 100000 {
			c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": "text must be at most 100000 characters"})
			return
		}
		sets = append(sets, "text = ?")
		args = append(args, text)
	}
	if commandText, ok := msg["commandText"].(string); ok {
		sets = append(sets, "command_text = ?")
		args = append(args, nullableString(commandText))
	}
	if priority, ok := msg["priority"]; ok {
		sets = append(sets, "priority = ?")
		args = append(args, boolIntAny(priority))
	}
	if len(sets) == 0 {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": "no buffer fields to update"})
		return
	}
	args = append(args, messageID, narratorID)
	result, err := sqlDB.ExecContext(context.Background(), `UPDATE narrator_buffered_messages SET `+strings.Join(sets, ", ")+` WHERE id = ? AND narrator_id = ?`, args...)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "messageId": messageID, "message": "Buffered message not found"})
		return
	}
	h.broadcastBufferSetOrError(c, sqlDB, narratorID)
}

func (h *Hub) handleRemoveBuffer(c *client, msg map[string]any) {
	sqlDB := h.narratorDB()
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	narratorID := strings.TrimSpace(stringValue(msg["narratorId"]))
	messageID := strings.TrimSpace(stringValue(msg["messageId"]))
	if narratorID == "" || messageID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "narratorId and messageId are required"})
		return
	}
	if _, err := sqlDB.ExecContext(context.Background(), `DELETE FROM narrator_buffered_messages WHERE id = ? AND narrator_id = ?`, messageID, narratorID); err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	h.broadcastBufferSetOrError(c, sqlDB, narratorID)
}

func (h *Hub) handleStatsSubscription(c *client, msg map[string]any, subscribed bool) {
	narratorID := strings.TrimSpace(stringValue(msg["narratorId"]))
	payload := map[string]any{"type": "output_stats", "charsPerSecond": 0, "tokensPerSecond": 0, "subscribed": subscribed}
	if narratorID != "" {
		payload["narratorId"] = narratorID
	}
	c.sendJSON(payload)
}

func (h *Hub) handleUpdateTimeout(c *client, msg map[string]any) {
	ctx := context.Background()
	sqlDB := h.narratorDB()
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	toolUseID := strings.TrimSpace(stringValue(msg["toolUseId"]))
	if toolUseID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "toolUseId is required"})
		return
	}
	// Mirror the TypeScript narratorWsMessageSchema update_timeout bound:
	// timeoutMs int 1000..86400000. Validate before any DB lookup/mutation.
	rawTimeout := msg["timeout"]
	if rawTimeout == nil {
		rawTimeout = msg["timeoutMs"]
	}
	timeoutMs := intNumber(rawTimeout)
	if timeoutMs < 1000 || timeoutMs > 86_400_000 {
		c.sendJSON(map[string]any{"type": "error", "toolUseId": toolUseID, "message": "timeoutMs must be between 1000 and 86400000"})
		return
	}
	var narratorID, inputRaw sql.NullString
	err := sqlDB.QueryRowContext(ctx, `SELECT narrator_id, input_json FROM narrator_tool_calls WHERE id = ? OR tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, toolUseID, toolUseID).Scan(&narratorID, &inputRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.sendJSON(map[string]any{"type": "error", "toolUseId": toolUseID, "message": "Tool call not found"})
			return
		}
		c.sendJSON(map[string]any{"type": "error", "toolUseId": toolUseID, "message": err.Error()})
		return
	}
	input := map[string]any{}
	if inputRaw.Valid && strings.TrimSpace(inputRaw.String) != "" {
		if err := json.Unmarshal([]byte(inputRaw.String), &input); err != nil {
			c.sendJSON(map[string]any{"type": "error", "toolUseId": toolUseID, "message": err.Error()})
			return
		}
	}
	timeoutValue := msg["timeout"]
	if timeoutValue == nil {
		timeoutValue = msg["timeoutMs"]
	}
	input["timeout"] = timeoutValue
	data, _ := json.Marshal(input)
	result, err := sqlDB.ExecContext(ctx, `UPDATE narrator_tool_calls SET input_json = ? WHERE id = ? OR tool_use_id = ?`, string(data), toolUseID, toolUseID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "toolUseId": toolUseID, "message": err.Error()})
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.sendJSON(map[string]any{"type": "error", "toolUseId": toolUseID, "message": "Tool call not found"})
		return
	}
	payload := map[string]any{"type": "timeout_updated", "toolUseId": toolUseID, "timeout": timeoutValue, "input": input}
	if _, ok := msg["timeoutMs"]; ok {
		payload["timeoutMs"] = timeoutValue
	}
	if narratorID.Valid && narratorID.String != "" {
		payload["narratorId"] = narratorID.String
		h.BroadcastNarrator(narratorID.String, payload)
		return
	}
	c.sendJSON(payload)
}

func (h *Hub) broadcastBufferSetOrError(c *client, sqlDB *sql.DB, narratorID string) {
	items, err := loadNarratorBuffer(context.Background(), sqlDB, narratorID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	h.BroadcastNarrator(narratorID, map[string]any{"type": "buffer_set", "narratorId": narratorID, "messages": items})
}

func (h *Hub) narratorDB() *sql.DB {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.terminalDB
}

func loadNarratorBuffer(ctx context.Context, sqlDB *sql.DB, narratorID string) ([]map[string]any, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, buffered_at, priority, seq FROM narrator_buffered_messages WHERE narrator_id = ? ORDER BY seq ASC, buffered_at ASC LIMIT 200`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, text, bufferedAt string
		var imagesRaw, commandText, createdBy, creatorRaw, textFilePathsRaw sql.NullString
		var priority, seq int
		if err := rows.Scan(&idValue, &text, &imagesRaw, &commandText, &createdBy, &creatorRaw, &textFilePathsRaw, &bufferedAt, &priority, &seq); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "text": text, "images": decodeJSONNull(imagesRaw), "commandText": nullableNullString(commandText), "createdBy": nullableNullString(createdBy), "creator": decodeJSONNull(creatorRaw), "textFilePaths": decodeJSONNull(textFilePathsRaw), "bufferedAt": bufferedAt, "priority": priority != 0, "seq": seq})
	}
	return items, rows.Err()
}

func decodeJSONNull(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw.String), &decoded); err != nil {
		return raw.String
	}
	return decoded
}

func boolIntAny(value any) int {
	switch typed := value.(type) {
	case bool:
		if typed {
			return 1
		}
	case float64:
		if typed != 0 {
			return 1
		}
	case int:
		if typed != 0 {
			return 1
		}
	case int64:
		if typed != 0 {
			return 1
		}
	case json.Number:
		if n, err := typed.Int64(); err == nil && n != 0 {
			return 1
		}
	}
	return 0
}

func (h *Hub) handleSyncCheck(c *client, msg map[string]any) {
	narratorID, _ := msg["narratorId"].(string)
	narratorID = strings.TrimSpace(narratorID)
	if narratorID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "narratorId is required"})
		return
	}
	provider := h.narratorSyncProvider()
	if provider == nil {
		c.sendJSON(map[string]any{"type": "full_reload", "narratorId": narratorID})
		return
	}
	serverVersion, err := provider.NarratorMessageVersion(context.Background(), narratorID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	if serverVersion == intNumber(msg["version"]) {
		c.sendJSON(map[string]any{"type": "sync_ok", "narratorId": narratorID, "version": serverVersion})
		return
	}
	anchor := narratorCatchUpAnchor(msg)
	if anchor == nil {
		c.sendJSON(map[string]any{"type": "full_reload", "narratorId": narratorID})
		return
	}
	page, err := provider.NarratorMessagesAfter(context.Background(), narratorID, anchor, 40)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	if boolValue(page["hitLimit"]) || (messageListLen(page["topLevel"]) == 0 && messageListLen(page["orphanChildren"]) == 0) {
		c.sendJSON(map[string]any{"type": "full_reload", "narratorId": narratorID})
		return
	}
	payload := map[string]any{
		"type":           "catch_up",
		"narratorId":     narratorID,
		"topLevel":       sliceOrEmpty(page["topLevel"]),
		"orphanChildren": sliceOrEmpty(page["orphanChildren"]),
		"cursor":         page["cursor"],
		"messageVersion": serverVersion,
	}
	c.sendJSON(payload)
}

func (h *Hub) sendCatchUpThenSubscribe(c *client, narratorID string, anchor any) {
	provider := h.narratorSyncProvider()
	if provider == nil {
		h.subscribe(c, narratorID)
		c.sendJSON(map[string]any{"type": "full_reload", "narratorId": narratorID})
		return
	}
	page, err := provider.NarratorMessagesAfter(context.Background(), narratorID, anchor, 40)
	if err != nil {
		h.subscribe(c, narratorID)
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	version, err := provider.NarratorMessageVersion(context.Background(), narratorID)
	if err != nil {
		h.subscribe(c, narratorID)
		c.sendJSON(map[string]any{"type": "error", "narratorId": narratorID, "message": err.Error()})
		return
	}
	if boolValue(page["hitLimit"]) {
		h.subscribe(c, narratorID)
		c.sendJSON(map[string]any{"type": "full_reload", "narratorId": narratorID})
		return
	}
	if messageListLen(page["topLevel"]) == 0 && messageListLen(page["orphanChildren"]) == 0 {
		c.sendJSON(map[string]any{"type": "sync_ok", "narratorId": narratorID, "version": version})
	} else {
		c.sendJSON(map[string]any{
			"type":           "catch_up",
			"narratorId":     narratorID,
			"topLevel":       sliceOrEmpty(page["topLevel"]),
			"orphanChildren": sliceOrEmpty(page["orphanChildren"]),
			"cursor":         page["cursor"],
			"messageVersion": version,
		})
	}
	h.subscribe(c, narratorID)
	latestVersion, err := provider.NarratorMessageVersion(context.Background(), narratorID)
	if err != nil || latestVersion == version || page["cursor"] == nil {
		return
	}
	delta, err := provider.NarratorMessagesAfter(context.Background(), narratorID, page["cursor"], 40)
	if err != nil {
		return
	}
	if boolValue(delta["hitLimit"]) {
		c.sendJSON(map[string]any{"type": "full_reload", "narratorId": narratorID})
	} else if messageListLen(delta["topLevel"]) > 0 || messageListLen(delta["orphanChildren"]) > 0 {
		c.sendJSON(map[string]any{
			"type":           "catch_up",
			"narratorId":     narratorID,
			"topLevel":       sliceOrEmpty(delta["topLevel"]),
			"orphanChildren": sliceOrEmpty(delta["orphanChildren"]),
			"cursor":         delta["cursor"],
			"messageVersion": latestVersion,
		})
	}
}

func (h *Hub) handlePermissionDecision(c *client, msg map[string]any) {
	ctx := context.Background()
	sqlDB := h.terminalDB
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	requestID, _ := msg["requestId"].(string)
	if strings.TrimSpace(requestID) == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "requestId is required"})
		return
	}
	decision, _ := msg["decision"].(string)
	status := permissionDecisionDBStatus(decision)
	updatedInput, err := mergePermissionAnswers(ctx, sqlDB, requestID, mapValue(msg["answers"]))
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	message, _ := msg["message"].(string)
	reason, _ := msg["feedbackText"].(string)
	if reason == "" {
		reason, _ = msg["reason"].(string)
	}
	now := db.NowISO()
	_, err = sqlDB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = ?, permission_decided_by = ?, permission_decided_at = ?, permission_decision_reason = ?, permission_deny_message = ? WHERE id = ? OR tool_use_id = ?`, status, c.userID, now, nullableString(reason), nullableString(message), requestID, requestID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	narratorID, toolUseID, err := permissionTarget(ctx, sqlDB, requestID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	if narratorID != "" {
		h.BroadcastNarrator(narratorID, map[string]any{"type": "permission_resolved", "narratorId": narratorID, "requestId": requestID, "toolUseId": toolUseID, "updatedInput": updatedInput, "decision": map[bool]string{true: "allow", false: "deny"}[status == "running"]})
	}
}

func (h *Hub) handleMergeDecision(c *client, msg map[string]any) {
	h.mu.RLock()
	sqlDB := h.mergeDB
	h.mu.RUnlock()
	if sqlDB == nil {
		c.sendJSON(map[string]any{"type": "error", "message": "database is not configured"})
		return
	}
	mergeSessionID := strings.TrimSpace(stringValue(msg["mergeSessionId"]))
	if mergeSessionID == "" {
		c.sendJSON(map[string]any{"type": "error", "message": "mergeSessionId is required"})
		return
	}
	decision := strings.ToLower(strings.TrimSpace(stringValue(msg["decision"])))
	status := "cancelled"
	if decision == "continue" {
		status = "running"
	} else if decision != "cancel" && decision != "cancelled" {
		c.sendJSON(map[string]any{"type": "error", "message": "decision must be continue or cancel"})
		return
	}
	projectID, hasProjectMetadata, err := mergeSessionProjectID(context.Background(), sqlDB, mergeSessionID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	if hasProjectMetadata {
		allowed, err := h.clientCanAccessMergeProject(context.Background(), sqlDB, c, projectID, false)
		if err != nil {
			c.sendJSON(map[string]any{"type": "error", "message": err.Error()})
			return
		}
		if !allowed {
			c.sendJSON(map[string]any{"type": "error", "message": "Merge session is not available for this project"})
			return
		}
	}
	result, err := sqlDB.ExecContext(context.Background(), `UPDATE merge_sessions SET status = ?, updated_at = ? WHERE id = ?`, status, db.NowISO(), mergeSessionID)
	if err != nil {
		c.sendJSON(map[string]any{"type": "error", "message": err.Error()})
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.sendJSON(map[string]any{"type": "error", "message": "Merge session not found"})
		return
	}
	c.sendJSON(map[string]any{"type": "merge_decision_resolved", "mergeSessionId": mergeSessionID, "decision": decision, "status": status})
}

func mergeSessionProjectID(ctx context.Context, sqlDB *sql.DB, mergeSessionID string) (string, bool, error) {
	if sqlDB == nil {
		return "", false, nil
	}
	chaptersExist, err := wsTableExists(ctx, sqlDB, "chapters")
	if err != nil || !chaptersExist {
		return "", false, err
	}
	var projectID sql.NullString
	err = sqlDB.QueryRowContext(ctx, `SELECT ch.project_id FROM merge_sessions ms LEFT JOIN chapters ch ON ch.id = ms.target_chapter_id WHERE ms.id = ?`, mergeSessionID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", true, fmt.Errorf("Merge session not found")
	}
	if err != nil {
		return "", false, err
	}
	if !projectID.Valid || strings.TrimSpace(projectID.String) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(projectID.String), true, nil
}

func (h *Hub) clientCanAccessMergeProject(ctx context.Context, sqlDB *sql.DB, c *client, projectID string, allowEmptySubscription bool) (bool, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || sqlDB == nil {
		return true, nil
	}
	narratorsExist, err := wsTableExists(ctx, sqlDB, "narrators")
	if err != nil || !narratorsExist {
		return true, err
	}
	chaptersExist, err := wsTableExists(ctx, sqlDB, "chapters")
	if err != nil || !chaptersExist {
		return true, err
	}
	subscriberIDs := h.clientSubscriberIDs(c)
	if len(subscriberIDs) == 0 {
		return allowEmptySubscription, nil
	}
	placeholders, args := wsSQLPlaceholders(subscriberIDs)
	args = append(args, projectID)
	var count int
	err = sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrators n JOIN chapters ch ON ch.id = n.chapter_id WHERE n.id IN (`+placeholders+`) AND ch.project_id = ?`, args...).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (h *Hub) clientSubscriberIDs(c *client) []string {
	if c == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(c.subscribers))
	for id := range c.subscribers {
		id = strings.TrimSpace(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func wsTableExists(ctx context.Context, sqlDB *sql.DB, table string) (bool, error) {
	var name string
	err := sqlDB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return name == table, nil
}

func wsSQLPlaceholders(values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}

func permissionDecisionDBStatus(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "approved", "approve":
		return "running"
	default:
		return "fail"
	}
}

func mergePermissionAnswers(ctx context.Context, sqlDB *sql.DB, requestID string, answers map[string]any) (any, error) {
	if len(answers) == 0 {
		return nil, nil
	}
	var inputRaw sql.NullString
	err := sqlDB.QueryRowContext(ctx, `SELECT input_json FROM narrator_tool_calls WHERE id = ? OR tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, requestID, requestID).Scan(&inputRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	input := map[string]any{}
	if inputRaw.Valid && strings.TrimSpace(inputRaw.String) != "" {
		if err := json.Unmarshal([]byte(inputRaw.String), &input); err != nil {
			input = map[string]any{"raw": inputRaw.String}
		}
	}
	input["answers"] = answers
	data, _ := json.Marshal(input)
	_, err = sqlDB.ExecContext(ctx, `UPDATE narrator_tool_calls SET input_json = ? WHERE id = ? OR tool_use_id = ?`, string(data), requestID, requestID)
	if err != nil {
		return nil, err
	}
	return input, nil
}

func permissionTarget(ctx context.Context, sqlDB *sql.DB, requestID string) (string, string, error) {
	var narratorID, toolUseID sql.NullString
	err := sqlDB.QueryRowContext(ctx, `SELECT narrator_id, tool_use_id FROM narrator_tool_calls WHERE id = ? OR tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, requestID, requestID).Scan(&narratorID, &toolUseID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("query permission target: %w", err)
	}
	return sqlString(narratorID), sqlString(toolUseID), nil
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func (h *Hub) sendStreamingSnapshot(c *client, narratorID string) {
	h.mu.RLock()
	snap := h.streamingSnapshots[narratorID]
	if snap == nil {
		h.mu.RUnlock()
		return
	}
	blocks := cloneMapSlice(snap.StreamingBlocks)
	chunks := make([]map[string]any, 0, len(snap.ToolChunks))
	for _, chunk := range snap.ToolChunks {
		chunks = append(chunks, cloneMap(chunk))
	}
	var queuePosition *int
	if snap.NKPQueuePosition != nil {
		position := *snap.NKPQueuePosition
		queuePosition = &position
	}
	var queueDepth *int
	if snap.NKPQueueDepth != nil {
		depth := *snap.NKPQueueDepth
		queueDepth = &depth
	}
	queueMessage := snap.QueueMessage
	h.mu.RUnlock()

	hasStreaming := len(blocks) > 0 || len(chunks) > 0
	hasQueue := queuePosition != nil && *queuePosition > 0
	hasQueueMessage := queueMessage != ""
	if !hasStreaming && !hasQueue && !hasQueueMessage {
		return
	}
	if hasStreaming {
		c.sendJSON(map[string]any{"type": "streaming_snapshot", "narratorId": narratorID, "streamingBlocks": blocks, "toolChunks": chunks})
	}
	if hasQueueMessage {
		payload := map[string]any{"type": "queue_status", "narratorId": narratorID, "queueMessage": queueMessage}
		if queuePosition != nil {
			payload["position"] = *queuePosition
		}
		if queueDepth != nil {
			payload["queueDepth"] = *queueDepth
		}
		c.sendJSON(payload)
	} else if hasQueue {
		depth := 0
		if queueDepth != nil {
			depth = *queueDepth
		}
		c.sendJSON(map[string]any{"type": "nkp_queue_status", "narratorId": narratorID, "position": *queuePosition, "queueDepth": depth})
	}
}

func (h *Hub) recordStreamingSnapshotLocked(narratorID string, payload map[string]any) {
	if payload == nil || strings.TrimSpace(narratorID) == "" {
		return
	}
	typeValue, _ := payload["type"].(string)
	switch typeValue {
	case "stream_text":
		text, _ := payload["text"].(string)
		if text == "" {
			return
		}
		snap := h.getOrCreateStreamingSnapshotLocked(narratorID)
		outputIndex, hasOutputIndex := optionalInt(payload["outputIndex"])
		if hasOutputIndex {
			for _, block := range snap.StreamingBlocks {
				if block["type"] == "text" && intNumber(block["outputIndex"]) == outputIndex {
					block["text"] = stringFromAny(block["text"]) + text
					return
				}
			}
		} else if len(snap.StreamingBlocks) > 0 {
			last := snap.StreamingBlocks[len(snap.StreamingBlocks)-1]
			if last["type"] == "text" {
				last["text"] = stringFromAny(last["text"]) + text
				return
			}
		}
		block := map[string]any{"type": "text", "text": text}
		if hasOutputIndex {
			block["outputIndex"] = outputIndex
		}
		snap.StreamingBlocks = insertStreamingBlock(snap.StreamingBlocks, block)
	case "stream_reasoning":
		text, _ := payload["text"].(string)
		if text == "" {
			return
		}
		snap := h.getOrCreateStreamingSnapshotLocked(narratorID)
		outputIndex, hasOutputIndex := optionalInt(payload["outputIndex"])
		reasoningID, _ := payload["id"].(string)
		for _, block := range snap.StreamingBlocks {
			if block["type"] != "reasoning" {
				continue
			}
			if (reasoningID != "" && stringFromAny(block["id"]) == reasoningID) || (hasOutputIndex && intNumber(block["outputIndex"]) == outputIndex) || (reasoningID == "" && !hasOutputIndex && block["id"] == nil && block["outputIndex"] == nil) {
				block["text"] = stringFromAny(block["text"]) + text
				if reasoningID != "" {
					block["id"] = reasoningID
				}
				if hasOutputIndex {
					block["outputIndex"] = outputIndex
				}
				return
			}
		}
		block := map[string]any{"type": "reasoning", "text": text}
		if reasoningID != "" {
			block["id"] = reasoningID
		}
		if hasOutputIndex {
			block["outputIndex"] = outputIndex
		}
		snap.StreamingBlocks = insertStreamingBlock(snap.StreamingBlocks, block)
	case "web_search", "image_generation":
		idValue, _ := payload["id"].(string)
		if idValue == "" {
			return
		}
		snap := h.getOrCreateStreamingSnapshotLocked(narratorID)
		for i, block := range snap.StreamingBlocks {
			if block["type"] == typeValue && stringFromAny(block["id"]) == idValue {
				snap.StreamingBlocks[i] = cloneSelected(payload, "type", "id", "status", "query", "queries", "revisedPrompt", "result", "outputIndex")
				return
			}
		}
		snap.StreamingBlocks = insertStreamingBlock(snap.StreamingBlocks, cloneSelected(payload, "type", "id", "status", "query", "queries", "revisedPrompt", "result", "outputIndex"))
	case "tool_call", "tool_started", "tool_use_chunk":
		toolUseID, _ := payload["toolUseId"].(string)
		if toolUseID == "" {
			return
		}
		snap := h.getOrCreateStreamingSnapshotLocked(narratorID)
		chunk := snap.ToolChunks[toolUseID]
		if chunk == nil {
			chunk = map[string]any{"toolUseId": toolUseID}
		}
		mergeSelected(chunk, payload, "toolName", "inputCharsTotal", "extractedFilePath", "contentCharsReceived", "extractedFields", "metadata", "parentToolUseId", "streamingField", "streamStartedAt", "input", "streamingOutput")
		if _, ok := chunk["inputCharsTotal"]; !ok {
			chunk["inputCharsTotal"] = 0
		}
		if typeValue == "tool_call" || typeValue == "tool_started" {
			chunk["started"] = true
		}
		snap.ToolChunks[toolUseID] = chunk
	case "tool_output":
		toolUseID, _ := payload["toolUseId"].(string)
		if toolUseID == "" {
			return
		}
		if snap := h.streamingSnapshots[narratorID]; snap != nil && snap.ToolChunks[toolUseID] != nil {
			snap.ToolChunks[toolUseID]["streamingOutput"] = payload["output"]
		}
	case "tool_result", "tool_completed":
		toolUseID, _ := payload["toolUseId"].(string)
		if snap := h.streamingSnapshots[narratorID]; snap != nil && toolUseID != "" {
			delete(snap.ToolChunks, toolUseID)
		}
	case "nkp_queue_status":
		snap := h.getOrCreateStreamingSnapshotLocked(narratorID)
		if position, ok := optionalInt(payload["position"]); ok {
			snap.NKPQueuePosition = intPtr(position)
		}
		if queueDepth, ok := optionalInt(payload["queueDepth"]); ok {
			snap.NKPQueueDepth = intPtr(queueDepth)
		}
	case "queue_status":
		snap := h.getOrCreateStreamingSnapshotLocked(narratorID)
		if position, ok := optionalInt(payload["position"]); ok {
			snap.NKPQueuePosition = intPtr(position)
		}
		if queueDepth, ok := optionalInt(payload["queueDepth"]); ok {
			snap.NKPQueueDepth = intPtr(queueDepth)
		}
		snap.QueueMessage = stringValue(payload["queueMessage"])
	case "block_complete":
		block := mapValue(payload["block"])
		if snap := h.streamingSnapshots[narratorID]; snap != nil && block != nil {
			snap.StreamingBlocks = removeCompletedStreamingBlock(snap.StreamingBlocks, block)
		}
	case "assistant_message", "error", "invalid_state":
		delete(h.streamingSnapshots, narratorID)
	}
}

func (h *Hub) getOrCreateStreamingSnapshotLocked(narratorID string) *streamingSnapshot {
	snap := h.streamingSnapshots[narratorID]
	if snap == nil {
		snap = &streamingSnapshot{StreamingBlocks: []map[string]any{}, ToolChunks: map[string]map[string]any{}}
		h.streamingSnapshots[narratorID] = snap
	}
	return snap
}

func insertStreamingBlock(blocks []map[string]any, block map[string]any) []map[string]any {
	order, ok := optionalInt(block["outputIndex"])
	if !ok {
		return append(blocks, block)
	}
	idx := len(blocks)
	for i, current := range blocks {
		if currentOrder, hasOrder := optionalInt(current["outputIndex"]); hasOrder && currentOrder > order {
			idx = i
			break
		}
	}
	blocks = append(blocks, nil)
	copy(blocks[idx+1:], blocks[idx:])
	blocks[idx] = block
	return blocks
}

func removeCompletedStreamingBlock(blocks []map[string]any, block map[string]any) []map[string]any {
	blockType, _ := block["type"].(string)
	if blockType == "" {
		return blocks
	}
	matchIndex := -1
	for i := len(blocks) - 1; i >= 0; i-- {
		current := blocks[i]
		if current["type"] != blockType {
			continue
		}
		if idValue := stringFromAny(block["id"]); idValue != "" && stringFromAny(current["id"]) != idValue {
			continue
		}
		if order, ok := optionalInt(block["outputIndex"]); ok && intNumber(current["outputIndex"]) != order {
			continue
		}
		matchIndex = i
		break
	}
	if matchIndex == -1 {
		return blocks
	}
	return append(blocks[:matchIndex], blocks[matchIndex+1:]...)
}

func narratorCatchUpAnchor(msg map[string]any) any {
	if cursor, ok := msg["catchUpCursor"]; ok && cursor != nil {
		return cursor
	}
	if lastMessageID, ok := msg["lastMessageId"].(string); ok && strings.TrimSpace(lastMessageID) != "" {
		return strings.TrimSpace(lastMessageID)
	}
	return nil
}

func sqlString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func (h *Hub) createTerminal(ctx context.Context, msg map[string]any) (map[string]any, error) {
	h.mu.RLock()
	sqlDB := h.terminalDB
	runtime := h.terminalRuntime
	h.mu.RUnlock()
	if sqlDB == nil {
		return nil, errors.New("terminal database is not configured")
	}
	chapterID, _ := msg["chapterId"].(string)
	narratorID, _ := msg["narratorId"].(string)
	chapterID = strings.TrimSpace(chapterID)
	narratorID = strings.TrimSpace(narratorID)
	if chapterID == "" && narratorID == "" {
		return nil, errors.New("chapterId or narratorId is required")
	}
	name, _ := msg["name"].(string)
	// Mirror the TypeScript terminalWsMessageSchema create bounds: name max 100,
	// cols 10..500 and rows 2..200 when provided.
	if len(name) > 100 {
		return nil, errors.New("Invalid terminal create request")
	}
	if _, ok := msg["cols"]; ok {
		cols := intNumber(msg["cols"])
		if cols < 10 || cols > 500 {
			return nil, errors.New("Invalid terminal create request")
		}
	}
	if _, ok := msg["rows"]; ok {
		rows := intNumber(msg["rows"])
		if rows < 2 || rows > 200 {
			return nil, errors.New("Invalid terminal create request")
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Terminal"
	}
	terminalID := id.New()
	now := db.NowISO()
	cwd, err := terminaldb.ResolveCwd(ctx, sqlDB, chapterID, narratorID)
	if err != nil {
		return nil, err
	}
	_, err = sqlDB.ExecContext(ctx, `INSERT INTO terminals (id, chapter_id, narrator_id, name, cwd, status, created_at) VALUES (?, ?, ?, ?, ?, 'running', ?)`, terminalID, nullableString(chapterID), nullableString(narratorID), name, nullableNullString(cwd), now)
	if err != nil {
		return nil, err
	}
	terminal, err := loadTerminal(ctx, sqlDB, terminalID)
	if err != nil {
		return nil, err
	}
	if runtime != nil {
		cols := intNumberDefault(msg["cols"], 80)
		rows := intNumberDefault(msg["rows"], 24)
		if sizer, ok := runtime.(terminalRuntimeSizer); ok {
			err = sizer.EnsureSize(ctx, terminalID, cols, rows)
		} else {
			err = runtime.Ensure(ctx, terminalID)
		}
		if err != nil {
			if _, cleanupErr := sqlDB.ExecContext(ctx, `DELETE FROM terminals WHERE id = ?`, terminalID); cleanupErr != nil {
				return nil, fmt.Errorf("terminal runtime start failed: %w; cleanup terminal row: %v", err, cleanupErr)
			}
			return nil, err
		}
		if loaded, loadErr := loadTerminal(ctx, sqlDB, terminalID); loadErr == nil {
			terminal = loaded
		}
	}
	return terminal, nil
}

func (h *Hub) renameTerminal(ctx context.Context, terminalID, name string) error {
	h.mu.RLock()
	sqlDB := h.terminalDB
	h.mu.RUnlock()
	if sqlDB == nil {
		return errors.New("terminal database is not configured")
	}
	terminalID = strings.TrimSpace(terminalID)
	name = strings.TrimSpace(name)
	if terminalID == "" {
		return errors.New("terminalId is required")
	}
	if name == "" {
		return errors.New("name is required")
	}
	// Mirror the TypeScript terminalWsMessageSchema rename bound: name min 1 / max 100.
	if len(name) > 100 {
		return errors.New("name must be at most 100 characters")
	}
	res, err := sqlDB.ExecContext(ctx, `UPDATE terminals SET name = ? WHERE id = ?`, name, terminalID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return errors.New("terminal not found")
	}
	return nil
}

func loadTerminal(ctx context.Context, sqlDB *sql.DB, terminalID string) (map[string]any, error) {
	row := sqlDB.QueryRowContext(ctx, `SELECT id, chapter_id, narrator_id, name, cwd, dtach_socket, status, exit_code, graph_opened, graph_x, graph_y, graph_width, graph_height, created_at FROM terminals WHERE id = ?`, terminalID)
	var idValue, name, status, createdAt string
	var chapterID, narratorID, cwd, dtachSocket sql.NullString
	var exitCode sql.NullInt64
	var graphOpened int
	var graphX, graphY, graphWidth, graphHeight sql.NullFloat64
	if err := row.Scan(&idValue, &chapterID, &narratorID, &name, &cwd, &dtachSocket, &status, &exitCode, &graphOpened, &graphX, &graphY, &graphWidth, &graphHeight, &createdAt); err != nil {
		return nil, err
	}
	return map[string]any{
		"id": idValue, "chapterId": nullableNullString(chapterID), "narratorId": nullableNullString(narratorID),
		"name": name, "cwd": nullableNullString(cwd), "dtachSocket": nullableNullString(dtachSocket),
		"status": status, "exitCode": nullableNullInt(exitCode), "graphOpened": graphOpened != 0,
		"graphX": nullableNullFloat(graphX), "graphY": nullableNullFloat(graphY),
		"graphWidth": nullableNullFloat(graphWidth), "graphHeight": nullableNullFloat(graphHeight),
		"createdAt": createdAt,
	}, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableNullString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullableNullInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return int(value.Int64)
}

func nullableNullFloat(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
}

func (c *client) SendJSON(payload any) {
	c.queueSendJSON(payload)
}

func (c *client) Close(code int, reason string) {
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(websocketWriteTimeout))
	_ = c.conn.Close()
}

func (c *client) sendJSON(payload any) {
	data, err := json.Marshal(payload)
	if err == nil {
		c.send(data)
	}
}

func (c *client) queueSendJSON(payload any) {
	data, err := json.Marshal(payload)
	if err == nil {
		c.queueSend(data)
	}
}

func (c *client) queueSend(data []byte) {
	c.queueSendBatch([][]byte{data})
}

func (c *client) queueSendBatch(frames [][]byte) {
	if len(frames) == 0 {
		return
	}
	slots := c.ensureSendSlots()
	select {
	case slots <- struct{}{}:
		go func() {
			defer func() { <-slots }()
			for _, frame := range frames {
				c.send(frame)
			}
		}()
	default:
		_ = c.conn.Close()
	}
}

func (c *client) ensureSendSlots() chan struct{} {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.sendSlots == nil {
		c.sendSlots = make(chan struct{}, websocketSendBacklog)
	}
	return c.sendSlots
}

func (c *client) send(data []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(websocketWriteTimeout))
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		_ = c.conn.Close()
	}
}

func intNumber(v any) int {
	switch typed := v.(type) {
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		value, _ := typed.Int64()
		return int(value)
	default:
		return 0
	}
}

func intNumberDefault(v any, fallback int) int {
	if value := intNumber(v); value != 0 {
		return value
	}
	return fallback
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v any) bool {
	value, _ := v.(bool)
	return value
}

func boolValueDefault(v any, fallback bool) bool {
	value, ok := v.(bool)
	if !ok {
		return fallback
	}
	return value
}

func readStringArray(v any) []string {
	switch raw := v.(type) {
	case []string:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intPtr(value int) *int {
	return &value
}

func optionalInt(v any) (int, bool) {
	switch typed := v.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case json.Number:
		value, err := typed.Int64()
		return int(value), err == nil
	default:
		return 0, false
	}
}

func messageListLen(v any) int {
	switch typed := v.(type) {
	case []any:
		return len(typed)
	case []map[string]any:
		return len(typed)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return 0
		}
		var items []any
		if err := json.Unmarshal(data, &items); err != nil {
			return 0
		}
		return len(items)
	}
}

func sliceOrEmpty(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

func cloneMapSlice(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, cloneMap(item))
	}
	return out
}

func cloneMap(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for key, value := range item {
		out[key] = value
	}
	return out
}

func cloneSelected(src map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	mergeSelected(out, src, keys...)
	return out
}

func mergeSelected(dst, src map[string]any, keys ...string) {
	for _, key := range keys {
		if value, ok := src[key]; ok && value != nil {
			dst[key] = value
		}
	}
}
