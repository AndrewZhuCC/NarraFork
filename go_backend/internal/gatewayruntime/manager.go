package gatewayruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/id"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/ws"
)

const maxGatewayTextBytes = 64 * 1024

type InboundMessage struct {
	Platform string
	ChatID   string
	UserID   string
	Username string
	Text     string
}

type adapter interface {
	Platform() string
	Start(ctx context.Context) error
	Stop() error
	Send(ctx context.Context, chatID, text string) error
}

type platformConfig struct {
	Platform     string
	Enabled      bool
	Token        string
	APIBase      string
	AllowedUsers []string
}

type runtimeConfig struct {
	Enabled            bool
	RateLimitPerMinute int
	Platforms          []platformConfig
}

type Manager struct {
	DB        *sql.DB
	Settings  *config.Store
	Narrators *narrators.Service

	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	started     bool
	adapters    map[string]adapter
	configured  []string
	unsupported map[string]string
	rateLimiter *rateLimiter
	delivered   map[string]time.Time
}

func New(sqlDB *sql.DB, settings *config.Store, narratorSvc *narrators.Service, hub *ws.Hub) *Manager {
	m := &Manager{DB: sqlDB, Settings: settings, Narrators: narratorSvc, adapters: map[string]adapter{}, unsupported: map[string]string{}, delivered: map[string]time.Time{}}
	if hub != nil {
		hub.AddNarratorBroadcastHook(func(narratorID string, payload any, _ []byte) {
			m.HandleNarratorBroadcast(context.Background(), narratorID, payload)
		})
	}
	return m
}

func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	m.ctx = runCtx
	m.cancel = cancel
	m.adapters = map[string]adapter{}
	m.unsupported = map[string]string{}
	m.configured = nil
	m.mu.Unlock()

	cfg := m.loadConfig(ctx)
	if !cfg.Enabled {
		return nil
	}
	if cfg.RateLimitPerMinute > 0 {
		m.rateLimiter = newRateLimiter(cfg.RateLimitPerMinute)
	}
	for _, platform := range cfg.Platforms {
		if !platform.Enabled {
			continue
		}
		m.configured = append(m.configured, platform.Platform)
		switch platform.Platform {
		case "telegram":
			if strings.TrimSpace(platform.Token) == "" {
				m.unsupported[platform.Platform] = "telegram token is not configured"
				continue
			}
			adapter := newTelegramAdapter(platform, m.HandleInbound)
			if err := adapter.Start(runCtx); err != nil {
				m.unsupported[platform.Platform] = err.Error()
				continue
			}
			m.mu.Lock()
			m.adapters[platform.Platform] = adapter
			m.mu.Unlock()
		case "webhook":
			// Public webhook route is handled by systemroutes; it is not a persistent runtime.
		default:
			m.unsupported[platform.Platform] = "persistent runtime is not implemented in go_backend"
		}
	}
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	adapters := make([]adapter, 0, len(m.adapters))
	for _, adapter := range m.adapters {
		adapters = append(adapters, adapter)
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.adapters = map[string]adapter{}
	m.started = false
	m.mu.Unlock()
	for _, adapter := range adapters {
		_ = adapter.Stop()
	}
}

func (m *Manager) Reload(ctx context.Context, _ []string) (map[string]any, error) {
	m.Stop()
	if err := m.Start(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "reloaded": m.activePlatforms(), "status": m.Status(ctx)}, nil
}

func (m *Manager) Status(ctx context.Context) map[string]any {
	if m == nil {
		return map[string]any{"started": false, "supported": false, "persistentRuntimes": false}
	}
	sessions, _ := m.sessionRows(ctx)
	configuredUsers := m.configuredUserCount(ctx)
	active := m.activePlatforms()
	unsupported := map[string]string{}
	m.mu.Lock()
	started := m.started
	configured := append([]string{}, m.configured...)
	for k, v := range m.unsupported {
		unsupported[k] = v
	}
	m.mu.Unlock()
	return map[string]any{
		"started":              started,
		"supported":            true,
		"persistentRuntimes":   true,
		"mode":                 "persistent-plus-http-webhook",
		"platforms":            active,
		"configuredPlatforms":  configured,
		"supportedPlatforms":   []string{"telegram", "webhook"},
		"unsupportedPlatforms": unsupported,
		"sessions":             len(sessions),
		"configuredUsers":      configuredUsers,
		"webhook":              map[string]any{"supported": true, "fallback": false},
	}
}

func (m *Manager) HandleInbound(ctx context.Context, msg InboundMessage) error {
	if strings.TrimSpace(msg.Text) == "" {
		return nil
	}
	if len([]byte(msg.Text)) > maxGatewayTextBytes {
		return fmt.Errorf("gateway text exceeds %d bytes", maxGatewayTextBytes)
	}
	if m.rateLimiter != nil && !m.rateLimiter.Allow(msg.Platform+":"+msg.UserID) {
		return fmt.Errorf("gateway user is rate limited")
	}
	mapping, ok, err := m.mappingForMessage(ctx, msg)
	if err != nil || !ok {
		m.reply(ctx, msg.Platform, msg.ChatID, "No NarraFork narrator is bound to this gateway session yet. Open NarraFork and bind this chat before sending messages.")
		return err
	}
	narratorID := stringValue(mapping["narrator_id"])
	if narratorID == "" || m.Narrators == nil {
		m.reply(ctx, msg.Platform, msg.ChatID, "Gateway mapping is stale or narrator runtime is unavailable.")
		return nil
	}
	creator := firstNonEmptyString(stringValue(mapping["app_user_id"]), "gateway:"+msg.UserID)
	if _, err := m.Narrators.AddUserMessage(ctx, narratorID, creator, msg.Text); err != nil {
		return err
	}
	_, _ = m.DB.ExecContext(ctx, `UPDATE gateway_session_mappings SET last_message_at=?, updated_at=? WHERE id=?`, db.NowISO(), db.NowISO(), stringValue(mapping["id"]))
	return nil
}

func (m *Manager) HandleNarratorBroadcast(ctx context.Context, narratorID string, payload any) {
	msgID, role, text := gatewayMessageFromPayload(payload)
	if role != "assistant" || strings.TrimSpace(text) == "" || msgID == "" {
		return
	}
	key := narratorID + ":" + msgID
	m.mu.Lock()
	if _, ok := m.delivered[key]; ok {
		m.mu.Unlock()
		return
	}
	m.delivered[key] = time.Now()
	m.mu.Unlock()
	mappings, err := m.mappingsForNarrator(ctx, narratorID)
	if err != nil {
		return
	}
	for _, mapping := range mappings {
		m.reply(ctx, stringValue(mapping["platform"]), stringValue(mapping["chat_id"]), text)
	}
}

func (m *Manager) reply(ctx context.Context, platform, chatID, text string) {
	m.mu.Lock()
	adapter := m.adapters[platform]
	m.mu.Unlock()
	if adapter == nil || chatID == "" || strings.TrimSpace(text) == "" {
		return
	}
	_ = adapter.Send(ctx, chatID, text)
}

func (m *Manager) activePlatforms() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.adapters))
	for platform := range m.adapters {
		out = append(out, platform)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) loadConfig(ctx context.Context) runtimeConfig {
	cfg := runtimeConfig{RateLimitPerMinute: 20}
	dbCfg := m.loadDBConfig(ctx)
	if enabled, ok := dbCfg["enabled"].(bool); ok {
		cfg.Enabled = enabled
	}
	if rate := intFromAny(dbCfg["rateLimitPerMinute"]); rate > 0 {
		cfg.RateLimitPerMinute = rate
	}
	if platforms, ok := dbCfg["platforms"].([]any); ok {
		for _, raw := range platforms {
			if item, ok := raw.(map[string]any); ok {
				cfg.Platforms = append(cfg.Platforms, platformConfigFromMap(item))
			}
		}
	}
	if token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); token != "" {
		cfg.Platforms = upsertPlatform(cfg.Platforms, platformConfig{Platform: "telegram", Enabled: true, Token: token, APIBase: os.Getenv("TELEGRAM_API_BASE"), AllowedUsers: splitList(os.Getenv("TELEGRAM_ALLOWED_USERS"))})
	}
	if secret := strings.TrimSpace(os.Getenv("GATEWAY_WEBHOOK_SECRET")); secret != "" {
		_ = secret
		cfg.Platforms = upsertPlatform(cfg.Platforms, platformConfig{Platform: "webhook", Enabled: true})
	}
	if enabled := strings.TrimSpace(os.Getenv("GATEWAY_ENABLED")); enabled != "" {
		cfg.Enabled = enabled == "1" || strings.EqualFold(enabled, "true")
	} else if len(cfg.Platforms) > 0 && !cfg.Enabled {
		for _, p := range cfg.Platforms {
			cfg.Enabled = cfg.Enabled || p.Enabled
		}
	}
	return cfg
}

func (m *Manager) loadDBConfig(ctx context.Context) map[string]any {
	if m == nil || m.DB == nil {
		return map[string]any{}
	}
	rows, err := m.DB.QueryContext(ctx, `SELECT up.gateway_config FROM user_preferences up JOIN users u ON u.id=up.user_id WHERE up.gateway_config IS NOT NULL AND up.gateway_config!='' ORDER BY CASE WHEN u.role='admin' THEN 0 ELSE 1 END, u.created_at LIMIT 1`)
	if err != nil {
		return map[string]any{}
	}
	defer rows.Close()
	if !rows.Next() {
		return map[string]any{}
	}
	var raw sql.NullString
	if err := rows.Scan(&raw); err != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw.String), &out)
	return out
}

func (m *Manager) mappingForMessage(ctx context.Context, msg InboundMessage) (map[string]any, bool, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT id,platform,chat_id,user_id,username,narrator_id,app_user_id,project_id,chapter_id,last_message_at,created_at,updated_at FROM gateway_session_mappings WHERE platform=? AND chat_id=? AND user_id=? ORDER BY updated_at DESC LIMIT 1`, msg.Platform, msg.ChatID, msg.UserID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil || len(items) == 0 {
		return nil, false, err
	}
	return items[0], true, nil
}

func (m *Manager) mappingsForNarrator(ctx context.Context, narratorID string) ([]map[string]any, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT id,platform,chat_id,user_id,username,narrator_id,app_user_id FROM gateway_session_mappings WHERE narrator_id=? ORDER BY updated_at DESC LIMIT 20`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rowsToMaps(rows)
}

func (m *Manager) sessionRows(ctx context.Context) ([]map[string]any, error) {
	if m == nil || m.DB == nil {
		return []map[string]any{}, nil
	}
	rows, err := m.DB.QueryContext(ctx, `SELECT id FROM gateway_session_mappings LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rowsToMaps(rows)
}

func (m *Manager) configuredUserCount(ctx context.Context) int {
	if m == nil || m.DB == nil {
		return 0
	}
	var count int
	_ = m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_preferences WHERE gateway_config IS NOT NULL AND gateway_config != '' AND gateway_config != '{}'`).Scan(&count)
	return count
}

func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, col := range cols {
			if b, ok := values[i].([]byte); ok {
				item[col] = string(b)
			} else {
				item[col] = values[i]
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func platformConfigFromMap(item map[string]any) platformConfig {
	return platformConfig{Platform: stringValue(item["platform"]), Enabled: boolValue(item["enabled"]), Token: firstNonEmptyString(stringValue(item["token"]), stringValue(item["botToken"])), APIBase: stringValue(item["apiBase"]), AllowedUsers: stringSlice(item["allowedUsers"])}
}

func upsertPlatform(items []platformConfig, item platformConfig) []platformConfig {
	for i := range items {
		if items[i].Platform == item.Platform {
			items[i] = item
			return items
		}
	}
	return append(items, item)
}

func gatewayMessageFromPayload(payload any) (string, string, string) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", "", ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil || stringValue(decoded["type"]) != "message" {
		return "", "", ""
	}
	msg, _ := decoded["message"].(map[string]any)
	return stringValue(msg["id"]), stringValue(msg["role"]), stringValue(msg["contentText"])
}

type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	events map[string][]time.Time
}

func newRateLimiter(limit int) *rateLimiter {
	return &rateLimiter{limit: limit, events: map[string][]time.Time{}}
}
func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	items := r.events[key]
	keep := items[:0]
	for _, item := range items {
		if item.After(cutoff) {
			keep = append(keep, item)
		}
	}
	if len(keep) >= r.limit {
		r.events[key] = keep
		return false
	}
	r.events[key] = append(keep, now)
	return true
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func boolValue(v any) bool { b, _ := v.(bool); return b }
func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, item := range items {
		if s := stringValue(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func insertMapping(ctx context.Context, sqlDB *sql.DB, msg InboundMessage, narratorID string) error {
	_, err := sqlDB.ExecContext(ctx, `INSERT INTO gateway_session_mappings (id,platform,chat_id,user_id,username,narrator_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, id.New(), msg.Platform, msg.ChatID, msg.UserID, msg.Username, narratorID, db.NowISO(), db.NowISO())
	return err
}
