package systemroutes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/terminals"
)

type TerminalRuntime interface {
	ShellPID(terminalID string) (int, bool)
	Kill(ctx context.Context, terminalID string) error
}

type activeTerminalRuntime interface {
	ActiveIDs() []string
}

type orphanTerminalRuntime interface {
	OrphanIDs(ctx context.Context) []string
	KillOrphan(ctx context.Context, terminalID string) error
}

type terminalReattachRuntime interface {
	Ensure(ctx context.Context, terminalID string) error
}

type Handler struct {
	DB              *sql.DB
	Settings        *config.Store
	TerminalRuntime TerminalRuntime
	Narrators       *narrators.Service
}

type nugOAuthState struct {
	ProviderID  string
	CallbackURL string
	ExpiresAt   time.Time
}

type codexDeviceFlow struct {
	DeviceAuthID    string
	UserCode        string
	VerificationURL string
	Interval        time.Duration
	Cancel          context.CancelFunc
	StartedAt       time.Time
	Completed       bool
	Err             string
}

type codexBrowserFlow struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	Server       *http.Server
	Completed    bool
	Err          string
	ExpiresAt    time.Time
}

type clineBrowserFlow struct {
	Server    *http.Server
	Pending   bool
	Err       string
	ExpiresAt time.Time
}

var (
	nugOAuthMu     sync.Mutex
	nugOAuthStates = map[string]nugOAuthState{}

	codexAuthMu  sync.Mutex
	codexDevice  *codexDeviceFlow
	codexBrowser *codexBrowserFlow
	clineAuthMu  sync.Mutex
	clineBrowser *clineBrowserFlow
)

func NewHandler(sqlDB *sql.DB, settings *config.Store, runtime ...TerminalRuntime) *Handler {
	h := &Handler{DB: sqlDB, Settings: settings}
	if len(runtime) > 0 {
		h.TerminalRuntime = runtime[0]
	}
	return h
}

func (h *Handler) RegisterAdmin(r chi.Router) {
	r.Get("/users", httpx.Handler(h.adminOnly(h.listUsers)))
	r.Patch("/users/{id}", httpx.Handler(h.adminOnly(h.updateUser)))
	r.Delete("/users/{id}", httpx.Handler(h.adminOnly(h.deleteUser)))
	r.Patch("/settings", httpx.Handler(h.adminOnly(h.updateAdminSettings)))
	r.Get("/terminals", httpx.Handler(h.adminOnly(h.adminTerminals)))
	r.Delete("/terminals/{id}", httpx.Handler(h.adminOnly(h.killTerminal)))
	r.Post("/terminals/batch-kill", httpx.Handler(h.adminOnly(h.batchKillTerminals)))
	r.Post("/terminals/kill-orphan", httpx.Handler(h.adminOnly(h.killOrphanTerminal)))
	r.Post("/terminals/{id}/reattach", httpx.Handler(h.adminOnly(h.reattachTerminal)))
	r.Post("/terminals/reattach-orphan", httpx.Handler(h.adminOnly(h.reattachOrphanTerminal)))
	r.Get("/diagnostics", httpx.Handler(h.adminOnly(h.diagnostics)))
}

var runtimeCache map[string]any

func (h *Handler) RegisterProjectDB(r chi.Router) {
	r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "project-routes", "message": "Use /api/projects/:id/backup/status and /api/projects/:id/backup/sync"})
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

func (h *Handler) RegisterBenchmarks(r chi.Router) {
	r.Post("/suites", httpx.Handler(h.adminOnly(h.createBenchmarkSuite)))
	r.Get("/suites", httpx.Handler(h.adminOnly(h.listBenchmarkSuites)))
	r.Get("/suites/{id}", httpx.Handler(h.adminOnly(h.getBenchmarkSuite)))
	r.Post("/runs", httpx.Handler(h.adminOnly(h.createBenchmarkRun)))
	r.Get("/runs", httpx.Handler(h.adminOnly(h.listBenchmarkRuns)))
	r.Get("/runs/{id}", httpx.Handler(h.adminOnly(h.getBenchmarkRun)))
	r.Post("/runs/{id}/start", httpx.Handler(h.adminOnly(h.startBenchmarkRun)))
	r.Post("/runs/{id}/cancel", httpx.Handler(h.adminOnly(h.cancelBenchmarkRun)))
	r.Post("/runs/{id}/retry", httpx.Handler(h.adminOnly(h.retryBenchmarkRun)))
	r.Get("/runs/{id}/results", httpx.Handler(h.adminOnly(h.getBenchmarkRunResults)))
	r.Get("/runs/{id}/summary", httpx.Handler(h.adminOnly(h.getBenchmarkRunSummary)))
	r.Get("/compare", httpx.Handler(h.adminOnly(h.compareBenchmarkRuns)))
}

func (h *Handler) createBenchmarkSuite(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if err := validateBenchmarkSuitePayload(body["tasks"]); err != nil {
		return err
	}
	suiteID := id.New()
	now := db.NowISO()
	tasks, _ := jsonMarshalString(body["tasks"])
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO benchmark_suites (id,name,version,description,tasks_json,created_at) VALUES (?,?,?,?,?,?)`, suiteID, stringMap(body, "name", "Suite"), nullableMapString(body, "version"), nullableMapString(body, "description"), tasks, now)
	if err != nil {
		return err
	}
	return h.getBenchmarkSuiteByID(w, r, suiteID, http.StatusCreated)
}
func (h *Handler) listBenchmarkSuites(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,name,version,description,tasks_json,created_at FROM benchmark_suites ORDER BY created_at`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := benchmarkSuiteRows(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}
func (h *Handler) getBenchmarkSuite(w http.ResponseWriter, r *http.Request) error {
	return h.getBenchmarkSuiteByID(w, r, chi.URLParam(r, "id"), http.StatusOK)
}
func (h *Handler) getBenchmarkSuiteByID(w http.ResponseWriter, r *http.Request, suiteID string, status int) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,name,version,description,tasks_json,created_at FROM benchmark_suites WHERE id=?`, suiteID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := benchmarkSuiteRows(rows)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return httpx.NotFound("Benchmark suite")
	}
	httpx.WriteJSON(w, status, items[0])
	return nil
}
func (h *Handler) createBenchmarkRun(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	suiteID := stringMap(body, "suiteId", "")
	var rawTasks sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT tasks_json FROM benchmark_suites WHERE id=?`, suiteID).Scan(&rawTasks); err != nil {
		if err == sql.ErrNoRows {
			return httpx.NotFound("Benchmark suite")
		}
		return err
	}
	tasks := benchmarkTasksFromJSON(rawTasks)
	runID := id.New()
	now := db.NowISO()
	config, _ := jsonMarshalDefault(body["config"], "{}")
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO benchmark_runs (id,suite_id,name,model,system_prompt,permission_mode,status,config,total_tasks,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, runID, suiteID, stringMap(body, "name", "Run"), stringMap(body, "model", ""), nullableMapString(body, "systemPrompt"), stringMap(body, "permissionMode", "bypassPermissions"), "pending", config, len(tasks), now)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		_, err = h.DB.ExecContext(r.Context(), `INSERT INTO benchmark_task_results (id,run_id,task_id,task_name,status,created_at) VALUES (?,?,?,?,?,?)`, id.New(), runID, stringMap(task, "id", id.New()), stringMap(task, "name", "Task"), "pending", now)
		if err != nil {
			return err
		}
	}
	return h.getBenchmarkRunByID(w, r, runID, http.StatusCreated)
}
func (h *Handler) listBenchmarkRuns(w http.ResponseWriter, r *http.Request) error {
	q := `SELECT id,suite_id,name,model,system_prompt,permission_mode,status,config,total_tasks,completed_tasks,passed_tasks,failed_tasks,total_cost_usd,total_tokens_in,total_tokens_out,total_duration_ms,created_at,started_at,completed_at FROM benchmark_runs`
	args := []any{}
	if suite := r.URL.Query().Get("suiteId"); suite != "" {
		q += ` WHERE suite_id=?`
		args = append(args, suite)
	}
	q += ` ORDER BY created_at`
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := benchmarkRunRows(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}
func (h *Handler) getBenchmarkRun(w http.ResponseWriter, r *http.Request) error {
	return h.getBenchmarkRunByID(w, r, chi.URLParam(r, "id"), http.StatusOK)
}
func (h *Handler) getBenchmarkRunByID(w http.ResponseWriter, r *http.Request, runID string, status int) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,suite_id,name,model,system_prompt,permission_mode,status,config,total_tasks,completed_tasks,passed_tasks,failed_tasks,total_cost_usd,total_tokens_in,total_tokens_out,total_duration_ms,created_at,started_at,completed_at FROM benchmark_runs WHERE id=?`, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := benchmarkRunRows(rows)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return httpx.NotFound("Benchmark run")
	}
	httpx.WriteJSON(w, status, items[0])
	return nil
}
func (h *Handler) startBenchmarkRun(w http.ResponseWriter, r *http.Request) error {
	runID := chi.URLParam(r, "id")
	claimed, err := h.claimBenchmarkRun(r.Context(), runID)
	if err != nil {
		return err
	}
	h.startClaimedBenchmarkRun(claimed)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "runId": runID, "status": "running", "runnerAvailable": true, "message": "Benchmark run started"})
	return nil
}

func (h *Handler) benchmarkRunExistsForCancel(ctx context.Context, tx *sql.Tx, runID string) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_runs WHERE id=?`, runID).Scan(&exists); err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (h *Handler) cancelBenchmarkRun(w http.ResponseWriter, r *http.Request) error {
	runID := chi.URLParam(r, "id")
	h.cancelActiveBenchmarkRun(runID)
	now := db.NowISO()
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	res, err := tx.ExecContext(r.Context(), `UPDATE benchmark_runs SET status='cancelled', completed_at=? WHERE id=? AND status != 'completed'`, now, runID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		exists, err := h.benchmarkRunExistsForCancel(r.Context(), tx, runID)
		if err != nil {
			return err
		}
		if !exists {
			return httpx.NotFound("Benchmark run")
		}
		return httpx.BadRequest("completed benchmark runs cannot be cancelled")
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE benchmark_task_results SET status='cancelled', completed_at=COALESCE(completed_at, ?) WHERE run_id=? AND status IN ('pending','running')`, now, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE benchmark_runs SET completed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status!='pending'), passed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status='passed'), failed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status IN ('failed','error','timeout')) WHERE id=?`, runID, runID, runID, runID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "runId": runID, "status": "cancelled", "message": "Run cancellation recorded"})
	return nil
}

func (h *Handler) getBenchmarkRunResults(w http.ResponseWriter, r *http.Request) error {
	limit := intQuery(r, "limit", 1000)
	if limit > 1000 {
		limit = 1000
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,run_id,task_id,task_name,narrator_id,status,score,max_score,output,eval_output,error_message,tokens_in,tokens_out,cost_usd,duration_ms,tool_call_count,message_count,metadata,started_at,completed_at,created_at FROM benchmark_task_results WHERE run_id=? ORDER BY created_at, task_id LIMIT ?`, chi.URLParam(r, "id"), limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idv, runID, taskID, taskName, status, created string
		var narratorID, output, evalOutput, errMsg, metadata, started, done sql.NullString
		var score, maxScore, cost sql.NullFloat64
		var tokensIn, tokensOut, duration, toolCalls, messages sql.NullInt64
		if err := rows.Scan(&idv, &runID, &taskID, &taskName, &narratorID, &status, &score, &maxScore, &output, &evalOutput, &errMsg, &tokensIn, &tokensOut, &cost, &duration, &toolCalls, &messages, &metadata, &started, &done, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": idv, "runId": runID, "taskId": taskID, "taskName": taskName, "narratorId": nullableString(narratorID), "status": status, "score": nullableFloat(score), "maxScore": nullableFloat(maxScore), "output": nullableString(output), "evalOutput": nullableString(evalOutput), "errorMessage": nullableString(errMsg), "tokensIn": nullableInt(tokensIn), "tokensOut": nullableInt(tokensOut), "costUsd": nullableFloat(cost), "durationMs": nullableInt(duration), "toolCallCount": nullableInt(toolCalls), "messageCount": nullableInt(messages), "metadata": decodeJSONWithDefault(metadata, nil), "startedAt": nullableString(started), "completedAt": nullableString(done), "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) getBenchmarkRunSummary(w http.ResponseWriter, r *http.Request) error {
	summary, err := h.benchmarkRunSummary(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, summary)
	return nil
}

func (h *Handler) compareBenchmarkRuns(w http.ResponseWriter, r *http.Request) error {
	raw := strings.TrimSpace(r.URL.Query().Get("runIds"))
	if raw == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "runIds query parameter required"})
		return nil
	}
	runIDs := []string{}
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			runIDs = append(runIDs, value)
		}
	}
	if len(runIDs) < 2 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "At least 2 run IDs required"})
		return nil
	}
	summaries := []map[string]any{}
	for _, runID := range runIDs {
		summary, err := h.benchmarkRunSummary(r.Context(), runID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return nil
		}
		summaries = append(summaries, summary)
	}
	httpx.WriteJSON(w, http.StatusOK, summaries)
	return nil
}

func (h *Handler) benchmarkRunSummary(ctx context.Context, runID string) (map[string]any, error) {
	row := h.DB.QueryRowContext(ctx, `SELECT br.id,COALESCE(bs.name,'unknown'),br.model,br.status,COUNT(btr.id),COALESCE(SUM(CASE WHEN btr.status='passed' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN btr.status='failed' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN btr.status='error' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN btr.status='timeout' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN btr.status='pending' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN btr.score IS NOT NULL THEN btr.score ELSE 0 END),0),COALESCE(SUM(CASE WHEN btr.score IS NOT NULL THEN 1 ELSE 0 END),0),COALESCE(SUM(COALESCE(btr.cost_usd,0)),0),COALESCE(SUM(COALESCE(btr.tokens_in,0)),0),COALESCE(SUM(COALESCE(btr.tokens_out,0)),0),COALESCE(SUM(COALESCE(btr.duration_ms,0)),0) FROM benchmark_runs br LEFT JOIN benchmark_suites bs ON bs.id=br.suite_id LEFT JOIN benchmark_task_results btr ON btr.run_id=br.id WHERE br.id=? GROUP BY br.id,bs.name,br.model,br.status`, runID)
	var idv, suiteName, model, status string
	var total, passed, failed, errors, timeouts, pending, scoreCount, tokensIn, tokensOut, totalDuration int64
	var scoreSum, totalCost float64
	if err := row.Scan(&idv, &suiteName, &model, &status, &total, &passed, &failed, &errors, &timeouts, &pending, &scoreSum, &scoreCount, &totalCost, &tokensIn, &tokensOut, &totalDuration); err != nil {
		if err == sql.ErrNoRows {
			return nil, httpx.NotFound("Benchmark run")
		}
		return nil, err
	}
	avgScore := scoreSum / float64(maxInt64(scoreCount, 1))
	avgDuration := float64(totalDuration) / float64(maxInt64(total-pending, 1))
	passRate := 0.0
	if total > 0 {
		passRate = math.Round((float64(passed)/float64(total))*10000) / 100
	}
	return map[string]any{"runId": idv, "suiteName": suiteName, "model": model, "status": status, "total": total, "passed": passed, "failed": failed, "errors": errors, "timeouts": timeouts, "pending": pending, "passRate": passRate, "avgScore": math.Round(avgScore*10000) / 10000, "totalCostUsd": math.Round(totalCost*1000000) / 1000000, "totalTokensIn": tokensIn, "totalTokensOut": tokensOut, "totalDurationMs": totalDuration, "avgDurationMs": math.Round(avgDuration)}, nil
}

func benchmarkSuiteRows(rows *sql.Rows) ([]map[string]any, error) {
	items := []map[string]any{}
	for rows.Next() {
		var idv, name, created string
		var version, desc, tasks sql.NullString
		if err := rows.Scan(&idv, &name, &version, &desc, &tasks, &created); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idv, "name": name, "version": nullableString(version), "description": nullableString(desc), "tasks": decodeJSON(tasks), "createdAt": created})
	}
	return items, rows.Err()
}
func stringMap(m map[string]any, key, fallback string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return fallback
}
func stringList(v any) []string {
	out := []string{}
	switch values := v.(type) {
	case []any:
		for _, item := range values {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range values {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func nullableMapString(m map[string]any, key string) any {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return nil
}
func jsonMarshalString(v any) (string, error) {
	return jsonMarshalDefault(v, "[]")
}
func jsonMarshalDefault(v any, fallback string) (string, error) {
	if v == nil {
		return fallback, nil
	}
	data, err := json.Marshal(v)
	return string(data), err
}
func decodeJSON(v sql.NullString) any {
	return decodeJSONWithDefault(v, []any{})
}
func decodeJSONWithDefault(v sql.NullString, fallback any) any {
	if !v.Valid || v.String == "" {
		return fallback
	}
	var out any
	if err := json.Unmarshal([]byte(v.String), &out); err != nil {
		return v.String
	}
	return out
}
func benchmarkTasksFromJSON(raw sql.NullString) []map[string]any {
	decoded, ok := decodeJSON(raw).([]any)
	if !ok {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for i, item := range decoded {
		if task, ok := item.(map[string]any); ok {
			if stringMap(task, "id", "") == "" {
				task["id"] = "task-" + strconv.Itoa(i+1)
			}
			if stringMap(task, "name", "") == "" {
				task["name"] = stringMap(task, "id", "Task")
			}
			out = append(out, task)
		}
	}
	return out
}
func maxInt64(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}
func benchmarkRunRows(rows *sql.Rows) ([]map[string]any, error) {
	items := []map[string]any{}
	for rows.Next() {
		var idv, suite, name, model, status, created string
		var systemPrompt, permissionMode, configRaw, started, done sql.NullString
		var total, completed, passed, failed, tokensIn, tokensOut, duration int
		var cost float64
		if err := rows.Scan(&idv, &suite, &name, &model, &systemPrompt, &permissionMode, &status, &configRaw, &total, &completed, &passed, &failed, &cost, &tokensIn, &tokensOut, &duration, &created, &started, &done); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idv, "suiteId": suite, "name": name, "model": model, "systemPrompt": nullableString(systemPrompt), "permissionMode": stringDefault(permissionMode, "bypassPermissions"), "status": status, "config": decodeJSONWithDefault(configRaw, map[string]any{}), "totalTasks": total, "completedTasks": completed, "passedTasks": passed, "failedTasks": failed, "totalCostUsd": cost, "totalTokensIn": tokensIn, "totalTokensOut": tokensOut, "totalDurationMs": duration, "createdAt": created, "startedAt": nullableString(started), "completedAt": nullableString(done)})
	}
	return items, rows.Err()
}

func (h *Handler) RegisterVNet(r chi.Router) {
	r.Get("/status", httpx.Handler(h.vnetStatus))
}

func (h *Handler) RegisterGatewayPublic(r chi.Router) {
	r.Post("/webhook", httpx.Handler(h.gatewayWebhookFallback))
}

func (h *Handler) RegisterGateway(r chi.Router) {
	r.Get("/status", httpx.Handler(h.gatewayStatus))
	r.Get("/sessions", httpx.Handler(h.gatewaySessions))
	r.Delete("/sessions/{id}", httpx.Handler(h.gatewayDeleteSession))
	r.Post("/reload", httpx.Handler(h.gatewayReload))
	r.Post("/weixin/qr-start", httpx.Handler(h.gatewayWeixinQRStart))
	r.Get("/weixin/qr-poll", httpx.Handler(h.gatewayWeixinQRPoll))
}

type gatewayWebhookConfig struct {
	Enabled bool
	Secret  string
}

type gatewayInboundMessage struct {
	Platform string
	ChatID   string
	UserID   string
	Username string
	Text     string
}

func (h *Handler) vnetStatus(w http.ResponseWriter, r *http.Request) error {
	addrs := []string{}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, _ := iface.Addrs()
		for _, addr := range ifaceAddrs {
			addrs = append(addrs, addr.String())
		}
	}
	sort.Strings(addrs)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":        false,
		"supported": false,
		"fallback":  true,
		"mode":      "disabled",
		"code":      "VNET_DISABLED",
		"feature":   "vnet",
		"ws":        false,
		"reason":    "VNet relay/UDP runtime is not bundled in go_backend",
		"relay":     map[string]any{"clients": 0, "rooms": 0, "messages": 0, "running": false},
		"udp":       map[string]any{"enabled": false, "sessions": 0},
		"network":   map[string]any{"hostname": hostname(), "addresses": addrs},
	})
	return nil
}

func (h *Handler) gatewayWebhookFallback(w http.ResponseWriter, r *http.Request) error {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024+1))
	if err != nil {
		return err
	}
	if len(rawBody) > 1024*1024 {
		httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, gatewayFallbackPayload("GATEWAY_WEBHOOK_BODY_TOO_LARGE", "Gateway webhook payload exceeds 1 MiB"))
		return nil
	}

	cfg := h.gatewayWebhookConfig(r.Context())
	if !cfg.Enabled || cfg.Secret == "" {
		status := http.StatusNotFound
		payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_NOT_CONFIGURED", "Webhook adapter is not configured in go_backend")
		payload["configured"] = false
		httpx.WriteJSON(w, status, payload)
		return nil
	}

	signature := firstNonEmpty(r.Header.Get("X-Hub-Signature-256"), r.Header.Get("X-Webhook-Signature"))
	if !verifyGatewayWebhookSignature(rawBody, signature, cfg.Secret) {
		httpx.WriteJSON(w, http.StatusUnauthorized, gatewayFallbackPayload("GATEWAY_WEBHOOK_INVALID_SIGNATURE", "Invalid webhook signature"))
		return nil
	}

	msg, errPayload := parseGatewayWebhookPayload(rawBody)
	if errPayload != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, errPayload)
		return nil
	}

	mapping, ok, err := h.gatewayMappingForMessage(r.Context(), msg)
	if err != nil {
		return err
	}
	if !ok {
		payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_NO_MAPPING", "No gateway session mapping exists for this webhook sender; Go backend will not create a new narrator implicitly")
		payload["chatId"] = msg.ChatID
		payload["userId"] = msg.UserID
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return nil
	}

	narratorID := stringMap(mapping, "narrator_id", "")
	if narratorID == "" || !h.narratorExists(r.Context(), narratorID) {
		payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_STALE_MAPPING", "Gateway session mapping does not point to an existing narrator")
		payload["chatId"] = msg.ChatID
		payload["narratorId"] = narratorID
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return nil
	}
	if h.Narrators == nil {
		payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_RUNTIME_UNAVAILABLE", "Narrator runtime is not attached to the public gateway route")
		payload["chatId"] = msg.ChatID
		payload["narratorId"] = narratorID
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return nil
	}

	creator := firstNonEmpty(stringMap(mapping, "app_user_id", ""), "gateway:"+msg.UserID)
	if _, err := h.Narrators.AddUserMessage(r.Context(), narratorID, creator, msg.Text); err != nil {
		return err
	}
	if err := h.updateGatewayLastMessage(r.Context(), stringMap(mapping, "id", "")); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "supported": true, "fallback": false, "platform": msg.Platform, "chatId": msg.ChatID, "narratorId": narratorID})
	return nil
}

func (h *Handler) updateGatewayLastMessage(ctx context.Context, mappingID string) error {
	_, err := h.DB.ExecContext(ctx, `UPDATE gateway_session_mappings SET last_message_at=?, updated_at=? WHERE id=?`, db.NowISO(), db.NowISO(), mappingID)
	return err
}

func (h *Handler) gatewayStatus(w http.ResponseWriter, r *http.Request) error {
	sessions, sessionErr := h.gatewaySessionRows(r.Context())
	configuredUsers := 0
	degraded := false
	fallbacks := []any{}
	errorsList := []any{}
	if sessionErr != nil {
		degraded = true
		fallbacks = append(fallbacks, "gateway_sessions_query_failed")
		errorsList = append(errorsList, sessionErr.Error())
		sessions = []map[string]any{}
	}
	if h.DB != nil {
		if ok, err := h.systemTableExists(r.Context(), "user_preferences"); err != nil {
			degraded = true
			fallbacks = append(fallbacks, "gateway_user_preferences_table_check_failed")
			errorsList = append(errorsList, err.Error())
		} else if ok {
			if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM user_preferences WHERE gateway_config IS NOT NULL AND gateway_config != '' AND gateway_config != '{}'`).Scan(&configuredUsers); err != nil {
				degraded = true
				fallbacks = append(fallbacks, "gateway_configured_users_count_failed")
				errorsList = append(errorsList, err.Error())
			}
		}
	}
	cfg := h.gatewayWebhookConfig(r.Context())
	platforms := []string{}
	if cfg.Enabled {
		platforms = append(platforms, "webhook")
	}
	status := map[string]any{"started": false, "platforms": platforms, "sessions": len(sessions), "configuredUsers": configuredUsers, "mode": "fallback", "supported": false, "reason": "Persistent gateway runtimes are not bundled in go_backend", "webhook": map[string]any{"configured": cfg.Enabled && cfg.Secret != "", "supported": h.Narrators != nil, "fallback": h.Narrators == nil}}
	if degraded {
		status["degraded"] = true
		status["fallback"] = true
		status["fallbacks"] = fallbacks
		status["errors"] = errorsList
	}
	httpx.WriteJSON(w, http.StatusOK, status)
	return nil
}

func (h *Handler) gatewaySessions(w http.ResponseWriter, r *http.Request) error {
	sessions, err := h.gatewaySessionRows(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, sessions)
	return nil
}

func (h *Handler) gatewayDeleteSession(w http.ResponseWriter, r *http.Request) error {
	if ok, _ := h.systemTableExists(r.Context(), "gateway_session_mappings"); !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": false, "deleted": 0, "reason": "gateway session storage is not available"})
		return nil
	}
	res, err := h.DB.ExecContext(r.Context(), `DELETE FROM gateway_session_mappings WHERE id=?`, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	deleted, _ := res.RowsAffected()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": deleted > 0, "deleted": deleted})
	return nil
}

func (h *Handler) gatewayReload(w http.ResponseWriter, r *http.Request) error {
	cfg := h.gatewayWebhookConfig(r.Context())
	platforms := []string{}
	if cfg.Enabled {
		platforms = append(platforms, "webhook")
	}
	status := map[string]any{"started": false, "platforms": platforms, "mode": "fallback", "supported": false, "reason": "Persistent gateway runtimes are not bundled in go_backend", "webhook": map[string]any{"configured": cfg.Enabled && cfg.Secret != "", "supported": h.Narrators != nil, "fallback": h.Narrators == nil}}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "reloaded": []string{}, "status": status})
	return nil
}

func (h *Handler) gatewayWeixinQRStart(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "status": "unavailable", "supported": false, "fallback": true, "code": "WEIXIN_QR_UNSUPPORTED", "qrcodeUrl": "", "qrcodeToken": "", "message": "Weixin QR gateway runtime is not bundled in go_backend"})
	return nil
}

func (h *Handler) gatewayWeixinQRPoll(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "status": "unavailable", "supported": false, "fallback": true, "code": "WEIXIN_QR_UNSUPPORTED", "message": "No active QR session: Weixin QR gateway runtime is not bundled in go_backend"})
	return nil
}

func gatewayFallbackPayload(code, message string) map[string]any {
	return map[string]any{
		"ok":        false,
		"supported": false,
		"fallback":  true,
		"code":      code,
		"error":     message,
		"message":   message,
		"platform":  "webhook",
	}
}

func (h *Handler) gatewayWebhookConfig(ctx context.Context) gatewayWebhookConfig {
	cfg := gatewayWebhookConfig{}
	if secret := strings.TrimSpace(os.Getenv("GATEWAY_WEBHOOK_SECRET")); secret != "" {
		cfg.Enabled = true
		cfg.Secret = secret
	}
	if enabled := strings.TrimSpace(os.Getenv("GATEWAY_ENABLED")); enabled != "" {
		cfg.Enabled = enabled == "true" || enabled == "1"
	}
	if h.DB != nil {
		if dbCfg, ok := h.gatewayDBConfig(ctx); ok {
			if enabled, ok := dbCfg["enabled"].(bool); ok && !cfg.Enabled {
				cfg.Enabled = enabled
			}
			if platforms, ok := dbCfg["platforms"].([]any); ok {
				for _, item := range platforms {
					platform, ok := item.(map[string]any)
					if !ok || stringMap(platform, "platform", "") != "webhook" {
						continue
					}
					if enabled, ok := platform["enabled"].(bool); ok && !cfg.Enabled {
						cfg.Enabled = enabled
					}
					if cfg.Secret == "" {
						cfg.Secret = strings.TrimSpace(stringMap(platform, "secret", ""))
					}
				}
			}
		}
	}
	if cfg.Secret != "" {
		cfg.Enabled = true
	}
	return cfg
}

func (h *Handler) gatewayDBConfig(ctx context.Context) (map[string]any, bool) {
	if h.DB == nil {
		return nil, false
	}
	if ok, _ := h.systemTableExists(ctx, "users"); !ok {
		return nil, false
	}
	if ok, _ := h.systemTableExists(ctx, "user_preferences"); !ok {
		return nil, false
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT u.id FROM users u ORDER BY CASE WHEN u.role='admin' THEN 0 ELSE 1 END, u.created_at LIMIT 5`)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	userIDs := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err == nil && userID != "" {
			userIDs = append(userIDs, userID)
		}
	}
	for _, userID := range userIDs {
		var raw sql.NullString
		if err := h.DB.QueryRowContext(ctx, `SELECT gateway_config FROM user_preferences WHERE user_id=?`, userID).Scan(&raw); err != nil || !raw.Valid || strings.TrimSpace(raw.String) == "" {
			continue
		}
		cfg := map[string]any{}
		if err := json.Unmarshal([]byte(raw.String), &cfg); err == nil {
			return cfg, true
		}
	}
	return nil, false
}

func verifyGatewayWebhookSignature(body []byte, signature, secret string) bool {
	if strings.TrimSpace(signature) == "" || secret == "" {
		return false
	}
	actual := strings.TrimSpace(signature)
	if strings.HasPrefix(actual, "sha256=") {
		actual = strings.TrimPrefix(actual, "sha256=")
	}
	actualBytes, err := hex.DecodeString(actual)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(expected, actualBytes)
}

func parseGatewayWebhookPayload(body []byte) (gatewayInboundMessage, map[string]any) {
	raw := map[string]any{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return gatewayInboundMessage{}, gatewayFallbackPayload("GATEWAY_WEBHOOK_INVALID_JSON", "Invalid JSON webhook payload")
	}
	text := strings.TrimSpace(stringMap(raw, "text", ""))
	if text == "" {
		return gatewayInboundMessage{}, gatewayFallbackPayload("GATEWAY_WEBHOOK_MISSING_TEXT", "Missing required webhook field: text")
	}
	if len(text) > 65536 {
		return gatewayInboundMessage{}, gatewayFallbackPayload("GATEWAY_WEBHOOK_TEXT_TOO_LONG", "Webhook text exceeds 65536 bytes")
	}
	msg := gatewayInboundMessage{
		Platform: "webhook",
		ChatID:   firstNonEmpty(stringMap(raw, "chat_id", ""), stringMap(raw, "chatId", ""), "webhook"),
		UserID:   firstNonEmpty(stringMap(raw, "user_id", ""), stringMap(raw, "userId", ""), "webhook-user"),
		Username: firstNonEmpty(stringMap(raw, "username", ""), "webhook"),
		Text:     text,
	}
	return msg, nil
}

func (h *Handler) gatewayMappingForMessage(ctx context.Context, msg gatewayInboundMessage) (map[string]any, bool, error) {
	if h.DB == nil {
		return nil, false, nil
	}
	if ok, err := h.systemTableExists(ctx, "gateway_session_mappings"); err != nil || !ok {
		return nil, false, err
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT * FROM gateway_session_mappings WHERE platform=? AND chat_id=? AND user_id=? ORDER BY updated_at DESC LIMIT 1`, msg.Platform, msg.ChatID, msg.UserID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items, err := genericRows(rows)
	if err != nil || len(items) == 0 {
		return nil, false, err
	}
	return items[0], true, nil
}

func (h *Handler) narratorExists(ctx context.Context, narratorID string) bool {
	if h.DB == nil || narratorID == "" {
		return false
	}
	if ok, _ := h.systemTableExists(ctx, "narrators"); !ok {
		return false
	}
	var id string
	return h.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE id=?`, narratorID).Scan(&id) == nil
}

func (h *Handler) gatewaySessionRows(ctx context.Context) ([]map[string]any, error) {
	if h.DB == nil {
		return []map[string]any{}, nil
	}
	if ok, err := h.systemTableExists(ctx, "gateway_session_mappings"); err != nil || !ok {
		return []map[string]any{}, err
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT * FROM gateway_session_mappings ORDER BY rowid DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return genericRows(rows)
}

func (h *Handler) systemTableExists(ctx context.Context, table string) (bool, error) {
	if h.DB == nil {
		return false, nil
	}
	var name string
	err := h.DB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func genericRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
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
		items = append(items, item)
	}
	return items, rows.Err()
}

func hostname() string {
	name, _ := os.Hostname()
	return name
}

func (h *Handler) RegisterUsageHistory(r chi.Router) {
	r.Get("/", httpx.Handler(h.adminOnly(h.usageHistoryList)))
	r.Get("/providers", httpx.Handler(h.adminOnly(h.usageHistoryProviders)))
	r.Get("/stats", httpx.Handler(h.adminOnly(h.usageHistoryStats)))
	r.Get("/timeseries", httpx.Handler(h.adminOnly(h.usageHistoryTimeseries)))
	r.Get("/{id}", httpx.Handler(h.adminOnly(h.usageHistoryGet)))
}

func (h *Handler) RegisterRuntime(r chi.Router) {
	r.Get("/scan", httpx.Handler(h.adminOnly(h.scanRuntime)))
	r.Get("/cached", httpx.Handler(h.adminOnly(h.cachedRuntime)))
	r.Post("/cleanup", httpx.Handler(h.adminOnly(h.cleanupRuntime)))
}

func (h *Handler) RegisterUpdate(r chi.Router) {
	r.Get("/check", httpx.Handler(h.updateCheck))
	r.Get("/version", httpx.Handler(h.updateVersion))
	r.Get("/directory", httpx.Handler(h.adminOnly(h.updateDirectory)))
	r.Get("/status", httpx.Handler(h.adminOnly(h.updateStatus)))
	r.Post("/cleanup", httpx.Handler(h.adminOnly(h.updateCleanup)))
	r.Post("/download", httpx.Handler(h.adminOnly(h.updateDownload)))
	r.Post("/apply", httpx.Handler(h.adminOnly(h.updateApply)))
}

func (h *Handler) updateCheck(w http.ResponseWriter, r *http.Request) error {
	current := currentBackendVersion()
	manifestURL := strings.TrimSpace(os.Getenv("NARRAFORK_UPDATE_MANIFEST_URL"))
	payload := map[string]any{"updateAvailable": false, "currentVersion": current, "releaseInfo": nil, "source": "go_backend", "selfUpdateAvailable": false, "instructions": updateManualInstructions("")}
	if manifestURL == "" {
		payload["reason"] = "NARRAFORK_UPDATE_MANIFEST_URL is not configured"
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	manifest, err := getJSON(ctx, manifestURL, map[string]string{})
	if err != nil {
		payload["error"] = err.Error()
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	latest := firstNonEmpty(stringMap(manifest, "version", ""), stringMap(manifest, "latestVersion", ""))
	payload["releaseInfo"] = manifest
	payload["latestVersion"] = latest
	payload["updateAvailable"] = latest != "" && latest != current
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) updateVersion(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": currentBackendVersion(), "commit": currentBackendCommit(), "platform": runtime.GOOS, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "selfUpdateAvailable": false, "manualOnly": true})
	return nil
}

func (h *Handler) updateDirectory(w http.ResponseWriter, r *http.Request) error {
	dir, err := updateBaseDir()
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"directory": dir, "selfUpdateAvailable": false, "instructions": updateManualInstructions(dir)})
	return nil
}

type updateDownloadFile struct {
	URL    string
	Name   string
	Size   int64
	SHA512 string
}

type updateDownloadedFileInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA512 string `json:"sha512,omitempty"`
}

type updateDownloadMetadata struct {
	Version             string                     `json:"version"`
	Directory           string                     `json:"directory"`
	Files               []updateDownloadedFileInfo `json:"files"`
	TotalBytes          int64                      `json:"totalBytes"`
	DownloadedAt        string                     `json:"downloadedAt"`
	SelfUpdateAvailable bool                       `json:"selfUpdateAvailable"`
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) error {
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	dir, err := updateVersionDir(version)
	if err != nil {
		return err
	}
	status := updateStatusPayload(version, dir)
	httpx.WriteJSON(w, http.StatusOK, status)
	return nil
}

func (h *Handler) updateCleanup(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	version := strings.TrimSpace(stringMap(body, "version", r.URL.Query().Get("version")))
	if version != "" {
		dir, err := updateVersionDir(version)
		if err != nil {
			return err
		}
		removed, err := removeUpdatePathIfSafe(dir)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "removed": removed, "directory": dir})
		return nil
	}

	base, err := updateBaseDir()
	if err != nil {
		return err
	}
	removed, err := cleanupOldUpdateEntries(base, 7*24*time.Hour)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "removed": removed, "directory": base})
	return nil
}

func (h *Handler) updateDownload(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	version, files, expectedBytes := updateDownloadRequestMetadata(body, r)
	send := newUpdateSSESender(w)

	send("progress", updateProgressPayload("checking", 0, expectedBytes, ""))
	if len(files) == 0 {
		send("error", updateDownloadErrorPayload(version, "No update artifact URL was provided; go_backend does not include a bundled update source."))
		return nil
	}
	for _, file := range files {
		parsed, err := url.Parse(file.URL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			send("error", updateDownloadErrorPayload(version, "valid http(s) update URL is required"))
			return nil
		}
	}

	dir, err := updateVersionDir(version)
	if err != nil {
		send("error", updateDownloadErrorPayload(version, err.Error()))
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		send("error", updateDownloadErrorPayload(version, err.Error()))
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	totalBytes := expectedBytes
	totalWritten := int64(0)
	lastEmitted := int64(0)
	limit := int64(512 * 1024 * 1024)
	downloaded := make([]updateDownloadedFileInfo, 0, len(files))
	downloadedPaths := []string{}
	send("progress", updateProgressPayload("downloading", 0, totalBytes, ""))

	for _, updateFile := range files {
		artifact := filepath.Join(dir, updateFile.Name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateFile.URL, nil)
		if err != nil {
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, err.Error()))
			return nil
		}
		resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
		if err != nil {
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, err.Error()))
			return nil
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := "Update download failed: " + resp.Status
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
				message += ": " + trimmed
			}
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, message))
			return nil
		}

		fileTotal := updateFile.Size
		if fileTotal <= 0 && resp.ContentLength > 0 {
			fileTotal = resp.ContentLength
			if totalBytes <= 0 && len(files) == 1 {
				totalBytes = resp.ContentLength
			}
		}
		file, err := os.OpenFile(artifact, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			_ = resp.Body.Close()
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, err.Error()))
			return nil
		}

		hash := sha512.New()
		writer := io.MultiWriter(file, hash)
		fileWritten := int64(0)
		buf := make([]byte, 64*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if totalWritten+int64(n) > limit {
					_ = file.Close()
					_ = resp.Body.Close()
					_ = os.Remove(artifact)
					cleanupDownloadedUpdateFiles(downloadedPaths)
					send("error", updateDownloadErrorPayload(version, "update artifacts exceed 512 MiB"))
					return nil
				}
				if _, err := writer.Write(buf[:n]); err != nil {
					_ = file.Close()
					_ = resp.Body.Close()
					_ = os.Remove(artifact)
					cleanupDownloadedUpdateFiles(downloadedPaths)
					send("error", updateDownloadErrorPayload(version, err.Error()))
					return nil
				}
				fileWritten += int64(n)
				totalWritten += int64(n)
				if totalBytes > 0 && (totalWritten == totalBytes || totalWritten-lastEmitted >= 1024*1024) {
					send("progress", updateProgressPayload("downloading", totalWritten, totalBytes, ""))
					lastEmitted = totalWritten
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = file.Close()
				_ = resp.Body.Close()
				_ = os.Remove(artifact)
				cleanupDownloadedUpdateFiles(downloadedPaths)
				send("error", updateDownloadErrorPayload(version, readErr.Error()))
				return nil
			}
		}
		_ = resp.Body.Close()
		if err := file.Close(); err != nil {
			_ = os.Remove(artifact)
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, err.Error()))
			return nil
		}
		if fileTotal > 0 && fileWritten != fileTotal {
			_ = os.Remove(artifact)
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, "update artifact size mismatch"))
			return nil
		}
		actualHex := hex.EncodeToString(hash.Sum(nil))
		actualBase64 := base64.StdEncoding.EncodeToString(hash.Sum(nil))
		if !updateSHA512Matches(updateFile.SHA512, actualHex, actualBase64) {
			_ = os.Remove(artifact)
			cleanupDownloadedUpdateFiles(downloadedPaths)
			send("error", updateDownloadErrorPayload(version, "SHA512 mismatch for update artifact "+updateFile.Name))
			return nil
		}
		downloadedPaths = append(downloadedPaths, artifact)
		downloaded = append(downloaded, updateDownloadedFileInfo{Name: updateFile.Name, Path: artifact, Size: fileWritten, SHA512: firstNonEmpty(updateFile.SHA512, actualBase64)})
	}
	if totalBytes <= 0 {
		totalBytes = totalWritten
	}
	if totalWritten != totalBytes {
		send("progress", updateProgressPayload("downloading", totalWritten, totalBytes, ""))
	}
	metadata := updateDownloadMetadata{Version: version, Directory: dir, Files: downloaded, TotalBytes: totalWritten, DownloadedAt: time.Now().Format(time.RFC3339), SelfUpdateAvailable: false}
	if err := writeUpdateDownloadMetadata(dir, metadata); err != nil {
		cleanupDownloadedUpdateFiles(downloadedPaths)
		send("error", updateDownloadErrorPayload(version, err.Error()))
		return nil
	}

	send("complete", updateDownloadCompletePayload(version, dir, downloaded, totalWritten, totalBytes))
	return nil
}

func newUpdateSSESender(w http.ResponseWriter) func(string, any) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return func(event string, data any) {
		payload, err := json.Marshal(data)
		if err != nil {
			payload, _ = json.Marshal(updateDownloadErrorPayload("", err.Error()))
			event = "error"
		}
		_, _ = w.Write([]byte("event: " + event + "\n"))
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(payload)
		_, _ = w.Write([]byte("\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func updateDownloadRequestMetadata(body map[string]any, r *http.Request) (string, []updateDownloadFile, int64) {
	releaseInfo, _ := body["releaseInfo"].(map[string]any)
	version := strings.TrimSpace(firstNonEmpty(stringMap(body, "version", ""), firstNonEmpty(stringMap(releaseInfo, "version", ""), r.URL.Query().Get("version"))))
	rawURL := firstNonEmpty(stringMap(body, "url", ""), firstNonEmpty(stringMap(body, "downloadUrl", ""), stringMap(body, "artifactUrl", "")))
	if rawURL == "" {
		rawURL = firstNonEmpty(stringMap(releaseInfo, "url", ""), firstNonEmpty(stringMap(releaseInfo, "downloadUrl", ""), stringMap(releaseInfo, "artifactUrl", "")))
	}
	expectedBytes := int64(numericMapOr(body, "totalBytes", numericMapOr(releaseInfo, "totalBytes", 0)))
	if rawURL != "" {
		file := updateDownloadFile{URL: strings.TrimSpace(rawURL), Name: "artifact", Size: expectedBytes, SHA512: firstNonEmpty(stringMap(body, "sha512", ""), stringMap(releaseInfo, "sha512", ""))}
		return version, []updateDownloadFile{file}, expectedBytes
	}

	files := updateFilesFromReleaseInfo(releaseInfo, version)
	if expectedBytes <= 0 {
		for _, file := range files {
			expectedBytes += file.Size
		}
	}
	return version, files, expectedBytes
}

func updateFilesFromReleaseInfo(releaseInfo map[string]any, version string) []updateDownloadFile {
	filesRaw, _ := releaseInfo["files"].([]any)
	files := make([]updateDownloadFile, 0, len(filesRaw))
	usedNames := map[string]int{}
	for index, item := range filesRaw {
		file, _ := item.(map[string]any)
		fileURL := strings.TrimSpace(firstNonEmpty(stringMap(file, "url", ""), firstNonEmpty(stringMap(file, "downloadUrl", ""), stringMap(file, "artifactUrl", ""))))
		if fileURL == "" {
			continue
		}
		name := uniqueUpdateFileName(updateArtifactFileName(file, fileURL, version, index, len(filesRaw)), usedNames)
		size := int64(numericMapOr(file, "size", 0))
		sha := stringMap(file, "sha512", "")
		if len(filesRaw) == 1 {
			if size <= 0 {
				size = int64(numericMapOr(releaseInfo, "totalBytes", 0))
			}
			sha = firstNonEmpty(sha, stringMap(releaseInfo, "sha512", ""))
		}
		files = append(files, updateDownloadFile{URL: fileURL, Name: name, Size: size, SHA512: sha})
	}
	return files
}

func updateArtifactFileName(file map[string]any, rawURL, version string, index, total int) string {
	if total == 1 {
		return "artifact"
	}
	candidate := firstNonEmpty(stringMap(file, "path", ""), firstNonEmpty(stringMap(file, "filename", ""), stringMap(file, "name", "")))
	if candidate == "" {
		if parsed, err := url.Parse(rawURL); err == nil {
			candidate = pathBase(parsed.Path)
		}
	}
	if candidate == "" || candidate == "." || candidate == string(filepath.Separator) {
		candidate = "artifact-" + strconv.Itoa(index+1)
	}
	candidate = filepath.Base(candidate)
	candidate = strings.NewReplacer("/", "-", "\\", "-", "\x00", "", "<", "-", ">", "-", ":", "-", "\"", "-", "|", "-", "?", "-", "*", "-").Replace(candidate)
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || candidate == "." || candidate == ".." {
		candidate = "narrafork-" + strings.NewReplacer("/", "-", "\\", "-", "..", "-").Replace(firstNonEmpty(version, "update"))
	}
	return candidate
}

func uniqueUpdateFileName(name string, used map[string]int) string {
	count := used[name]
	used[name] = count + 1
	if count == 0 {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return stem + "-" + strconv.Itoa(count+1) + ext
}

func pathBase(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	idx := strings.LastIndex(p, "/")
	if idx >= 0 {
		return p[idx+1:]
	}
	return p
}

func updateProgressPayload(phase string, bytesDownloaded, totalBytes int64, errorMessage string) map[string]any {
	payload := map[string]any{
		"phase":           phase,
		"bytesDownloaded": bytesDownloaded,
		"totalBytes":      totalBytes,
		"progress":        bytesDownloaded,
		"total":           totalBytes,
		"percent":         updateDownloadPercent(bytesDownloaded, totalBytes),
	}
	if errorMessage != "" {
		payload["error"] = errorMessage
	}
	return payload
}

func updateDownloadErrorPayload(version, message string) map[string]any {
	payload := updateProgressPayload("error", 0, 0, message)
	payload["success"] = false
	payload["version"] = version
	payload["message"] = message
	payload["selfUpdateAvailable"] = false
	payload["instructions"] = updateManualInstructions("")
	return payload
}

func updateDownloadCompletePayload(version, directory string, files []updateDownloadedFileInfo, bytesDownloaded, totalBytes int64) map[string]any {
	if totalBytes <= 0 {
		totalBytes = bytesDownloaded
	}
	updatePath := directory
	if len(files) == 1 {
		updatePath = files[0].Path
	}
	payload := updateProgressPayload("complete", bytesDownloaded, totalBytes, "")
	payload["success"] = true
	payload["version"] = version
	payload["downloaded"] = true
	payload["ready"] = true
	payload["bytes"] = bytesDownloaded
	payload["updatePath"] = updatePath
	payload["artifactPath"] = updatePath
	payload["directory"] = directory
	payload["files"] = files
	payload["placed"] = false
	payload["selfUpdateAvailable"] = false
	payload["instructions"] = updateManualInstructions(updatePath)
	return payload
}

func updateDownloadPercent(bytesDownloaded, totalBytes int64) int {
	if totalBytes <= 0 {
		return 0
	}
	percent := int(math.Round(float64(bytesDownloaded) * 100 / float64(totalBytes)))
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func updateSHA512Matches(expected, actualHex, actualBase64 string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	expected = strings.TrimPrefix(expected, "sha512-")
	return strings.EqualFold(expected, actualHex) || expected == actualBase64
}

func updateManualInstructions(updatePath string) map[string]any {
	message := "go_backend downloaded update artifact(s) for an operator, but in-process self-update/apply is not supported. Stop NarraFork and manually replace or run the downloaded artifact as appropriate."
	instructions := map[string]any{"manual": true, "message": message}
	if updatePath != "" {
		instructions["updatePath"] = updatePath
	}
	return instructions
}

func writeUpdateDownloadMetadata(dir string, metadata updateDownloadMetadata) error {
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(updateMetadataPath(dir), payload, 0o600)
}

func readUpdateDownloadMetadata(dir string) (updateDownloadMetadata, bool) {
	var metadata updateDownloadMetadata
	payload, err := os.ReadFile(updateMetadataPath(dir))
	if err != nil {
		return metadata, false
	}
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return metadata, false
	}
	return metadata, true
}

func updateMetadataPath(dir string) string {
	return filepath.Join(dir, "metadata.json")
}

func updateStatusPayload(version, dir string) map[string]any {
	metadata, hasMetadata := readUpdateDownloadMetadata(dir)
	ready := false
	files := []updateDownloadedFileInfo{}
	updateFile := ""
	updatePath := ""
	if hasMetadata && (version == "" || metadata.Version == version) {
		ready = len(metadata.Files) > 0
		for _, file := range metadata.Files {
			info, err := os.Stat(file.Path)
			if err != nil || info.IsDir() || (file.Size > 0 && info.Size() != file.Size) {
				ready = false
				break
			}
			files = append(files, file)
		}
		if ready {
			if len(files) == 1 {
				updateFile = files[0].Name
				updatePath = files[0].Path
			} else if len(files) > 1 {
				updateFile = files[0].Name
				updatePath = dir
			}
		} else {
			files = nil
		}
		if version == "" {
			version = metadata.Version
		}
	}
	if !ready {
		artifact := filepath.Join(dir, "artifact")
		info, err := os.Stat(artifact)
		ready = err == nil && !info.IsDir()
		if ready {
			updateFile = "artifact"
			updatePath = artifact
			files = []updateDownloadedFileInfo{{Name: "artifact", Path: artifact, Size: info.Size()}}
		}
	}
	payload := map[string]any{
		"ready":               ready,
		"downloaded":          ready,
		"version":             version,
		"directory":           dir,
		"canAutoRestart":      false,
		"placed":              false,
		"selfUpdateAvailable": false,
		"instructions":        updateManualInstructions(updatePath),
	}
	if updateFile != "" {
		payload["updateFile"] = updateFile
	}
	if updatePath != "" {
		payload["updatePath"] = updatePath
		payload["artifactPath"] = updatePath
	}
	if len(files) > 0 {
		payload["files"] = files
	}
	return payload
}

func cleanupDownloadedUpdateFiles(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func cleanupOldUpdateEntries(base string, maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		ok, err := removeUpdatePathIfSafe(path)
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

func removeUpdatePathIfSafe(target string) (bool, error) {
	base, err := updateBaseDir()
	if err != nil {
		return false, err
	}
	cleanBase := filepath.Clean(base)
	cleanTarget := filepath.Clean(target)
	if cleanTarget == cleanBase {
		return false, nil
	}
	rel, err := filepath.Rel(cleanBase, cleanTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	if _, err := os.Stat(cleanTarget); os.IsNotExist(err) {
		return false, nil
	}
	if err := os.RemoveAll(cleanTarget); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) updateApply(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "serverRestarting": false, "restarting": false, "canAutoRestart": false, "selfUpdateAvailable": false, "message": "go_backend can download artifacts for operators, but in-process self-update/apply is not supported.", "instructions": updateManualInstructions("")})
	return nil
}

func currentBackendVersion() string {
	for _, key := range []string{"NARRAFORK_VERSION", "APP_VERSION"} {
		if version := strings.TrimSpace(os.Getenv(key)); version != "" {
			return version
		}
	}
	return "go-backend-dev"
}

func currentBackendCommit() string {
	for _, key := range []string{"NARRAFORK_COMMIT", "GIT_COMMIT"} {
		if commit := strings.TrimSpace(os.Getenv(key)); commit != "" {
			return commit
		}
	}
	return ""
}

func updateBaseDir() (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "updates"), nil
}

func updateVersionDir(version string) (string, error) {
	base, err := updateBaseDir()
	if err != nil {
		return "", err
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = "pending"
	}
	version = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(version)
	return filepath.Join(base, version), nil
}

func (h *Handler) RegisterProviders(r chi.Router) {
	r.Get("/kiro/status", httpx.Handler(h.kiroStatus))
	r.Get("/kiro/models", httpx.Handler(h.kiroModels))
	r.Post("/kiro/models/refresh", httpx.Handler(h.kiroModels))
	r.Get("/kiro/mcp-tools", httpx.Handler(h.kiroMcpTools))
	r.Post("/kiro/mcp-search", httpx.Handler(h.kiroMcpSearch))
	r.Post("/kiro/chat", httpx.Handler(h.kiroChat))
	r.Post("/kiro/global-proxy", httpx.Handler(h.kiroSetGlobalProxy))
	r.Post("/kiro/load-balancing-mode", httpx.Handler(h.kiroSetLoadBalancingMode))
	r.Post("/kiro/credentials/import", httpx.Handler(h.kiroImportCredentials))
	r.Post("/kiro/credentials/{id}/disable", httpx.Handler(h.kiroCredentialDisable))
	r.Post("/kiro/credentials/{id}/enable", httpx.Handler(h.kiroCredentialEnable))
	r.Post("/kiro/credentials/{id}/reset", httpx.Handler(h.kiroCredentialReset))
	r.Post("/kiro/credentials/{id}/priority", httpx.Handler(h.kiroCredentialPriority))
	r.Post("/kiro/credentials/{id}/refresh", httpx.Handler(h.kiroCredentialRefresh))
	r.Get("/kiro/credentials/{id}/usage", httpx.Handler(h.kiroCredentialUsage))
	r.Patch("/kiro/credentials/{id}", httpx.Handler(h.kiroCredentialUpdate))
	r.Delete("/kiro/credentials/{id}", httpx.Handler(h.kiroCredentialDelete))

	r.Get("/openai/models", httpx.Handler(h.providerCachedModels("openai", false)))
	r.Post("/openai/models/refresh", httpx.Handler(h.providerCachedModels("openai", true)))
	r.Get("/openai/providers/{id}/models", httpx.Handler(h.providerCachedModels("openai", false)))
	r.Post("/openai/providers/{id}/models/refresh", httpx.Handler(h.providerCachedModels("openai", true)))
	r.Get("/anthropic/models", httpx.Handler(h.providerCachedModels("anthropic", false)))
	r.Post("/anthropic/models/refresh", httpx.Handler(h.providerCachedModels("anthropic", true)))
	r.Get("/anthropic/providers/{id}/models", httpx.Handler(h.providerCachedModels("anthropic", false)))
	r.Post("/anthropic/providers/{id}/models/refresh", httpx.Handler(h.providerCachedModels("anthropic", true)))
	r.Get("/nkp/models", httpx.Handler(h.providerCachedModels("nkp", false)))
	r.Post("/nkp/models/refresh", httpx.Handler(h.providerCachedModels("nkp", true)))
	r.Get("/nkp/providers/{id}/models", httpx.Handler(h.providerCachedModels("nkp", false)))
	r.Post("/nkp/providers/{id}/models/refresh", httpx.Handler(h.providerCachedModels("nkp", true)))
	r.Get("/nkp/providers/{id}/quota", httpx.Handler(h.nkpQuota))
	r.Get("/nkp/quotas", httpx.Handler(h.nkpQuotas))

	r.Get("/nug/models", httpx.Handler(h.nugAllModels))
	r.Post("/nug/models/refresh", httpx.Handler(h.nugAllModels))
	r.Get("/nug/providers/{id}/models", httpx.Handler(h.nugModels))
	r.Post("/nug/providers/{id}/models/refresh", httpx.Handler(h.nugModels))
	r.Post("/nug/providers/{id}/login", httpx.Handler(h.nugLogin))
	r.Post("/nug/providers/{id}/api-key", httpx.Handler(h.nugAPIKey))
	r.Get("/nug/providers/{id}/quota", httpx.Handler(h.nugQuota))
	r.Get("/nug/providers/{id}/channels/health", httpx.Handler(h.nugChannelsHealth))
	r.Get("/nug/providers/{id}/usage", httpx.Handler(h.nugUsage))
	r.Get("/nug/providers/{id}/usage/summary", httpx.Handler(h.nugUsageSummary))
	r.Get("/nug/providers/{id}/oauth/start", httpx.Handler(h.nugOAuthStart))

	r.Get("/codex/quota-overview", httpx.Handler(h.codexQuotaOverview))
	r.Get("/codex/status", h.codexStatus)
	r.Get("/codex/default-reasoning-effort", httpx.Handler(h.codexGetDefaultReasoningEffort))
	r.Post("/codex/default-reasoning-effort", httpx.Handler(h.codexSetDefaultReasoningEffort))
	r.Post("/codex/use-websocket", httpx.Handler(h.codexSetUseWebSocket))
	r.Post("/codex/load-balancing-mode", httpx.Handler(h.codexSetLoadBalancingMode))
	r.Post("/codex/tier-order", httpx.Handler(h.codexSetTierOrder))
	r.Post("/codex/global-proxy", httpx.Handler(h.codexSetGlobalProxy))
	r.Post("/codex/import", httpx.Handler(h.codexImport))
	r.Post("/codex/usage-queue/clear", ok)
	r.Post("/codex/auth/browser", httpx.Handler(h.codexAuthBrowser))
	r.Post("/codex/auth/browser/wait", httpx.Handler(h.codexAuthBrowserWait))
	r.Post("/codex/auth/browser/cancel", httpx.Handler(h.codexAuthCancel))
	r.Post("/codex/auth/device/start", httpx.Handler(h.codexDeviceStart))
	r.Post("/codex/auth/device/poll", httpx.Handler(h.codexDevicePoll))
	r.Post("/codex/auth/device/cancel", httpx.Handler(h.codexDeviceCancel))
	r.Post("/codex/credentials/{id}/disable", httpx.Handler(h.codexCredentialDisable))
	r.Post("/codex/credentials/{id}/enable", httpx.Handler(h.codexCredentialEnable))
	r.Post("/codex/credentials/{id}/reset", httpx.Handler(h.codexCredentialReset))
	r.Post("/codex/credentials/{id}/refresh", httpx.Handler(h.codexCredentialRefresh))
	r.Post("/codex/credentials/{id}/usage", httpx.Handler(h.codexCredentialUsage))
	r.Patch("/codex/credentials/{id}", httpx.Handler(h.codexCredentialUpdate))
	r.Delete("/codex/credentials/batch", httpx.Handler(h.codexCredentialBatchDelete))
	r.Delete("/codex/credentials/{id}", httpx.Handler(h.codexCredentialDelete))

	r.Get("/cline/status", httpx.Handler(h.clineStatus))
	r.Get("/cline/models", httpx.Handler(h.providerCachedModels("cline", false)))
	r.Post("/cline/models/refresh", httpx.Handler(h.providerCachedModels("cline", true)))
	r.Get("/cline/providers/{id}/models", httpx.Handler(h.providerCachedModels("cline", false)))
	r.Post("/cline/providers/{id}/models/refresh", httpx.Handler(h.providerCachedModels("cline", true)))
	r.Get("/cline/balance", httpx.Handler(h.clineBalance))
	r.Get("/cline/recommended-models", httpx.Handler(h.clineRecommendedModels))
	r.Get("/cline/pool/search", httpx.Handler(h.clinePoolSearch))
	r.Get("/cline/pool/count", httpx.Handler(h.clinePoolCount))
	r.Post("/cline/enabled-models", httpx.Handler(h.clineSetEnabledModels))
	r.Post("/cline/user-info/refresh", httpx.Handler(h.clineUserInfoRefresh))
	r.Post("/cline/auth/browser", httpx.Handler(h.clineAuthBrowser))
	r.Post("/cline/auth/cancel", httpx.Handler(h.clineAuthCancel))
	r.Post("/cline/auth/callback", httpx.Handler(h.clineAuthCallback))
	r.Post("/cline/auth/logout", httpx.Handler(h.clineAuthLogout))
}

func (h *Handler) kiroStatus(w http.ResponseWriter, r *http.Request) error {
	entries, _ := h.loadKiroCredentials()
	usageCache := h.loadKiroUsageCache()
	config := h.loadKiroConfig()
	loadBalancingMode := stringMap(config, "loadBalancingMode", "priority")
	if loadBalancingMode != "balanced" {
		loadBalancingMode = "priority"
	}
	snapshotEntries := kiroCredentialSnapshots(entries)
	currentID := ""
	available := 0
	for _, entry := range snapshotEntries {
		m, _ := entry.(map[string]any)
		if currentID == "" && !boolMap(m, "disabled") {
			currentID = stringMap(m, "id", "")
		}
		if !boolMap(m, "disabled") {
			available++
		}
	}
	snapshot := map[string]any{
		"entries":           snapshotEntries,
		"credentials":       snapshotEntries,
		"currentId":         currentID,
		"loadBalancingMode": loadBalancingMode,
		"total":             len(snapshotEntries),
		"available":         available,
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"available":   len(snapshotEntries) > 0,
		"snapshot":    snapshot,
		"globalProxy": h.kiroSettingValue("proxy", ""),
		"usageCache":  usageCache,
	})
	return nil
}

func (h *Handler) kiroModels(w http.ResponseWriter, r *http.Request) error {
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return err
	}
	credentialID := r.URL.Query().Get("credentialId")
	seen := map[string]bool{}
	models := []any{}
	addModel := func(modelID string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || seen[modelID] {
			return
		}
		seen[modelID] = true
		models = append(models, map[string]any{"id": modelID, "name": modelID})
	}
	for _, entry := range entries {
		if credentialID != "" && stringMap(entry, "id", "") != credentialID {
			continue
		}
		for _, modelID := range stringList(entry["availableModels"]) {
			addModel(modelID)
		}
	}
	payload := map[string]any{"models": models, "fromCache": true}
	if credentialID != "" {
		payload["credentialId"] = credentialID
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) kiroMcpTools(w http.ResponseWriter, r *http.Request) error {
	if result, err := h.callKiroMCP(r.Context(), "tools/list", map[string]any{}); err == nil {
		httpx.WriteJSON(w, http.StatusOK, result)
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": "go-backend-kiro-tools", "jsonrpc": "2.0", "tools": kiroFallbackMcpTools(), "result": map[string]any{"tools": kiroFallbackMcpTools()}, "fallback": true})
	return nil
}

func (h *Handler) kiroMcpSearch(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Query string `json:"query"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	query := strings.TrimSpace(body.Query)
	if query == "" {
		return httpx.BadRequest("query is required")
	}
	params := map[string]any{"name": "web_search", "arguments": map[string]any{"query": query}}
	if result, err := h.callKiroMCP(r.Context(), "tools/call", params); err == nil {
		httpx.WriteJSON(w, http.StatusOK, result)
		return nil
	} else {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"id":       "go-backend-kiro-search",
			"jsonrpc":  "2.0",
			"error":    map[string]any{"code": -32000, "message": "Kiro MCP search unavailable in go_backend: " + err.Error()},
			"fallback": true,
		})
		return nil
	}
}

func (h *Handler) kiroChat(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("event: done\ndata: \n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (h *Handler) callKiroMCP(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	entry, err := h.firstUsableKiroCredential()
	if err != nil {
		return nil, err
	}
	config := h.loadKiroConfig()
	region := firstNonEmpty(stringMap(entry, "apiRegion", ""), stringMap(entry, "region", ""))
	region = firstNonEmpty(region, stringMap(config, "region", "us-east-1"))
	kiroVersion := firstNonEmpty(stringMap(config, "kiroVersion", ""), "1.26.1")
	token := strings.TrimSpace(stringMap(entry, "accessToken", ""))
	if token == "" {
		return nil, errorsNew("No Kiro access token available")
	}
	body := map[string]any{"id": id.Must(12), "jsonrpc": "2.0", "method": method, "params": params}
	if profileArn := stringMap(entry, "profileArn", ""); profileArn != "" {
		body["profileArn"] = profileArn
	}
	payload, _ := json.Marshal(body)
	urlValue := "https://q." + region + ".amazonaws.com"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlValue, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("x-amz-target", "AmazonCodeWhispererStreamingService.InvokeMCP")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("User-Agent", "AmazonQ-For-CLI/Version/"+kiroVersion)
	req.Header.Set("Host", "q."+region+".amazonaws.com")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-amzn-codewhisperer-os", runtime.GOOS)
	req.Header.Set("x-amzn-codewhisperer-os-version", runtime.GOOS)
	req.Header.Set("x-amzn-codewhisperer-ide", "kiro")
	req.Header.Set("x-amzn-codewhisperer-ide-version", kiroVersion)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1000000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errorsNew(resp.Status + ": " + string(data))
	}
	result := map[string]any{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (h *Handler) firstUsableKiroCredential() (map[string]any, error) {
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if boolMap(entry, "disabled") {
			continue
		}
		if strings.TrimSpace(stringMap(entry, "accessToken", "")) != "" {
			return entry, nil
		}
	}
	return nil, errorsNew("No enabled Kiro credential with access token")
}

func kiroFallbackMcpTools() []map[string]any {
	return []map[string]any{
		{"name": "web_search", "description": "Search the web through Kiro MCP when credentials are available", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}},
	}
}

func (h *Handler) kiroSetGlobalProxy(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	proxy := strings.TrimSpace(stringMap(body, "proxy", ""))
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return httpx.BadRequest("Invalid proxy URL")
		}
	}
	if err := h.patchKiroSetting("proxy", proxy); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) kiroSetLoadBalancingMode(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	mode := stringMap(body, "mode", "")
	if mode != "priority" && mode != "balanced" {
		return httpx.BadRequest("mode must be 'priority' or 'balanced'")
	}
	if err := h.patchKiroSetting("loadBalancingMode", mode); err != nil {
		return err
	}
	config := h.loadKiroConfig()
	if mode == "priority" {
		delete(config, "loadBalancingMode")
	} else {
		config["loadBalancingMode"] = mode
	}
	if err := h.saveKiroConfig(config); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) kiroImportCredentials(w http.ResponseWriter, r *http.Request) error {
	var body any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	inputs := []any{}
	if m, ok := body.(map[string]any); ok {
		if arr, ok := m["credentials"].([]any); ok {
			inputs = arr
		}
	} else if arr, ok := body.([]any); ok {
		inputs = arr
	}
	if inputs == nil {
		return httpx.BadRequest("credentials must be an array")
	}
	if err := h.ensureKiroPaths(); err != nil {
		return err
	}
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if key := kiroCredentialKey(entry); key != "" {
			seen[key] = true
		}
	}
	added := 0
	duplicates := 0
	for _, item := range inputs {
		incoming, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key := kiroCredentialKey(incoming)
		if key == "" || seen[key] {
			duplicates++
			continue
		}
		entry := map[string]any{}
		for k, v := range incoming {
			entry[k] = v
		}
		if stringMap(entry, "id", "") == "" {
			entry["id"] = id.Must(8)
		}
		if _, ok := entry["priority"]; !ok {
			entry["priority"] = len(entries)
		}
		if stringMap(entry, "authMethod", "") == "builder-id" || stringMap(entry, "authMethod", "") == "iam" {
			entry["authMethod"] = "idc"
		}
		entries = append(entries, entry)
		seen[key] = true
		added++
	}
	if err := h.saveKiroCredentials(entries); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"added": added, "duplicates": duplicates})
	return nil
}

func (h *Handler) kiroCredentialDisable(w http.ResponseWriter, r *http.Request) error {
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		entry["disabled"] = true
		entry["disabledReason"] = "manual"
		return nil
	})
}

func (h *Handler) kiroCredentialEnable(w http.ResponseWriter, r *http.Request) error {
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		entry["disabled"] = false
		delete(entry, "disabledReason")
		return nil
	})
}

func (h *Handler) kiroCredentialReset(w http.ResponseWriter, r *http.Request) error {
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		entry["disabled"] = false
		entry["failureCount"] = 0
		delete(entry, "disabledReason")
		return nil
	})
}

func (h *Handler) kiroCredentialPriority(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	priority := int(numericMapOr(body, "priority", 0))
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		entry["priority"] = priority
		return nil
	})
}

func (h *Handler) kiroCredentialRefresh(w http.ResponseWriter, r *http.Request) error {
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		entry["lastRefreshAttemptAt"] = time.Now().Format(time.RFC3339)
		return nil
	})
}

func (h *Handler) kiroCredentialUpdate(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	allowed := map[string]bool{"email": true, "displayName": true, "region": true, "authRegion": true, "apiRegion": true, "subscriptionTitle": true}
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		for k, v := range body {
			if allowed[k] {
				entry[k] = v
			}
		}
		return nil
	})
}

func (h *Handler) kiroCredentialDelete(w http.ResponseWriter, r *http.Request) error {
	credID := chi.URLParam(r, "id")
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return err
	}
	out := entries[:0]
	removed := false
	for _, entry := range entries {
		if stringMap(entry, "id", "") == credID {
			removed = true
			continue
		}
		out = append(out, entry)
	}
	if !removed {
		return httpx.NotFound("Credential not found")
	}
	if err := h.saveKiroCredentials(out); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) kiroCredentialUsage(w http.ResponseWriter, r *http.Request) error {
	credID := chi.URLParam(r, "id")
	cache := h.loadKiroUsageCache()
	if item, ok := cache[credID]; ok {
		httpx.WriteJSON(w, http.StatusOK, item)
		return nil
	}
	entries, _ := h.loadKiroCredentials()
	for _, entry := range entries {
		if stringMap(entry, "id", "") == credID {
			usage := map[string]any{
				"id":                credID,
				"subscriptionTitle": stringMap(entry, "subscriptionTitle", ""),
				"currentUsage":      numericMapOr(entry, "meterUsage", 0),
				"usageLimit":        0,
				"remaining":         0,
				"usagePercentage":   0,
				"queriedAt":         time.Now().Format(time.RFC3339),
			}
			httpx.WriteJSON(w, http.StatusOK, usage)
			return nil
		}
	}
	return httpx.NotFound("Credential not found")
}

func (h *Handler) updateKiroCredential(w http.ResponseWriter, credID string, mutate func(map[string]any) error) error {
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if stringMap(entry, "id", "") == credID {
			if err := mutate(entry); err != nil {
				return err
			}
			if err := h.saveKiroCredentials(entries); err != nil {
				return err
			}
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			return nil
		}
	}
	return httpx.NotFound("Credential not found")
}

func (h *Handler) nkpQuota(w http.ResponseWriter, r *http.Request) error {
	providerID := chi.URLParam(r, "id")
	provider, err := h.providerConfig("nkp", providerID)
	if err != nil {
		return err
	}
	apiKey := strings.TrimSpace(stringMap(provider, "apiKey", ""))
	baseURL := strings.TrimRight(stringMap(provider, "baseUrl", ""), "/")
	if baseURL == "" || apiKey == "" {
		return httpx.BadRequest("Provider not fully configured")
	}
	data, err := getJSON(r.Context(), baseURL+"/v1/quota", map[string]string{"Authorization": "Bearer " + apiKey})
	if err != nil {
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	cache, _ := loadNKPQuotaCache()
	cache[providerID] = map[string]any{
		"quotaBalance":      numericMap(data, "quotaBalance"),
		"quotaTotalGranted": numericMap(data, "quotaTotalGranted"),
	}
	_ = saveNKPQuotaCache(cache)
	httpx.WriteJSON(w, http.StatusOK, data)
	return nil
}

func (h *Handler) nkpQuotas(w http.ResponseWriter, r *http.Request) error {
	cache, _ := loadNKPQuotaCache()
	httpx.WriteJSON(w, http.StatusOK, cache)
	return nil
}

func loadNKPQuotaCache() (map[string]map[string]any, error) {
	path, err := nkpQuotaCachePath()
	if err != nil {
		return map[string]map[string]any{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]map[string]any{}, nil
	}
	cache := map[string]map[string]any{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return map[string]map[string]any{}, err
	}
	return cache, nil
}

func saveNKPQuotaCache(cache map[string]map[string]any) error {
	path, err := nkpQuotaCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func nkpQuotaCachePath() (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nkp-quotas.json"), nil
}

func (h *Handler) providerCachedModels(kind string, refresh bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		if kind == "cline" {
			if err := h.ensureDefaultClineProvider(""); err != nil {
				return err
			}
		}
		providerID := chi.URLParam(r, "id")
		cache, _ := loadProviderModelCache(kind)
		if refresh {
			if providerID != "" {
				provider, err := h.providerConfig(kind, providerID)
				if err != nil {
					return err
				}
				models, extra, err := h.fetchProviderModels(r.Context(), kind, provider)
				if err != nil {
					return httpx.Error(http.StatusInternalServerError, "MODEL_REFRESH_FAILED", err.Error())
				}
				cache[providerID] = models
				_ = saveProviderModelCache(kind, cache)
				response := map[string]any{"models": models, "fromCache": false}
				for key, value := range extra {
					response[key] = value
				}
				httpx.WriteJSON(w, http.StatusOK, response)
				return nil
			}

			results := []map[string]any{}
			for _, provider := range h.providerConfigs(kind) {
				models, extra, err := h.fetchProviderModels(r.Context(), kind, provider)
				result := map[string]any{
					"providerId": stringMap(provider, "id", ""),
					"name":       stringMap(provider, "name", ""),
					"count":      0,
				}
				for key, value := range extra {
					result[key] = value
				}
				if err != nil {
					result["error"] = err.Error()
					results = append(results, result)
					continue
				}
				providerID := stringMap(provider, "id", "")
				cache[providerID] = models
				result["count"] = len(models)
				results = append(results, result)
			}
			_ = saveProviderModelCache(kind, cache)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results, "models": aggregateProviderModels(kind, h.providerConfigs(kind), cache), "fromCache": false})
			return nil
		}

		if providerID != "" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": cache[providerID], "fromCache": true})
			return nil
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": aggregateProviderModels(kind, h.providerConfigs(kind), cache), "fromCache": true})
		return nil
	}
}

func (h *Handler) providerConfigs(kind string) []map[string]any {
	if h.Settings == nil {
		return []map[string]any{}
	}
	field := kind + "Providers"
	raw, _ := h.Settings.Raw()[field].([]any)
	providers := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		provider, ok := item.(map[string]any)
		if ok {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (h *Handler) providerConfig(kind, providerID string) (map[string]any, error) {
	for _, provider := range h.providerConfigs(kind) {
		if stringMap(provider, "id", "") == providerID {
			return provider, nil
		}
	}
	return nil, httpx.NotFound("Provider")
}

func (h *Handler) fetchProviderModels(ctx context.Context, kind string, provider map[string]any) ([]map[string]any, map[string]any, error) {
	switch kind {
	case "openai":
		models, err := fetchOpenAIModels(ctx, provider)
		return models, map[string]any{}, err
	case "anthropic":
		return fetchAnthropicModels(ctx, provider)
	case "nkp":
		models, err := fetchNKPModels(ctx, provider)
		return models, map[string]any{}, err
	case "cline":
		models, err := fetchClineModels(ctx)
		return models, map[string]any{}, err
	default:
		return []map[string]any{}, map[string]any{}, nil
	}
}

func fetchOpenAIModels(ctx context.Context, provider map[string]any) ([]map[string]any, error) {
	apiKey := strings.TrimSpace(stringMap(provider, "apiKey", ""))
	if apiKey == "" {
		return nil, errProviderFieldMissing("OpenAI API key", provider)
	}
	apiMode := stringMap(provider, "apiMode", "")
	baseURL := strings.TrimRight(stringMap(provider, "baseUrl", ""), "/")
	if baseURL == "" {
		if apiMode == "codex" {
			baseURL = "https://chatgpt.com/backend-api/codex"
		} else {
			baseURL = "https://api.openai.com/v1"
		}
	}
	if apiMode == "codex" && isOfficialCodexDomain(baseURL) {
		models := make([]map[string]any, 0, len(builtinCodexModels))
		for _, modelID := range builtinCodexModels {
			models = append(models, map[string]any{"id": modelID})
		}
		return models, nil
	}
	headers := map[string]string{"Authorization": "Bearer " + apiKey}
	if apiMode == "codex" {
		headers["originator"] = "narrafork"
		if accountID := stringMap(provider, "codexAccountId", ""); accountID != "" && isOfficialCodexDomain(baseURL) {
			headers["ChatGPT-Account-Id"] = accountID
		}
	}
	var errors []string
	for _, target := range openAIModelURLs(baseURL) {
		data, err := getJSON(ctx, target, headers)
		if err != nil {
			errors = append(errors, target+" → "+err.Error())
			continue
		}
		return uniqueModelMaps(extractModelList(data), "id"), nil
	}
	return nil, fmtError("Failed to fetch models from all candidate URLs: " + strings.Join(errors, "; "))
}

func fetchAnthropicModels(ctx context.Context, provider map[string]any) ([]map[string]any, map[string]any, error) {
	apiKey := strings.TrimSpace(stringMap(provider, "apiKey", ""))
	if apiKey == "" {
		return nil, nil, errProviderFieldMissing("Anthropic API key", provider)
	}
	baseURL := strings.TrimRight(stringMap(provider, "baseUrl", ""), "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	headers := map[string]string{"anthropic-version": "2023-06-01"}
	if boolMap(provider, "officialApi") {
		headers["Authorization"] = "Bearer " + apiKey
	} else {
		headers["x-api-key"] = apiKey
	}
	var errors []string
	for _, target := range anthropicModelURLs(baseURL) {
		data, err := getJSON(ctx, target, headers)
		if err != nil {
			errors = append(errors, target+" → "+err.Error())
			continue
		}
		return uniqueModelMaps(extractModelList(data), "id"), map[string]any{"resolvedBaseUrl": strings.TrimSuffix(target, "/models")}, nil
	}
	return nil, nil, fmtError("Anthropic API error: " + strings.Join(errors, "; "))
}

func fetchClineModels(ctx context.Context) ([]map[string]any, error) {
	data, err := getJSON(ctx, "https://openrouter.ai/api/v1/models", clineOpenRouterHeaders())
	if err != nil {
		return nil, err
	}
	raw := extractModelList(data)
	models := make([]map[string]any, 0, len(raw))
	for _, model := range raw {
		modelID := stringMap(model, "id", "")
		if modelID == "" {
			continue
		}
		item := map[string]any{"id": modelID}
		if name := stringMap(model, "name", ""); name != "" {
			item["name"] = name
		}
		if contextLength := numericMap(model, "context_length"); contextLength != nil {
			item["contextLength"] = contextLength
		}
		if pricing, ok := model["pricing"].(map[string]any); ok {
			if prompt := stringMap(pricing, "prompt", ""); prompt != "" {
				item["promptPrice"] = prompt
			}
			if completion := stringMap(pricing, "completion", ""); completion != "" {
				item["completionPrice"] = completion
			}
		}
		models = append(models, item)
	}
	return uniqueModelMaps(models, "id"), nil
}

func clineOpenRouterHeaders() map[string]string {
	return map[string]string{
		"User-Agent":         "Cline/3.74.0",
		"X-PLATFORM":         "node",
		"X-PLATFORM-VERSION": runtime.Version(),
		"X-CLIENT-TYPE":      "extension",
		"X-CLIENT-VERSION":   "3.74.0",
		"X-CORE-VERSION":     "3.74.0",
		"HTTP-Referer":       "https://cline.bot",
		"X-Title":            "Cline",
	}
}

func fetchNKPModels(ctx context.Context, provider map[string]any) ([]map[string]any, error) {
	apiKey := strings.TrimSpace(stringMap(provider, "apiKey", ""))
	if apiKey == "" {
		return nil, errProviderFieldMissing("NKP API key", provider)
	}
	baseURL := strings.TrimRight(stringMap(provider, "baseUrl", ""), "/")
	if baseURL == "" {
		return nil, errProviderFieldMissing("NKP base URL", provider)
	}
	data, err := getJSON(ctx, baseURL+"/v1/kiro/models", map[string]string{"Authorization": "Bearer " + apiKey})
	if err != nil {
		return nil, err
	}
	models := extractModelList(data)
	for _, model := range models {
		if model["model_id"] == nil && model["id"] != nil {
			model["model_id"] = model["id"]
		}
		if stringMap(model, "model_short_name", "") == "" {
			name := stringMap(model, "model_name", "")
			model["model_short_name"] = strings.TrimPrefix(strings.TrimPrefix(name, "Claude "), "claude ")
		}
	}
	return uniqueModelMaps(models, "model_id"), nil
}

var builtinCodexModels = []string{
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex-spark",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5.1-codex",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",
}

func isOfficialCodexDomain(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com") || strings.HasSuffix(host, ".openai.com")
}

func openAIModelURLs(baseURL string) []string {
	urls := []string{baseURL + "/models"}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return urls
	}
	origin := parsed.Scheme + "://" + parsed.Host
	segments := strings.FieldsFunc(strings.Trim(parsed.Path, "/"), func(r rune) bool { return r == '/' })
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] == "v1" && i > 0 {
			candidate := origin + "/" + strings.Join(segments[i:], "/") + "/models"
			urls = appendUniqueString(urls, candidate)
			break
		}
	}
	urls = appendUniqueString(urls, origin+"/api/v1/models")
	urls = appendUniqueString(urls, origin+"/v1/models")
	return urls
}

func anthropicModelURLs(baseURL string) []string {
	urls := []string{baseURL + "/models"}
	if !strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
		urls = appendUniqueString(urls, baseURL+"/v1/models")
	}
	lastSlash := strings.LastIndex(baseURL, "/")
	if lastSlash > len("https://") {
		stripped := baseURL[:lastSlash]
		if stripped != baseURL && stripped != "" {
			urls = appendUniqueString(urls, stripped+"/models")
			if !strings.HasSuffix(strings.TrimRight(stripped, "/"), "/v1") {
				urls = appendUniqueString(urls, stripped+"/v1/models")
			}
		}
	}
	return urls
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func getJSON(ctx context.Context, target string, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmtError(resp.Status + ": " + string(body))
	}
	data := map[string]any{}
	if len(strings.TrimSpace(string(body))) == 0 {
		return data, nil
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func extractModelList(data map[string]any) []map[string]any {
	raw, _ := data["data"].([]any)
	if len(raw) == 0 {
		raw, _ = data["models"].([]any)
	}
	models := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		switch model := item.(type) {
		case map[string]any:
			models = append(models, model)
		case string:
			models = append(models, map[string]any{"id": model})
		}
	}
	return models
}

func uniqueModelMaps(models []map[string]any, idKey string) []map[string]any {
	seen := map[string]bool{}
	unique := make([]map[string]any, 0, len(models))
	for _, model := range models {
		modelID := stringMap(model, idKey, "")
		if modelID == "" && idKey == "model_id" {
			modelID = stringMap(model, "modelId", "")
		}
		if modelID == "" && idKey == "model_id" {
			modelID = stringMap(model, "id", "")
		}
		if modelID == "" || seen[modelID] {
			continue
		}
		seen[modelID] = true
		unique = append(unique, model)
	}
	sort.Slice(unique, func(i, j int) bool {
		return modelIdentity(unique[i], idKey) < modelIdentity(unique[j], idKey)
	})
	return unique
}

func modelIdentity(model map[string]any, idKey string) string {
	if value := stringMap(model, idKey, ""); value != "" {
		return value
	}
	if idKey == "model_id" {
		if value := stringMap(model, "modelId", ""); value != "" {
			return value
		}
		return stringMap(model, "id", "")
	}
	return ""
}

func aggregateProviderModels(kind string, providers []map[string]any, cache map[string][]map[string]any) []map[string]any {
	active := map[string]bool{}
	for _, provider := range providers {
		if !boolMap(provider, "disabled") {
			active[stringMap(provider, "id", "")] = true
		}
	}
	idKey := "id"
	if kind == "nkp" {
		idKey = "model_id"
	}
	all := []map[string]any{}
	for providerID, models := range cache {
		if !active[providerID] {
			continue
		}
		all = append(all, models...)
	}
	return uniqueModelMaps(all, idKey)
}

func loadProviderModelCache(kind string) (map[string][]map[string]any, error) {
	path, err := providerModelCachePath(kind)
	if err != nil {
		return map[string][]map[string]any{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]map[string]any{}, nil
	}
	raw := map[string][]map[string]any{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string][]map[string]any{}, err
	}
	return raw, nil
}

func saveProviderModelCache(kind string, value map[string][]map[string]any) error {
	path, err := providerModelCachePath(kind)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func providerModelCachePath(kind string) (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	if kind == "cline" {
		return filepath.Join(dir, "cline-models.json"), nil
	}
	return filepath.Join(dir, kind+"-models-providers.json"), nil
}

func errProviderFieldMissing(field string, provider map[string]any) error {
	return fmtError(field + " not configured for provider \"" + stringMap(provider, "name", "") + "\"")
}

type stringError string

func (e stringError) Error() string { return string(e) }

func fmtError(message string) error { return stringError(message) }

func (h *Handler) nugAllModels(w http.ResponseWriter, r *http.Request) error {
	if h.Settings == nil {
		return httpx.BadRequest("Settings unavailable")
	}
	providers, _ := h.Settings.Raw()["nugProviders"].([]any)
	cache, _ := loadProviderModelCache("nug")
	seen := map[string]bool{}
	models := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		providerID := stringMap(provider, "id", "")
		data, err := h.fetchNugJSON(r.Context(), providerID, "/v1/models", url.Values{})
		if err != nil {
			continue
		}
		providerModels := normalizeNugModels(data)
		cache[providerID] = mapsFromAnyModels(providerModels)
		for _, model := range providerModels {
			m, _ := model.(map[string]any)
			modelID := stringMap(m, "id", "")
			if modelID == "" || seen[modelID] {
				continue
			}
			seen[modelID] = true
			models = append(models, model)
		}
	}
	_ = saveProviderModelCache("nug", cache)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": models, "fromCache": false})
	return nil
}

func (h *Handler) nugModels(w http.ResponseWriter, r *http.Request) error {
	providerID := chi.URLParam(r, "id")
	data, err := h.fetchNugJSON(r.Context(), providerID, "/v1/models", url.Values{})
	if err != nil {
		return err
	}
	models := normalizeNugModels(data)
	cache, _ := loadProviderModelCache("nug")
	cache[providerID] = mapsFromAnyModels(models)
	_ = saveProviderModelCache("nug", cache)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": models, "fromCache": false})
	return nil
}

func (h *Handler) nugQuota(w http.ResponseWriter, r *http.Request) error {
	data, err := h.fetchNugJSON(r.Context(), chi.URLParam(r, "id"), "/v1/quota", url.Values{})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, data)
	return nil
}

func (h *Handler) nugChannelsHealth(w http.ResponseWriter, r *http.Request) error {
	data, err := h.fetchNugJSON(r.Context(), chi.URLParam(r, "id"), "/v1/channels/health", url.Values{})
	if err != nil {
		return err
	}
	if _, ok := data["channels"]; !ok {
		data["channels"] = []any{}
	}
	httpx.WriteJSON(w, http.StatusOK, data)
	return nil
}

func (h *Handler) nugUsage(w http.ResponseWriter, r *http.Request) error {
	limit := int(numericQuery(r, "limit", 50))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := int(numericQuery(r, "offset", 0))
	if offset < 0 {
		offset = 0
	}
	query := url.Values{"limit": []string{strconv.Itoa(limit)}, "offset": []string{strconv.Itoa(offset)}}
	data, err := h.fetchNugJSON(r.Context(), chi.URLParam(r, "id"), "/v1/usage", query)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, normalizeNugUsageResponse(data))
	return nil
}

func (h *Handler) nugUsageSummary(w http.ResponseWriter, r *http.Request) error {
	rangeValue := r.URL.Query().Get("range")
	period := map[string]string{"today": "today", "7days": "7days", "30days": "month", "month": "month", "all": "all"}[rangeValue]
	if period == "" {
		period = "month"
	}
	data, err := h.fetchNugJSON(r.Context(), chi.URLParam(r, "id"), "/v1/usage/summary", url.Values{"period": []string{period}})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"requestCount":          nugNumber(data, "request_count", "requestCount"),
		"totalMeterUsage":       nugNumber(data, "total_meter_usage", "totalMeterUsage"),
		"totalQuotaCost":        nugNumber(data, "total_quota_cost", "totalQuotaCost"),
		"totalInputTokens":      nugNumber(data, "total_input_tokens", "totalInputTokens"),
		"totalOutputTokens":     nugNumber(data, "total_output_tokens", "totalOutputTokens"),
		"totalCacheWriteTokens": nugNumber(data, "total_cache_write_tokens", "totalCacheWriteTokens"),
		"totalCacheReadTokens":  nugNumber(data, "total_cache_read_tokens", "totalCacheReadTokens"),
	})
	return nil
}

func (h *Handler) fetchNugJSON(ctx context.Context, providerID, endpoint string, query url.Values) (map[string]any, error) {
	provider, baseURL, err := h.nugProvider(providerID)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(stringMap(provider, "apiKey", ""))
	if apiKey == "" {
		return nil, httpx.Error(http.StatusUnprocessableEntity, "NUG_API_KEY_REQUIRED", "Provider API key not configured")
	}
	target := baseURL + endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", "NUG request failed: "+resp.Status+" "+string(body))
	}
	data := map[string]any{}
	if len(strings.TrimSpace(string(body))) == 0 {
		return data, nil
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func mapsFromAnyModels(models []any) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, item := range models {
		if model, ok := item.(map[string]any); ok {
			out = append(out, model)
		}
	}
	return out
}

func normalizeNugModels(data map[string]any) []any {
	if models, ok := data["models"].([]any); ok {
		out := make([]any, 0, len(models))
		for _, item := range models {
			switch m := item.(type) {
			case map[string]any:
				if stringMap(m, "id", "") == "" {
					continue
				}
				out = append(out, m)
			case string:
				out = append(out, map[string]any{"id": m, "name": m})
			}
		}
		return out
	}
	return []any{}
}

func normalizeNugUsageResponse(data map[string]any) map[string]any {
	rawEvents, _ := data["events"].([]any)
	events := make([]any, 0, len(rawEvents))
	for _, item := range rawEvents {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		events = append(events, map[string]any{
			"id":                       nugString(record, "id"),
			"channelType":              nugString(record, "channelType", "channel_type"),
			"model":                    nugString(record, "model"),
			"inputTokens":              nugNumber(record, "inputTokens", "input_tokens"),
			"outputTokens":             nugNumber(record, "outputTokens", "output_tokens"),
			"cacheCreationInputTokens": nugNumber(record, "cacheCreationInputTokens", "cache_creation_input_tokens", "cacheWriteTokens", "cache_write_tokens"),
			"cacheReadInputTokens":     nugNumber(record, "cacheReadInputTokens", "cache_read_input_tokens", "cacheReadTokens", "cache_read_tokens"),
			"quotaCost":                nugNumber(record, "quotaCost", "quota_cost"),
			"meterUsage":               nugNumber(record, "meterUsage", "meter_usage"),
			"status":                   firstNonEmpty(nugString(record, "status"), "unknown"),
			"durationMs":               nugNumber(record, "durationMs", "duration_ms"),
			"createdAt":                nugString(record, "createdAt", "created_at"),
		})
	}
	return map[string]any{"events": events, "total": nugNumber(data, "total")}
}

func nugString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringMap(record, key, ""); value != "" {
			return value
		}
	}
	return ""
}

func nugNumber(record map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if _, ok := record[key]; ok {
			return numericMapOr(record, key, 0)
		}
	}
	return 0
}

func numericQuery(r *http.Request, key string, fallback float64) float64 {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func (h *Handler) nugLogin(w http.ResponseWriter, r *http.Request) error {
	provider, baseURL, err := h.nugProvider(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	username := stringMap(body, "username", "")
	password := stringMap(body, "password", "")
	if username == "" || password == "" {
		return httpx.BadRequest("Missing username or password")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	loginPayload, _ := json.Marshal(map[string]any{"username": username, "password": password})
	loginReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, baseURL+"/api/auth/login", bytes.NewReader(loginPayload))
	if err != nil {
		return err
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return nil
	}
	defer loginResp.Body.Close()
	loginBody, _ := io.ReadAll(io.LimitReader(loginResp.Body, 200000))
	if loginResp.StatusCode < 200 || loginResp.StatusCode >= 300 {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": "NUG login failed: " + string(loginBody)})
		return nil
	}
	sessionToken := extractNugSession(strings.Join(loginResp.Header.Values("Set-Cookie"), "; "))
	if sessionToken == "" {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": "NUG login succeeded but no session cookie received"})
		return nil
	}
	var loginData map[string]any
	_ = json.Unmarshal(loginBody, &loginData)
	apiKeyData, status, err := h.createNugAPIKey(r.Context(), client, baseURL, sessionToken, "narrafork-"+strconv.FormatInt(time.Now().UnixMilli(), 10))
	if err != nil {
		httpx.WriteJSON(w, status, map[string]any{"error": err.Error()})
		return nil
	}
	_ = provider
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"apiKey": apiKeyData["apiKey"], "user": loginData["user"]})
	return nil
}

func (h *Handler) nugAPIKey(w http.ResponseWriter, r *http.Request) error {
	_, baseURL, err := h.nugProvider(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sessionToken := stringMap(body, "sessionToken", "")
	if sessionToken == "" {
		return httpx.BadRequest("Missing sessionToken")
	}
	name := stringMap(body, "name", "narrafork-"+strconv.FormatInt(time.Now().UnixMilli(), 10))
	data, status, err := h.createNugAPIKey(r.Context(), &http.Client{Timeout: 30 * time.Second}, baseURL, sessionToken, name)
	if err != nil {
		httpx.WriteJSON(w, status, map[string]any{"error": err.Error()})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, data)
	return nil
}

func (h *Handler) nugOAuthStart(w http.ResponseWriter, r *http.Request) error {
	provider, baseURL, err := h.nugProvider(chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	clientID := stringMap(provider, "oauthClientId", "")
	if clientID == "" {
		return httpx.BadRequest("OAuth client ID not configured")
	}
	callbackURL := stringMap(provider, "oauthCallbackUrl", "")
	if callbackURL == "" {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		if host == "" {
			host = "localhost:7779"
		}
		proto := r.Header.Get("X-Forwarded-Proto")
		if proto == "" {
			proto = "http"
		}
		callbackURL = proto + "://" + host + "/api/nug/oauth/callback"
	}
	state := chi.URLParam(r, "id") + ":" + strconv.FormatInt(time.Now().UnixMilli(), 10) + ":" + id.Must(12)
	rememberNugOAuthState(state, chi.URLParam(r, "id"), callbackURL)
	authorizeURL, err := url.Parse(baseURL + "/oauth/authorize")
	if err != nil {
		return err
	}
	query := authorizeURL.Query()
	query.Set("client_id", clientID)
	query.Set("redirect_uri", callbackURL)
	query.Set("state", state)
	authorizeURL.RawQuery = query.Encode()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"authorizeUrl": authorizeURL.String(), "state": state})
	return nil
}

func (h *Handler) NugOAuthCallback(w http.ResponseWriter, r *http.Request) error {
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Redirect(w, r, "/settings/providers?oauth_error="+url.QueryEscape(errMsg), http.StatusFound)
		return nil
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Redirect(w, r, "/settings/providers?oauth_error=missing_params", http.StatusFound)
		return nil
	}
	pending, ok := consumeNugOAuthState(state)
	if !ok {
		http.Redirect(w, r, "/settings/providers?oauth_error=state_expired", http.StatusFound)
		return nil
	}
	provider, baseURL, err := h.nugProvider(pending.ProviderID)
	if err != nil {
		http.Redirect(w, r, "/settings/providers?oauth_error=invalid_provider", http.StatusFound)
		return nil
	}
	clientID := stringMap(provider, "oauthClientId", "")
	clientSecret := stringMap(provider, "oauthClientSecret", "")
	if clientID == "" || clientSecret == "" {
		http.Redirect(w, r, "/settings/providers?oauth_error=oauth_not_configured", http.StatusFound)
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"code":          code,
		"client_id":     clientID,
		"client_secret": clientSecret,
		"redirect_uri":  pending.CallbackURL,
	})
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, baseURL+"/api/oauth/token", bytes.NewReader(payload))
	if err != nil {
		http.Redirect(w, r, "/settings/providers?oauth_error="+url.QueryEscape(err.Error()), http.StatusFound)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		http.Redirect(w, r, "/settings/providers?oauth_error="+url.QueryEscape(err.Error()), http.StatusFound)
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Redirect(w, r, "/settings/providers?oauth_error="+url.QueryEscape(string(body)), http.StatusFound)
		return nil
	}
	tokenData := map[string]any{}
	if err := json.Unmarshal(body, &tokenData); err != nil {
		http.Redirect(w, r, "/settings/providers?oauth_error="+url.QueryEscape(err.Error()), http.StatusFound)
		return nil
	}
	if err := h.updateNugProviderAfterOAuth(pending.ProviderID, tokenData); err != nil {
		http.Redirect(w, r, "/settings/providers?oauth_error="+url.QueryEscape(err.Error()), http.StatusFound)
		return nil
	}
	http.Redirect(w, r, "/settings/providers?oauth_success="+url.QueryEscape(pending.ProviderID), http.StatusFound)
	return nil
}

func (h *Handler) updateNugProviderAfterOAuth(providerID string, tokenData map[string]any) error {
	if h.Settings == nil {
		return errorsNew("Settings unavailable")
	}
	raw := h.Settings.Raw()
	providers, _ := raw["nugProviders"].([]any)
	updated := false
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok || stringMap(provider, "id", "") != providerID {
			continue
		}
		if apiKey := stringMap(tokenData, "api_key", ""); apiKey != "" {
			provider["apiKey"] = apiKey
		}
		if deviceID := stringMap(tokenData, "device_id", ""); deviceID != "" {
			provider["oauthDeviceId"] = deviceID
		}
		if username := stringMap(tokenData, "username", ""); username != "" {
			provider["nugUsername"] = username
		}
		if userID := stringMap(tokenData, "user_id", ""); userID != "" {
			provider["nugUserId"] = userID
		}
		updated = true
		break
	}
	if !updated {
		return errorsNew("Provider not found")
	}
	_, err := h.Settings.Patch(map[string]any{"nugProviders": providers})
	return err
}

func rememberNugOAuthState(state, providerID, callbackURL string) {
	nugOAuthMu.Lock()
	defer nugOAuthMu.Unlock()
	expiresAt := time.Now().Add(10 * time.Minute)
	for key, value := range nugOAuthStates {
		if value.ExpiresAt.Before(time.Now()) {
			delete(nugOAuthStates, key)
		}
	}
	nugOAuthStates[state] = nugOAuthState{ProviderID: providerID, CallbackURL: callbackURL, ExpiresAt: expiresAt}
}

func consumeNugOAuthState(state string) (nugOAuthState, bool) {
	nugOAuthMu.Lock()
	defer nugOAuthMu.Unlock()
	pending, ok := nugOAuthStates[state]
	delete(nugOAuthStates, state)
	if !ok || pending.ExpiresAt.Before(time.Now()) {
		return nugOAuthState{}, false
	}
	return pending, true
}

func (h *Handler) nugProvider(providerID string) (map[string]any, string, error) {
	if h.Settings == nil {
		return nil, "", httpx.BadRequest("Settings unavailable")
	}
	raw := h.Settings.Raw()
	providers, _ := raw["nugProviders"].([]any)
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok || stringMap(provider, "id", "") != providerID {
			continue
		}
		baseURL := strings.TrimRight(stringMap(provider, "baseUrl", ""), "/")
		if baseURL == "" {
			return nil, "", httpx.BadRequest("Provider base URL not configured")
		}
		return provider, baseURL, nil
	}
	return nil, "", httpx.NotFound("Provider")
}

func (h *Handler) createNugAPIKey(ctx context.Context, client *http.Client, baseURL, sessionToken, name string) (map[string]any, int, error) {
	payload, _ := json.Marshal(map[string]any{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/api-keys", bytes.NewReader(payload))
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "nug_session="+sessionToken)
	resp, err := client.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, http.StatusBadGateway, errorsNew("NUG API key creation failed: " + string(body))
	}
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, http.StatusBadGateway, err
	}
	return data, http.StatusOK, nil
}

func extractNugSession(setCookieHeader string) string {
	for _, part := range strings.Split(setCookieHeader, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "nug_session=") {
			return strings.TrimPrefix(part, "nug_session=")
		}
	}
	return ""
}

func errorsNew(message string) error { return &simpleError{message: message} }

type simpleError struct{ message string }

func (e *simpleError) Error() string { return e.message }

func (h *Handler) adminOnly(next httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			return httpx.Forbidden("Admin access required")
		}
		return next(w, r)
	}
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, username, role, avatar_color, avatar_image_id, created_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, username, role, createdAt string
		var avatarColor, avatarImageID sql.NullString
		if err := rows.Scan(&id, &username, &role, &avatarColor, &avatarImageID, &createdAt); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "username": username, "role": role, "avatarColor": nullString(avatarColor), "avatarImageId": nullString(avatarImageID), "createdAt": createdAt})
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return rows.Err()
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	if username, ok := body["username"].(string); ok && strings.TrimSpace(username) != "" {
		sets = append(sets, "username = ?")
		args = append(args, strings.TrimSpace(username))
	}
	if role, ok := body["role"].(string); ok && (role == "admin" || role == "user") {
		sets = append(sets, "role = ?")
		args = append(args, role)
	}
	if password, ok := body["password"].(string); ok && password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
		if err != nil {
			return err
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, string(hash))
	}
	if len(sets) == 0 {
		return httpx.BadRequest("At least one field is required")
	}
	args = append(args, chi.URLParam(r, "id"))
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	if claims != nil && claims.Subject == chi.URLParam(r, "id") {
		return httpx.BadRequest("Cannot delete your own account")
	}
	_, err := h.DB.ExecContext(r.Context(), `DELETE FROM users WHERE id = ?`, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) updateAdminSettings(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	next, err := h.Settings.Patch(map[string]any{"auth": map[string]any{"registrationOpen": body["registrationOpen"]}})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"registrationOpen": next["auth"].(map[string]any)["registrationOpen"]})
	return nil
}

func (h *Handler) adminTerminals(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, chapter_id, narrator_id, name, cwd, status, created_at FROM terminals ORDER BY created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, status, createdAt string
		var chapterID, narratorID, cwd sql.NullString
		if err := rows.Scan(&id, &chapterID, &narratorID, &name, &cwd, &status, &createdAt); err != nil {
			return err
		}
		attached := false
		processes := any([]any{})
		if status == "running" && h.TerminalRuntime != nil {
			if shellPID, ok := h.TerminalRuntime.ShellPID(id); ok {
				attached = true
				if processList, err := terminals.ProcessTree(r.Context(), shellPID); err == nil {
					processes = processList
				}
			}
		}
		items = append(items, map[string]any{"id": id, "chapterId": nullString(chapterID), "narratorId": nullString(narratorID), "name": name, "cwd": nullString(cwd), "status": status, "createdAt": createdAt, "attached": attached, "processes": processes})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"terminals": items, "orphanSockets": h.adminOrphanSockets(r.Context())})
	return nil
}

func (h *Handler) adminOrphanSockets(ctx context.Context) []map[string]any {
	orphanRuntime, ok := h.TerminalRuntime.(orphanTerminalRuntime)
	if h.TerminalRuntime == nil || !ok {
		return []map[string]any{}
	}
	ids := orphanRuntime.OrphanIDs(ctx)
	out := make([]map[string]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		terminalID := strings.TrimSpace(id)
		if terminalID == "" || seen[terminalID] {
			continue
		}
		seen[terminalID] = true
		out = append(out, map[string]any{"terminalId": terminalID, "socketPath": h.adminOrphanSocketPath(ctx, terminalID)})
	}
	return out
}

func (h *Handler) adminOrphanSocketPath(ctx context.Context, terminalID string) string {
	if h.DB == nil {
		return ""
	}
	var socket sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT dtach_socket FROM terminals WHERE id = ?`, terminalID).Scan(&socket); err != nil {
		return ""
	}
	if !socket.Valid {
		return ""
	}
	return socket.String
}

func (h *Handler) killTerminal(w http.ResponseWriter, r *http.Request) error {
	terminalID := chi.URLParam(r, "id")
	if h.TerminalRuntime != nil {
		_ = h.TerminalRuntime.Kill(r.Context(), terminalID)
	}
	_, err := h.DB.ExecContext(r.Context(), `UPDATE terminals SET status = 'exited', exit_code = COALESCE(exit_code, 0) WHERE id = ?`, terminalID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) batchKillTerminals(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	results := []map[string]any{}
	for _, id := range body.IDs {
		if h.TerminalRuntime != nil {
			_ = h.TerminalRuntime.Kill(r.Context(), id)
		}
		_, err := h.DB.ExecContext(r.Context(), `UPDATE terminals SET status = 'exited', exit_code = COALESCE(exit_code, 0) WHERE id = ?`, id)
		results = append(results, map[string]any{"id": id, "ok": err == nil})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

func (h *Handler) killOrphanTerminal(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		TerminalID string `json:"terminalId"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	terminalID := strings.TrimSpace(body.TerminalID)
	if terminalID == "" {
		return httpx.BadRequest("terminalId is required")
	}
	orphanRuntime, ok := h.TerminalRuntime.(orphanTerminalRuntime)
	if h.TerminalRuntime == nil || !ok {
		writeTerminalAdminUnsupported(w, "admin.terminal.killOrphan", "Detached terminal orphan cleanup is not supported by the current terminal runtime")
		return nil
	}
	if err := orphanRuntime.KillOrphan(r.Context(), terminalID); err != nil {
		return err
	}
	if h.DB != nil {
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE terminals SET status = 'exited', exit_code = COALESCE(exit_code, 0) WHERE id = ?`, terminalID); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "terminalId": terminalID, "killed": true, "orphan": true})
	return nil
}

func (h *Handler) reattachTerminal(w http.ResponseWriter, r *http.Request) error {
	terminalID := strings.TrimSpace(chi.URLParam(r, "id"))
	if terminalID == "" {
		return httpx.BadRequest("terminal id is required")
	}
	return h.reattachTerminalID(w, r, terminalID, "admin.terminal.reattach")
}

func (h *Handler) reattachOrphanTerminal(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		TerminalID string `json:"terminalId"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	terminalID := strings.TrimSpace(body.TerminalID)
	if terminalID == "" {
		return httpx.BadRequest("terminalId is required")
	}
	return h.reattachTerminalID(w, r, terminalID, "admin.terminal.reattachOrphan")
}

func (h *Handler) reattachTerminalID(w http.ResponseWriter, r *http.Request, terminalID, feature string) error {
	reconnector, ok := h.TerminalRuntime.(terminalReattachRuntime)
	if h.TerminalRuntime == nil || !ok {
		writeTerminalAdminUnsupported(w, feature, "Detached terminal reattach is not supported by the current terminal runtime")
		return nil
	}
	if err := reconnector.Ensure(r.Context(), terminalID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "terminalId": terminalID, "reattached": true})
	return nil
}

func writeTerminalAdminUnsupported(w http.ResponseWriter, feature, message string) {
	httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{
		"ok":        false,
		"code":      "TERMINAL_RUNTIME_UNSUPPORTED",
		"feature":   feature,
		"supported": false,
		"fallback":  true,
		"error":     message,
		"message":   message,
	})
}

func (h *Handler) usageHistoryList(w http.ResponseWriter, r *http.Request) error {
	page := intQuery(r, "page", 1)
	pageSize := intQuery(r, "pageSize", 50)
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize
	where, args := usageFilters(r)
	var total int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM api_requests `+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, pageSize, offset)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,narrator_id,message_id,kind,provider,model,input_tokens,output_tokens,cached_input_tokens,cache_creation_input_tokens,reasoning_tokens,ttft_ms,duration_ms,cost_usd,context_percent,meter_usage,meter_unit,error_message,CASE WHEN raw_dump_json IS NOT NULL AND raw_dump_json != '' THEN 1 ELSE 0 END AS has_raw_dump,COALESCE(LENGTH(raw_dump_json),0) AS raw_dump_bytes,created_at FROM api_requests `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	records, err := usageListRows(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"records": records, "total": total, "page": page, "pageSize": pageSize, "totalPages": (total + pageSize - 1) / pageSize})
	return nil
}

func (h *Handler) usageHistoryProviders(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT DISTINCT provider FROM api_requests WHERE provider IS NOT NULL AND provider != '' ORDER BY provider`)
	if err != nil {
		return err
	}
	defer rows.Close()
	providers := []string{}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			providers = append(providers, p)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": providers})
	return nil
}

func (h *Handler) usageHistoryStats(w http.ResponseWriter, r *http.Request) error {
	endDate := parseUsageDate(r.URL.Query().Get("endDate"), time.Now().UTC())
	startDate := parseUsageDate(r.URL.Query().Get("startDate"), endDate.AddDate(0, 0, -90))
	bounded := strings.TrimSpace(r.URL.Query().Get("startDate")) == "" || strings.TrimSpace(r.URL.Query().Get("endDate")) == ""
	if startDate.After(endDate) {
		startDate = endDate
	}
	where, args := usageFiltersWithDateRange(r, startDate.Format(time.RFC3339), endDate.Format(time.RFC3339))
	var total, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning int
	var cost, avgDuration, avgTtft sql.NullFloat64
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_input_tokens),0), COALESCE(SUM(cached_input_tokens),0), COALESCE(SUM(cache_creation_5m_tokens),0), COALESCE(SUM(cache_creation_1h_tokens),0), COALESCE(SUM(reasoning_tokens),0), SUM(cost_usd), AVG(duration_ms), AVG(ttft_ms) FROM api_requests `+where, args...).Scan(&total, &input, &output, &cacheCreate, &cacheRead, &cache5m, &cache1h, &reasoning, &cost, &avgDuration, &avgTtft); err != nil {
		return err
	}
	totalCost := float64(0)
	if cost.Valid {
		totalCost = cost.Float64
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"totalRequests": total, "totalInputTokens": input, "totalOutputTokens": output, "totalCacheCreationTokens": cacheCreate, "totalCacheReadTokens": cacheRead, "totalCacheCreation5mTokens": cache5m, "totalCacheCreation1hTokens": cache1h, "totalReasoningTokens": reasoning, "totalTokens": input + output + cacheCreate + cacheRead, "totalCost": totalCost, "totalCostUsd": totalCost, "averageDurationMs": nullFloatOrZero(avgDuration), "averageTtftMs": nullFloatOrZero(avgTtft), "bounded": bounded, "effectiveStartDate": startDate.Format(time.RFC3339), "effectiveEndDate": endDate.Format(time.RFC3339)})
	return nil
}

func (h *Handler) usageHistoryTimeseries(w http.ResponseWriter, r *http.Request) error {
	granularity := r.URL.Query().Get("granularity")
	if granularity != "hour" && granularity != "month" {
		granularity = "day"
	}
	config := usageTimeSeriesConfig(granularity)
	now := time.Now().UTC()
	requestedEnd := parseUsageDate(r.URL.Query().Get("endDate"), now)
	requestedEndBucket := normalizeUsageBucket(requestedEnd, granularity)
	requestedStart := parseUsageDate(r.URL.Query().Get("startDate"), addUsageBuckets(requestedEndBucket, -(config.defaultBuckets-1), granularity))
	if requestedStart.After(requestedEnd) {
		requestedStart = requestedEnd
	}
	startBucket := normalizeUsageBucket(requestedStart, granularity)
	endBucket := normalizeUsageBucket(requestedEnd, granularity)
	effectiveStart := requestedStart
	truncated := false
	if countUsageBuckets(startBucket, endBucket, granularity) > config.maxBuckets {
		startBucket = addUsageBuckets(endBucket, -(config.maxBuckets - 1), granularity)
		effectiveStart = startBucket
		truncated = true
	}
	where, args := usageFiltersWithDateRange(r, effectiveStart.Format(time.RFC3339), requestedEnd.Format(time.RFC3339))
	bucketExpr := usageBucketExpression(granularity)
	query := `SELECT ` + bucketExpr + ` AS bucket, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0), COALESCE(SUM(cache_creation_input_tokens),0), COALESCE(SUM(cached_input_tokens),0), COALESCE(SUM(cache_creation_5m_tokens),0), COALESCE(SUM(cache_creation_1h_tokens),0), COALESCE(SUM(reasoning_tokens),0), COALESCE(SUM(cost_usd),0), COALESCE(AVG(duration_ms),0), COALESCE(AVG(ttft_ms),0), COALESCE(SUM(CASE WHEN error_message IS NOT NULL AND TRIM(error_message) != '' THEN 1 ELSE 0 END),0), COALESCE(SUM(meter_usage),0), CASE WHEN COUNT(DISTINCT meter_unit) = 1 THEN MAX(meter_unit) WHEN COUNT(DISTINCT meter_unit) > 1 THEN 'mixed' ELSE NULL END FROM api_requests ` + where + ` GROUP BY bucket ORDER BY bucket`
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byBucket := map[string]map[string]any{}
	for rows.Next() {
		var bucket string
		var requestCount, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning, errorCount int
		var cost, avgDuration, avgTtft, meterUsage float64
		var meterUnit sql.NullString
		if err := rows.Scan(&bucket, &requestCount, &input, &output, &cacheCreate, &cacheRead, &cache5m, &cache1h, &reasoning, &cost, &avgDuration, &avgTtft, &errorCount, &meterUsage, &meterUnit); err != nil {
			return err
		}
		byBucket[bucket] = usagePoint(bucket, requestCount, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning, cost, avgDuration, avgTtft, errorCount, meterUsage, nullableString(meterUnit))
	}
	points := []map[string]any{}
	for cursor := startBucket; !cursor.After(endBucket) && len(points) < config.maxBuckets; cursor = addUsageBuckets(cursor, 1, granularity) {
		timestamp := usageBucketTimestamp(cursor)
		point := byBucket[timestamp]
		if point == nil {
			point = usagePoint(timestamp, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, nil)
		}
		points = append(points, point)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"granularity": granularity, "points": points, "bucketCount": len(points), "maxBuckets": config.maxBuckets, "truncated": truncated, "requestedStartDate": requestedStart.Format(time.RFC3339), "requestedEndDate": requestedEnd.Format(time.RFC3339), "effectiveStartDate": effectiveStart.Format(time.RFC3339), "effectiveEndDate": requestedEnd.Format(time.RFC3339), "generatedAt": time.Now().UTC().Format(time.RFC3339)})
	return rows.Err()
}

func (h *Handler) usageHistoryGet(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,narrator_id,message_id,kind,provider,model,input_tokens,output_tokens,cached_input_tokens,cache_creation_input_tokens,reasoning_tokens,ttft_ms,duration_ms,cost_usd,context_percent,meter_usage,meter_unit,error_message,raw_dump_json,created_at FROM api_requests WHERE id = ?`, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	defer rows.Close()
	records, err := usageRows(rows)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return httpx.NotFound("Usage record")
	}
	httpx.WriteJSON(w, http.StatusOK, records[0])
	return nil
}

func usageFilters(r *http.Request) (string, []any) {
	return usageFiltersFromValues(r.URL.Query(), "", "")
}

func usageFiltersWithDateRange(r *http.Request, startDate, endDate string) (string, []any) {
	return usageFiltersFromValues(r.URL.Query(), startDate, endDate)
}

func usageFiltersFromValues(values url.Values, startDateOverride, endDateOverride string) (string, []any) {
	clauses := []string{}
	args := []any{}
	for key, col := range map[string]string{"narratorId": "narrator_id", "provider": "provider", "model": "model", "kind": "kind"} {
		if v := values.Get(key); v != "" {
			clauses = append(clauses, col+" = ?")
			args = append(args, v)
		}
	}
	if v := values.Get("chapterId"); v != "" {
		clauses = append(clauses, "narrator_id IN (SELECT id FROM narrators WHERE chapter_id = ?)")
		args = append(args, v)
	}
	if v := values.Get("projectId"); v != "" {
		clauses = append(clauses, "narrator_id IN (SELECT n.id FROM narrators n JOIN chapters c ON c.id = n.chapter_id WHERE c.project_id = ?)")
		args = append(args, v)
	}
	startDate := firstNonEmpty(startDateOverride, values.Get("startDate"))
	endDate := firstNonEmpty(endDateOverride, values.Get("endDate"))
	if startDate != "" {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, startDate)
	}
	if endDate != "" {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, endDate)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

type usageSeriesConfig struct {
	maxBuckets     int
	defaultBuckets int
}

func usageTimeSeriesConfig(granularity string) usageSeriesConfig {
	switch granularity {
	case "hour":
		return usageSeriesConfig{maxBuckets: 744, defaultBuckets: 24 * 7}
	case "month":
		return usageSeriesConfig{maxBuckets: 60, defaultBuckets: 24}
	default:
		return usageSeriesConfig{maxBuckets: 366, defaultBuckets: 90}
	}
}

func parseUsageDate(value string, fallback time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback.UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return fallback.UTC()
}

func normalizeUsageBucket(value time.Time, granularity string) time.Time {
	value = value.UTC()
	switch granularity {
	case "hour":
		return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), 0, 0, 0, time.UTC)
	case "month":
		return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	}
}

func addUsageBuckets(value time.Time, amount int, granularity string) time.Time {
	switch granularity {
	case "hour":
		return value.Add(time.Duration(amount) * time.Hour)
	case "month":
		return value.AddDate(0, amount, 0)
	default:
		return value.AddDate(0, 0, amount)
	}
}

func countUsageBuckets(start, end time.Time, granularity string) int {
	if start.After(end) {
		return 0
	}
	switch granularity {
	case "hour":
		return int(end.Sub(start)/time.Hour) + 1
	case "month":
		return (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
	default:
		return int(end.Sub(start)/(24*time.Hour)) + 1
	}
}

func usageBucketExpression(granularity string) string {
	switch granularity {
	case "hour":
		return `substr(created_at, 1, 13) || ':00:00.000Z'`
	case "month":
		return `substr(created_at, 1, 7) || '-01T00:00:00.000Z'`
	default:
		return `substr(created_at, 1, 10) || 'T00:00:00.000Z'`
	}
}

func usageBucketTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func usagePoint(timestamp string, requestCount, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning int, cost, avgDuration, avgTtft float64, errorCount int, meterUsage float64, meterUnit any) map[string]any {
	return map[string]any{"timestamp": timestamp, "requestCount": requestCount, "totalInputTokens": input, "totalOutputTokens": output, "totalCacheCreationTokens": cacheCreate, "totalCacheReadTokens": cacheRead, "totalCacheCreation5mTokens": cache5m, "totalCacheCreation1hTokens": cache1h, "totalReasoningTokens": reasoning, "totalTokens": input + output + cacheCreate + cacheRead, "totalCost": cost, "averageDurationMs": avgDuration, "averageTtftMs": avgTtft, "errorCount": errorCount, "meterUsage": meterUsage, "meterUnit": meterUnit}
}

func nullFloatOrZero(value sql.NullFloat64) float64 {
	if !value.Valid {
		return 0
	}
	return value.Float64
}

func usageListRows(rows *sql.Rows) ([]map[string]any, error) {
	out := []map[string]any{}
	for rows.Next() {
		var id, narratorID, messageID, kind, provider, model, meterUnit, errMsg, created sql.NullString
		var input, output, cached, cacheCreate, reasoning int
		var hasRawDump, rawDumpBytes int
		var ttft, duration sql.NullInt64
		var cost, contextPercent, meter sql.NullFloat64
		if err := rows.Scan(&id, &narratorID, &messageID, &kind, &provider, &model, &input, &output, &cached, &cacheCreate, &reasoning, &ttft, &duration, &cost, &contextPercent, &meter, &meterUnit, &errMsg, &hasRawDump, &rawDumpBytes, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id.String, "narratorId": nullableString(narratorID), "messageId": nullableString(messageID), "kind": stringDefault(kind, "narrator"), "provider": nullableString(provider), "model": nullableString(model), "inputTokens": input, "outputTokens": output, "cachedInputTokens": cached, "cacheCreationInputTokens": cacheCreate, "reasoningTokens": reasoning, "ttftMs": nullableInt(ttft), "durationMs": nullableInt(duration), "costUsd": nullableFloat(cost), "contextPercent": nullableFloat(contextPercent), "meterUsage": nullableFloat(meter), "meterUnit": nullableString(meterUnit), "errorMessage": nullableString(errMsg), "hasRawDump": hasRawDump != 0, "rawDumpBytes": rawDumpBytes, "rawDumpJson": nil, "createdAt": created.String})
	}
	return out, rows.Err()
}

func usageRows(rows *sql.Rows) ([]map[string]any, error) {
	out := []map[string]any{}
	for rows.Next() {
		var id, narratorID, messageID, kind, provider, model, meterUnit, errMsg, raw, created sql.NullString
		var input, output, cached, cacheCreate, reasoning int
		var ttft, duration sql.NullInt64
		var cost, contextPercent, meter sql.NullFloat64
		if err := rows.Scan(&id, &narratorID, &messageID, &kind, &provider, &model, &input, &output, &cached, &cacheCreate, &reasoning, &ttft, &duration, &cost, &contextPercent, &meter, &meterUnit, &errMsg, &raw, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id.String, "narratorId": nullableString(narratorID), "messageId": nullableString(messageID), "kind": stringDefault(kind, "narrator"), "provider": nullableString(provider), "model": nullableString(model), "inputTokens": input, "outputTokens": output, "cachedInputTokens": cached, "cacheCreationInputTokens": cacheCreate, "reasoningTokens": reasoning, "ttftMs": nullableInt(ttft), "durationMs": nullableInt(duration), "costUsd": nullableFloat(cost), "contextPercent": nullableFloat(contextPercent), "meterUsage": nullableFloat(meter), "meterUnit": nullableString(meterUnit), "errorMessage": nullableString(errMsg), "hasRawDump": raw.Valid && raw.String != "", "createdAt": created.String})
	}
	return out, rows.Err()
}
func intQuery(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
func nullableString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
func stringDefault(v sql.NullString, fallback string) string {
	if !v.Valid || v.String == "" {
		return fallback
	}
	return v.String
}
func nullableInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}
func nullableFloat(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

const (
	codexClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexIssuer         = "https://auth.openai.com"
	clineDefaultBaseURL = "https://api.cline.bot"
)

func (h *Handler) clineRecommendedModels(w http.ResponseWriter, r *http.Request) error {
	refresh := r.URL.Query().Get("refresh") == "1" || strings.EqualFold(r.URL.Query().Get("refresh"), "true")
	if refresh {
		data, err := getJSON(r.Context(), "https://api.cline.bot/api/v1/ai/cline/recommended-models", map[string]string{})
		if err == nil {
			normalized := normalizeClineRecommendedModels(data)
			if len(normalized["recommended"].([]any)) > 0 || len(normalized["free"].([]any)) > 0 {
				_ = saveClineRecommendedCache(normalized)
				httpx.WriteJSON(w, http.StatusOK, normalized)
				return nil
			}
		}
	}
	if cached, cacheErr := loadClineRecommendedCache(); cacheErr == nil && cached != nil {
		httpx.WriteJSON(w, http.StatusOK, cached)
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, clineRecommendedFallback())
	return nil
}

func (h *Handler) clinePoolSearch(w http.ResponseWriter, r *http.Request) error {
	if err := h.ensureDefaultClineProvider(""); err != nil {
		return err
	}
	limit := int(numericQuery(r, "limit", 50))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	terms := strings.Fields(query)
	cache, _ := loadProviderModelCache("cline")
	allModels := aggregateProviderModels("cline", h.providerConfigs("cline"), cache)
	matched := make([]map[string]any, 0, len(allModels))
	for _, model := range allModels {
		haystack := strings.ToLower(stringMap(model, "id", "") + " " + stringMap(model, "name", ""))
		ok := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, model)
		}
	}
	visible := matched
	if len(visible) > limit {
		visible = visible[:limit]
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": visible, "total": len(matched)})
	return nil
}

func (h *Handler) clinePoolCount(w http.ResponseWriter, r *http.Request) error {
	if err := h.ensureDefaultClineProvider(""); err != nil {
		return err
	}
	cache, _ := loadProviderModelCache("cline")
	models := aggregateProviderModels("cline", h.providerConfigs("cline"), cache)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"count": len(models)})
	return nil
}

func normalizeClineRecommendedModels(data map[string]any) map[string]any {
	if nested, ok := data["data"].(map[string]any); ok {
		data = nested
	}
	return map[string]any{
		"recommended": normalizeClineRecommendedList(data["recommended"]),
		"free":        normalizeClineRecommendedList(data["free"]),
	}
}

func normalizeClineRecommendedList(value any) []any {
	raw, _ := value.([]any)
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		model, ok := item.(map[string]any)
		if !ok {
			continue
		}
		modelID := stringMap(model, "id", "")
		if modelID == "" {
			continue
		}
		name := firstNonEmpty(stringMap(model, "name", ""), modelID)
		tags := []any{}
		if rawTags, ok := model["tags"].([]any); ok {
			tags = rawTags
		}
		entry := map[string]any{"id": modelID, "name": name, "tags": tags}
		if description := stringMap(model, "description", ""); description != "" {
			entry["description"] = description
		}
		out = append(out, entry)
	}
	return out
}

func clineRecommendedFallback() map[string]any {
	return map[string]any{
		"recommended": []any{
			map[string]any{"id": "anthropic/claude-sonnet-4.6", "name": "Anthropic Claude Sonnet 4.6", "description": "Latest Sonnet release with strong coding and agent performance", "tags": []any{"NEW"}},
			map[string]any{"id": "anthropic/claude-opus-4.6", "name": "Anthropic Claude Opus 4.6", "description": "Most intelligent model for agents and coding", "tags": []any{"BEST"}},
		},
		"free": []any{
			map[string]any{"id": "kwaipilot/kat-coder-pro", "name": "KwaiKAT Kat Coder Pro", "description": "KwaiKAT's most advanced agentic coding model", "tags": []any{"FREE"}},
		},
	}
}

func loadClineRecommendedCache() (map[string]any, error) {
	path, err := clineRecommendedCachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	value := map[string]any{}
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return normalizeClineRecommendedModels(value), nil
}

func saveClineRecommendedCache(value map[string]any) error {
	path, err := clineRecommendedCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func clineRecommendedCachePath() (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cline-recommended-models.json"), nil
}

func (h *Handler) clineSetEnabledModels(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Models []string `json:"models"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if body.Models == nil {
		return httpx.BadRequest("models must be an array")
	}
	if err := h.ensureDefaultClineProvider(""); err != nil {
		return err
	}
	if h.Settings == nil {
		return httpx.BadRequest("Settings unavailable")
	}
	raw := h.Settings.Raw()
	providers, _ := raw["clineProviders"].([]any)
	if len(providers) == 0 {
		return httpx.BadRequest("No Cline provider available")
	}
	models := make([]any, 0, len(body.Models))
	for _, model := range body.Models {
		if strings.TrimSpace(model) != "" {
			models = append(models, model)
		}
	}
	patch := map[string]any{"clineProviders": providers}
	if provider, ok := providers[0].(map[string]any); ok {
		provider["enabledModels"] = models
		if provider["defaultModel"] == nil && len(models) > 0 {
			provider["defaultModel"] = models[0]
		}
		if agent, ok := raw["agent"].(map[string]any); ok {
			windows, _ := agent["modelContextWindows"].(map[string]any)
			if windows == nil {
				windows = map[string]any{}
			}
			cache, _ := loadProviderModelCache("cline")
			prefix := firstNonEmpty(stringMap(provider, "prefix", ""), "cline")
			changedWindows := false
			for _, modelValue := range models {
				modelID, _ := modelValue.(string)
				if modelID == "" || windows[prefix+":"+modelID] != nil {
					continue
				}
				for _, cached := range cache[stringMap(provider, "id", "")] {
					if stringMap(cached, "id", "") == modelID {
						if ctx := numericMap(cached, "contextLength"); ctx != nil {
							windows[prefix+":"+modelID] = ctx
							changedWindows = true
						}
						break
					}
				}
			}
			if changedWindows {
				agent["modelContextWindows"] = windows
				patch["agent"] = agent
			}
		}
	}
	patched, err := h.Settings.Patch(patch)
	if err != nil {
		return err
	}
	modelContextWindows := map[string]any{}
	if agent, ok := patched["agent"].(map[string]any); ok {
		if windows, ok := agent["modelContextWindows"].(map[string]any); ok {
			modelContextWindows = windows
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(models), "modelContextWindows": modelContextWindows})
	return nil
}

func (h *Handler) clineUserInfoRefresh(w http.ResponseWriter, r *http.Request) error {
	creds, err := h.refreshClineUserInfo(r.Context())
	if err != nil {
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	if creds == nil || stringMap(creds, "accessToken", "") == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, clineAuthStatusPayload(creds))
	return nil
}

func (h *Handler) clineBalance(w http.ResponseWriter, r *http.Request) error {
	creds, err := h.validClineCredentials(r.Context())
	if err != nil {
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	if creds == nil || stringMap(creds, "accessToken", "") == "" {
		return httpx.Error(http.StatusUnprocessableEntity, "CLINE_AUTH_REQUIRED", "Not authenticated or balance unavailable")
	}
	if stringMap(creds, "userId", "") == "" {
		creds, _ = h.refreshClineUserInfo(r.Context())
	}
	userID := stringMap(creds, "userId", "")
	if userID == "" {
		return httpx.Error(http.StatusUnprocessableEntity, "CLINE_AUTH_REQUIRED", "Not authenticated or balance unavailable")
	}
	baseURL := strings.TrimRight(clineDefaultBaseURL, "/")
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, baseURL+"/api/v1/users/"+url.PathEscape(userID)+"/balance", nil)
	if err != nil {
		return err
	}
	setClineHeaders(req, stringMap(creds, "accessToken", ""))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpx.Error(http.StatusUnprocessableEntity, "CLINE_BALANCE_UNAVAILABLE", "Not authenticated or balance unavailable")
	}
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}
	if success, _ := data["success"].(bool); success {
		if nested, ok := data["data"].(map[string]any); ok {
			httpx.WriteJSON(w, http.StatusOK, nested)
			return nil
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"balance": numericMap(data, "balance"), "userId": firstNonEmpty(stringMap(data, "userId", ""), userID)})
	return nil
}

func (h *Handler) validClineCredentials(ctx context.Context) (map[string]any, error) {
	creds, err := loadClineCredentials()
	if err != nil {
		return nil, nil
	}
	if stringMap(creds, "accessToken", "") == "" || stringMap(creds, "refreshToken", "") == "" {
		return nil, nil
	}
	if numericMapOr(creds, "expiresAt", 0) >= float64(time.Now().Add(5*time.Minute).Unix()) {
		return creds, nil
	}
	return refreshClineAccessToken(ctx, creds)
}

func refreshClineAccessToken(ctx context.Context, creds map[string]any) (map[string]any, error) {
	payload, _ := json.Marshal(map[string]any{"refreshToken": stringMap(creds, "refreshToken", ""), "grantType": "refresh_token"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(clineDefaultBaseURL, "/")+"/api/v1/auth/refresh", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	setClineHeaders(req, "")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		if path, pathErr := clineCredentialsPath(); pathErr == nil {
			_ = os.Remove(path)
		}
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errorsNew("Cline token refresh failed: " + resp.Status + " " + string(body))
	}
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	nested, _ := data["data"].(map[string]any)
	if success, _ := data["success"].(bool); !success || stringMap(nested, "accessToken", "") == "" {
		return nil, errorsNew("Cline token refresh returned invalid data")
	}
	updated := map[string]any{
		"accessToken":  stringMap(nested, "accessToken", ""),
		"refreshToken": firstNonEmpty(stringMap(nested, "refreshToken", ""), stringMap(creds, "refreshToken", "")),
		"email":        stringMap(creds, "email", ""),
		"displayName":  stringMap(creds, "displayName", ""),
		"startedAt":    numericMapOr(creds, "startedAt", float64(time.Now().UnixMilli())),
	}
	if expiresAt := stringMap(nested, "expiresAt", ""); expiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			updated["expiresAt"] = float64(parsed.Unix())
		}
	}
	if updated["expiresAt"] == nil {
		updated["expiresAt"] = float64(time.Now().Add(time.Hour).Unix())
	}
	if userInfo, ok := nested["userInfo"].(map[string]any); ok {
		updated["email"] = firstNonEmpty(stringMap(userInfo, "email", ""), stringMap(updated, "email", ""))
		updated["displayName"] = firstNonEmpty(stringMap(userInfo, "name", ""), stringMap(updated, "displayName", ""))
	}
	if userID := stringMap(creds, "userId", ""); userID != "" {
		updated["userId"] = userID
	}
	if err := writeJSONFileMustDir(clineCredentialsPath, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (h *Handler) refreshClineUserInfo(ctx context.Context) (map[string]any, error) {
	creds, err := h.validClineCredentials(ctx)
	if err != nil || creds == nil {
		return creds, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(clineDefaultBaseURL, "/")+"/api/v1/users/me", nil)
	if err != nil {
		return nil, err
	}
	setClineHeaders(req, stringMap(creds, "accessToken", ""))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return creds, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	if nested, ok := data["data"].(map[string]any); ok {
		creds["userId"] = firstNonEmpty(stringMap(nested, "id", ""), stringMap(creds, "userId", ""))
		creds["email"] = firstNonEmpty(stringMap(nested, "email", ""), stringMap(creds, "email", ""))
		creds["displayName"] = firstNonEmpty(firstNonEmpty(stringMap(nested, "displayName", ""), stringMap(nested, "name", "")), stringMap(creds, "displayName", ""))
		_ = writeJSONFileMustDir(clineCredentialsPath, creds)
	}
	return creds, nil
}

func clineAuthStatusPayload(creds map[string]any) map[string]any {
	payload := map[string]any{"authenticated": stringMap(creds, "accessToken", "") != ""}
	for _, key := range []string{"email", "displayName", "userId"} {
		if value := stringMap(creds, key, ""); value != "" {
			payload[key] = value
		}
	}
	if expiresAt := numericMap(creds, "expiresAt"); expiresAt != nil {
		payload["expiresAt"] = expiresAt
	}
	return payload
}

func (h *Handler) clineStatus(w http.ResponseWriter, r *http.Request) error {
	_ = h.ensureDefaultClineProvider("")
	creds, _ := loadClineCredentials()
	authenticated := stringMap(creds, "accessToken", "") != ""
	providers := []any{}
	enabledModels := []any{}
	if h.Settings != nil {
		raw := h.Settings.Raw()
		if arr, ok := raw["clineProviders"].([]any); ok {
			for _, item := range arr {
				provider, ok := item.(map[string]any)
				if !ok {
					continue
				}
				providers = append(providers, map[string]any{
					"id":       stringMap(provider, "id", ""),
					"name":     stringMap(provider, "name", ""),
					"prefix":   stringMap(provider, "prefix", "cline"),
					"hasToken": stringMap(provider, "accessToken", "") != "",
				})
				if models, ok := provider["enabledModels"].([]any); ok {
					enabledModels = append(enabledModels, models...)
				}
			}
		}
	}
	clineAuthMu.Lock()
	pending := clineBrowser != nil && clineBrowser.Pending && time.Now().Before(clineBrowser.ExpiresAt)
	clineAuthMu.Unlock()
	payload := map[string]any{
		"authenticated": authenticated,
		"providers":     providers,
		"totalModels":   len(enabledModels),
		"enabledModels": enabledModels,
		"pendingAuth":   pending,
	}
	if authenticated {
		payload["email"] = stringMap(creds, "email", "")
		payload["displayName"] = stringMap(creds, "displayName", "")
		payload["expiresAt"] = numericMap(creds, "expiresAt")
		if userID := stringMap(creds, "userId", ""); userID != "" {
			payload["userId"] = userID
		}
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) clineAuthCallback(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		CallbackURL string `json:"callbackUrl"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.CallbackURL) == "" {
		return httpx.BadRequest("callbackUrl is required")
	}
	creds, err := importClineCallbackURL(body.CallbackURL)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	if err := h.ensureDefaultClineProvider(stringMap(creds, "accessToken", "")); err != nil {
		return err
	}
	h.stopClineBrowserAuth()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "email": stringMap(creds, "email", ""), "displayName": stringMap(creds, "displayName", "")})
	return nil
}

func (h *Handler) clineAuthLogout(w http.ResponseWriter, r *http.Request) error {
	if path, err := clineCredentialsPath(); err == nil {
		_ = os.Remove(path)
	}
	if h.Settings != nil {
		raw := h.Settings.Raw()
		providers, _ := raw["clineProviders"].([]any)
		for _, item := range providers {
			if provider, ok := item.(map[string]any); ok {
				delete(provider, "accessToken")
			}
		}
		_, _ = h.Settings.Patch(map[string]any{"clineProviders": providers})
	}
	h.stopClineBrowserAuth()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) clineAuthCancel(w http.ResponseWriter, r *http.Request) error {
	h.stopClineBrowserAuth()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) clineAuthBrowser(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		APIBaseURL string `json:"apiBaseUrl"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	baseURL := strings.TrimRight(firstNonEmpty(body.APIBaseURL, clineDefaultBaseURL), "/")
	callbackURL := "http://localhost:19876/auth/callback"
	authEndpoint, err := url.Parse(baseURL + "/api/v1/auth/authorize")
	if err != nil {
		return err
	}
	q := authEndpoint.Query()
	q.Set("client_type", "extension")
	q.Set("callback_url", callbackURL)
	q.Set("redirect_uri", callbackURL)
	authEndpoint.RawQuery = q.Encode()
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, authEndpoint.String(), nil)
	if err != nil {
		return err
	}
	setClineHeaders(req, "")
	resp, err := client.Do(req)
	if err != nil {
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	defer resp.Body.Close()
	authorizeURL := resp.Header.Get("Location")
	if authorizeURL == "" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		data := map[string]any{}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 200000)).Decode(&data)
		authorizeURL = stringMap(data, "redirect_url", "")
	}
	if authorizeURL == "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", "Cline auth request failed: "+resp.Status+" "+string(body))
	}
	if err := h.startClineCallbackServer(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"authorizeUrl": authorizeURL})
	return nil
}

func (h *Handler) startClineCallbackServer() error {
	h.stopClineBrowserAuth()
	listener, err := net.Listen("tcp", "127.0.0.1:19876")
	if err != nil {
		return httpx.Error(http.StatusConflict, "PORT_IN_USE", "Cline callback port 19876 is unavailable: "+err.Error())
	}
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" {
			http.NotFound(w, r)
			return
		}
		fullURL := "http://localhost:19876" + r.URL.RequestURI()
		creds, err := importClineCallbackURL(fullURL)
		if err == nil {
			err = h.ensureDefaultClineProvider(stringMap(creds, "accessToken", ""))
		}
		clineAuthMu.Lock()
		if clineBrowser != nil {
			clineBrowser.Pending = false
			if err != nil {
				clineBrowser.Err = err.Error()
			}
		}
		clineAuthMu.Unlock()
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html><body><h2>Authorization failed</h2><p>" + err.Error() + "</p></body></html>"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h2>Authorization successful!</h2><p>You can close this window.</p><script>window.close()</script></body></html>"))
		go h.stopClineBrowserAuth()
	})
	clineAuthMu.Lock()
	clineBrowser = &clineBrowserFlow{Server: server, Pending: true, ExpiresAt: time.Now().Add(5 * time.Minute)}
	clineAuthMu.Unlock()
	go func() { _ = server.Serve(listener) }()
	go func() {
		time.Sleep(5 * time.Minute)
		clineAuthMu.Lock()
		expired := clineBrowser != nil && clineBrowser.Server == server && clineBrowser.Pending
		clineAuthMu.Unlock()
		if expired {
			h.stopClineBrowserAuth()
		}
	}()
	return nil
}

func (h *Handler) stopClineBrowserAuth() {
	clineAuthMu.Lock()
	flow := clineBrowser
	clineBrowser = nil
	clineAuthMu.Unlock()
	if flow != nil && flow.Server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = flow.Server.Shutdown(ctx)
	}
}

func (h *Handler) ensureDefaultClineProvider(accessToken string) error {
	if h.Settings == nil {
		return nil
	}
	raw := h.Settings.Raw()
	providers, _ := raw["clineProviders"].([]any)
	if len(providers) == 0 {
		providers = append(providers, map[string]any{
			"id":            id.Must(8),
			"name":          "Cline",
			"prefix":        "cline",
			"baseUrl":       "https://api.cline.bot/api/v1",
			"defaultModel":  "anthropic/claude-sonnet-4",
			"enabledModels": []any{},
		})
	}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.Contains(stringMap(provider, "baseUrl", ""), "openrouter.ai") {
			provider["baseUrl"] = "https://api.cline.bot/api/v1"
		}
		if accessToken != "" {
			provider["accessToken"] = accessToken
		}
		break
	}
	_, err := h.Settings.Patch(map[string]any{"clineProviders": providers})
	return err
}

func importClineCallbackURL(callbackURL string) (map[string]any, error) {
	parsed, err := url.Parse(strings.TrimSpace(callbackURL))
	if err != nil {
		return nil, errorsNew("Invalid callback URL")
	}
	code := parsed.Query().Get("code")
	if code == "" {
		return nil, errorsNew("No 'code' parameter found in callback URL")
	}
	decoded, err := decodeFlexibleBase64(code)
	if err != nil {
		return nil, errorsNew("Failed to decode callback code parameter")
	}
	idx := strings.LastIndex(decoded, "}")
	if idx < 0 {
		return nil, errorsNew("No JSON found in decoded callback code")
	}
	data := map[string]any{}
	if err := json.Unmarshal([]byte(decoded[:idx+1]), &data); err != nil {
		return nil, errorsNew("Failed to parse callback code JSON")
	}
	if stringMap(data, "accessToken", "") == "" {
		return nil, errorsNew("No accessToken in callback data")
	}
	if stringMap(data, "refreshToken", "") == "" {
		return nil, errorsNew("No refreshToken in callback data")
	}
	displayName := stringMap(data, "name", "")
	if displayName == "" {
		displayName = strings.TrimSpace(stringMap(data, "firstName", "") + " " + stringMap(data, "lastName", ""))
	}
	expiresAt := float64(time.Now().Add(time.Hour).Unix())
	if rawExpires := stringMap(data, "expiresAt", ""); rawExpires != "" {
		if parsedTime, err := time.Parse(time.RFC3339, rawExpires); err == nil {
			expiresAt = float64(parsedTime.Unix())
		}
	}
	creds := map[string]any{
		"accessToken":  stringMap(data, "accessToken", ""),
		"refreshToken": stringMap(data, "refreshToken", ""),
		"expiresAt":    expiresAt,
		"email":        stringMap(data, "email", ""),
		"displayName":  displayName,
		"startedAt":    time.Now().UnixMilli(),
	}
	if err := writeJSONFileMustDir(clineCredentialsPath, creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func (h *Handler) codexGetDefaultReasoningEffort(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reasoningEffort": h.codexSettingsValue("defaultReasoningEffort", nil)})
	return nil
}

func (h *Handler) codexSetDefaultReasoningEffort(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	value := body["reasoningEffort"]
	if s, ok := value.(string); ok && s != "" && !validReasoningEffort(s) {
		return httpx.BadRequest("Invalid reasoningEffort")
	}
	if value == nil || value == "" {
		value = nil
	}
	if err := h.patchCodexSetting("defaultReasoningEffort", value); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "reasoningEffort": value})
	return nil
}

func (h *Handler) codexSetUseWebSocket(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	useWebSocket, ok := body["useWebSocket"].(bool)
	if !ok {
		return httpx.BadRequest("useWebSocket must be boolean")
	}
	if err := h.patchCodexSetting("useWebSocket", useWebSocket); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "useWebSocket": useWebSocket})
	return nil
}

func (h *Handler) codexSetLoadBalancingMode(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	mode := stringMap(body, "mode", "")
	if mode != "priority" && mode != "balanced" && mode != "tier-balanced" && mode != "tiered_sticky" {
		return httpx.BadRequest("Invalid mode. Must be 'priority', 'balanced', 'tier-balanced', or 'tiered_sticky'")
	}
	if err := h.patchCodexSetting("loadBalancingMode", mode); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": mode})
	return nil
}

func (h *Handler) codexSetTierOrder(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	tierOrder, ok := body["tierOrder"].([]any)
	if !ok {
		return httpx.BadRequest("tierOrder must be an array")
	}
	normalized := normalizeCodexTierOrder(tierOrder)
	if err := h.patchCodexSetting("tierOrder", normalized); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "tierOrder": normalized, "effectiveTierOrder": normalized})
	return nil
}

func (h *Handler) codexSetGlobalProxy(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	proxy := strings.TrimSpace(stringMap(body, "proxy", ""))
	if proxy == "" {
		proxy = ""
	} else if parsed, err := url.Parse(proxy); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return httpx.BadRequest("Invalid proxy URL")
	}
	if err := h.patchCodexSetting("proxy", proxy); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) patchCodexSetting(key string, value any) error {
	if h.Settings == nil {
		return nil
	}
	_, err := h.Settings.Patch(map[string]any{"codex": map[string]any{key: value}})
	return err
}

func (h *Handler) codexSettingsValue(key string, fallback any) any {
	if h.Settings == nil {
		return fallback
	}
	if codex, ok := h.Settings.Raw()["codex"].(map[string]any); ok {
		if v, ok := codex[key]; ok {
			return v
		}
	}
	return fallback
}

func validReasoningEffort(value string) bool {
	switch value {
	case "none", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func normalizeCodexTierOrder(values []any) []any {
	allowed := map[string]bool{"free": true, "plus": true, "team": true, "prolite": true, "pro": true, "other": true}
	seen := map[string]bool{}
	out := []any{}
	for _, item := range values {
		value, ok := item.(string)
		if !ok || !allowed[value] || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (h *Handler) codexCredentialDisable(w http.ResponseWriter, r *http.Request) error {
	return h.updateCodexCredentialFlag(w, chi.URLParam(r, "id"), true)
}

func (h *Handler) codexCredentialEnable(w http.ResponseWriter, r *http.Request) error {
	return h.updateCodexCredentialFlag(w, chi.URLParam(r, "id"), false)
}

func (h *Handler) codexCredentialReset(w http.ResponseWriter, r *http.Request) error {
	idParam := chi.URLParam(r, "id")
	updated, err := mutateCodexCredential(idParam, func(entry map[string]any) {
		entry["disabled"] = false
		delete(entry, "disabledReason")
		delete(entry, "lastError")
		entry["successCount"] = float64(0)
		entry["failureCount"] = float64(0)
	})
	if err != nil {
		return err
	}
	if !updated {
		return httpx.NotFound("Codex credential")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) codexCredentialUpdate(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	idParam := chi.URLParam(r, "id")
	updated, err := mutateCodexCredential(idParam, func(entry map[string]any) {
		if displayName, ok := body["displayName"].(string); ok {
			entry["displayName"] = displayName
		}
		if _, ok := body["priority"]; ok {
			entry["priority"] = numericMapOr(body, "priority", numericMapOr(entry, "priority", 0))
		}
	})
	if err != nil {
		return err
	}
	if !updated {
		return httpx.NotFound("Codex credential")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) codexCredentialDelete(w http.ResponseWriter, r *http.Request) error {
	idParam := chi.URLParam(r, "id")
	removed, err := removeCodexCredentials([]string{idParam})
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		return httpx.NotFound("Codex credential")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) codexCredentialBatchDelete(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if len(body.IDs) == 0 {
		return httpx.BadRequest("ids must be a non-empty array")
	}
	removed, err := removeCodexCredentials(body.IDs)
	if err != nil {
		return err
	}
	removedSet := map[string]bool{}
	for _, item := range removed {
		removedSet[item] = true
	}
	notFound := []string{}
	for _, item := range body.IDs {
		if !removedSet[item] {
			notFound = append(notFound, item)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed, "notFound": notFound})
	return nil
}

func (h *Handler) codexCredentialRefresh(w http.ResponseWriter, r *http.Request) error {
	idParam := chi.URLParam(r, "id")
	entries, err := loadCodexCredentials()
	if err != nil {
		return err
	}
	for i, entry := range entries {
		if stringMap(entry, "id", "") != idParam {
			continue
		}
		refreshed, err := refreshCodexCredentialToken(r.Context(), entry)
		if err != nil {
			return httpx.BadRequest(err.Error())
		}
		refreshed["id"] = idParam
		refreshed["priority"] = numericMapOr(entry, "priority", float64(i))
		refreshed["disabled"] = boolMap(entry, "disabled")
		if stringMap(refreshed, "displayName", "") == "" {
			refreshed["displayName"] = entry["displayName"]
		}
		entries[i] = mergeCredentialMetadata(entry, refreshed)
		if err := saveCodexCredentials(entries); err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return httpx.NotFound("Codex credential")
}

func (h *Handler) codexCredentialUsage(w http.ResponseWriter, r *http.Request) error {
	idParam := chi.URLParam(r, "id")
	entries, err := loadCodexCredentials()
	if err != nil {
		return err
	}
	for i, entry := range entries {
		if stringMap(entry, "id", "") != idParam {
			continue
		}
		accessToken := stringMap(entry, "accessToken", "")
		if accessToken == "" || numericMapOr(entry, "expiresAt", 0) < float64(time.Now().UnixMilli()+30000) {
			refreshed, err := refreshCodexCredentialToken(r.Context(), entry)
			if err != nil {
				return httpx.BadRequest(err.Error())
			}
			entry = mergeCredentialMetadata(entry, refreshed)
			entries[i] = entry
			_ = saveCodexCredentials(entries)
			accessToken = stringMap(entry, "accessToken", "")
		}
		usage, err := fetchCodexUsage(r.Context(), accessToken, stringMap(entry, "accountId", ""))
		if err != nil {
			return httpx.BadRequest(err.Error())
		}
		entry["usage"] = usage
		entries[i] = entry
		_ = saveCodexCredentials(entries)
		httpx.WriteJSON(w, http.StatusOK, usage)
		return nil
	}
	return httpx.NotFound("Codex credential")
}

func (h *Handler) updateCodexCredentialFlag(w http.ResponseWriter, idParam string, disabled bool) error {
	updated, err := mutateCodexCredential(idParam, func(entry map[string]any) {
		entry["disabled"] = disabled
		if !disabled {
			delete(entry, "disabledReason")
		}
	})
	if err != nil {
		return err
	}
	if !updated {
		return httpx.NotFound("Codex credential")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func mutateCodexCredential(idParam string, mutate func(map[string]any)) (bool, error) {
	entries, err := loadCodexCredentials()
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if stringMap(entry, "id", "") == idParam {
			mutate(entry)
			return true, saveCodexCredentials(entries)
		}
	}
	return false, nil
}

func removeCodexCredentials(ids []string) ([]string, error) {
	entries, err := loadCodexCredentials()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, idValue := range ids {
		want[idValue] = true
	}
	removed := []string{}
	kept := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		entryID := stringMap(entry, "id", "")
		if want[entryID] {
			removed = append(removed, entryID)
			continue
		}
		kept = append(kept, entry)
	}
	if len(removed) > 0 {
		if err := saveCodexCredentials(kept); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

func refreshCodexCredentialToken(ctx context.Context, entry map[string]any) (map[string]any, error) {
	refreshToken := stringMap(entry, "refreshToken", "")
	if refreshToken == "" {
		return nil, errorsNew("Credential has no refresh token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", codexClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errorsNew("Token refresh failed: " + resp.Status + " " + string(body))
	}
	tokens := map[string]any{}
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}
	refreshed := codexCredentialFromTokenResponse(tokens)
	if stringMap(refreshed, "refreshToken", "") == "" {
		refreshed["refreshToken"] = refreshToken
	}
	return refreshed, nil
}

func mergeCredentialMetadata(previous, next map[string]any) map[string]any {
	merged := map[string]any{}
	for k, v := range previous {
		merged[k] = v
	}
	for k, v := range next {
		if v != nil && v != "" {
			merged[k] = v
		}
	}
	return merged
}

func fetchCodexUsage(ctx context.Context, accessToken, accountID string) (map[string]any, error) {
	if accessToken == "" {
		return nil, errorsNew("Credential has no access token")
	}
	if accountID == "" {
		return nil, errorsNew("Credential has no account ID")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "narrafork/1.0.0 (Go)")
	req.Header.Set("Chatgpt-Account-Id", accountID)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errorsNew("Failed to fetch usage: " + resp.Status + " " + string(body))
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return normalizeCodexUsage(payload), nil
}

func normalizeCodexUsage(payload map[string]any) map[string]any {
	result := map[string]any{"plan_type": stringMap(payload, "plan_type", "unknown"), "queriedAt": time.Now().Format(time.RFC3339)}
	if rateLimit, ok := payload["rate_limit"].(map[string]any); ok {
		if primary, ok := rateLimit["primary_window"].(map[string]any); ok {
			result["primary_window"] = normalizeCodexUsageWindow(primary, true)
		}
		if secondary, ok := rateLimit["secondary_window"].(map[string]any); ok {
			result["secondary_window"] = normalizeCodexUsageWindow(secondary, true)
		}
	}
	if review, ok := payload["code_review_rate_limit"].(map[string]any); ok {
		if primary, ok := review["primary_window"].(map[string]any); ok {
			result["code_review"] = normalizeCodexUsageWindow(primary, false)
		}
	}
	return result
}

func normalizeCodexUsageWindow(window map[string]any, includeType bool) map[string]any {
	used := numericMapOr(window, "used_percent", 0)
	out := map[string]any{"used_percent": used, "remaining_percent": 100 - used, "reset_at": numericMap(window, "reset_at"), "reset_after_seconds": numericMap(window, "reset_after_seconds")}
	if includeType {
		seconds := int(numericMapOr(window, "limit_window_seconds", 0))
		windowType := "unknown"
		if seconds == 18000 {
			windowType = "5h"
		} else if seconds == 604800 {
			windowType = "weekly"
		}
		out["window_type"] = windowType
	}
	return out
}

func (h *Handler) codexImport(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Credentials []map[string]any `json:"credentials"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	entries, _ := loadCodexCredentials()
	existing := map[string]bool{}
	for _, entry := range entries {
		if rt := stringMap(entry, "refreshToken", ""); rt != "" {
			existing[sha256String(rt)] = true
		}
	}
	added, duplicates, skipped := 0, 0, 0
	for _, cred := range body.Credentials {
		rt := stringMap(cred, "refreshToken", "")
		if rt == "" {
			skipped++
			continue
		}
		hash := sha256String(rt)
		if existing[hash] {
			duplicates++
			continue
		}
		existing[hash] = true
		entry := map[string]any{
			"id":           id.Must(8),
			"refreshToken": rt,
			"priority":     numericMapOr(cred, "priority", float64(len(entries))),
			"disabled":     false,
		}
		if displayName := stringMap(cred, "displayName", ""); displayName != "" {
			entry["displayName"] = displayName
		}
		entries = append(entries, entry)
		added++
	}
	if added > 0 {
		if err := saveCodexCredentials(entries); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"added": added, "duplicates": duplicates, "skipped": skipped})
	return nil
}

func (h *Handler) codexAuthBrowser(w http.ResponseWriter, r *http.Request) error {
	authorizeURL, err := h.startCodexBrowserOAuth()
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"authorizeUrl": authorizeURL})
	return nil
}

func (h *Handler) codexAuthBrowserWait(w http.ResponseWriter, r *http.Request) error {
	authorizeURL, err := h.startCodexBrowserOAuth()
	if err != nil {
		return err
	}
	time.Sleep(time.Second)
	codexAuthMu.Lock()
	success := codexBrowser == nil || codexBrowser.Completed
	codexAuthMu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"authorizeUrl": authorizeURL, "success": success, "message": "Authorization started"})
	return nil
}

func (h *Handler) codexAuthCancel(w http.ResponseWriter, r *http.Request) error {
	h.stopCodexBrowserOAuth("Login cancelled")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) startCodexBrowserOAuth() (string, error) {
	h.stopCodexBrowserOAuth("Superseded by a new browser OAuth flow")
	verifier := randomBase64URL(96)
	challenge := codeChallenge(verifier)
	state := randomHex(16)
	redirectURI := "http://localhost:1455/auth/callback"
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return "", httpx.Error(http.StatusConflict, "PORT_IN_USE", "Codex callback port 1455 is unavailable: "+err.Error())
	}
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" {
			http.NotFound(w, r)
			return
		}
		h.handleCodexBrowserCallback(w, r)
	})
	codexAuthMu.Lock()
	codexBrowser = &codexBrowserFlow{State: state, CodeVerifier: verifier, RedirectURI: redirectURI, Server: server, ExpiresAt: time.Now().Add(5 * time.Minute)}
	codexAuthMu.Unlock()
	go func() { _ = server.Serve(listener) }()
	go func() {
		time.Sleep(5 * time.Minute)
		codexAuthMu.Lock()
		expired := codexBrowser != nil && codexBrowser.Server == server && !codexBrowser.Completed
		codexAuthMu.Unlock()
		if expired {
			h.stopCodexBrowserOAuth("OAuth callback timeout")
		}
	}()
	params := url.Values{}
	params.Set("client_id", codexClientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", "openid email profile offline_access")
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("prompt", "login")
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	return codexIssuer + "/oauth/authorize?" + params.Encode(), nil
}

func (h *Handler) handleCodexBrowserCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		h.finishCodexBrowser(false, errMsg)
		writeOAuthHTML(w, false, errMsg)
		return
	}
	codexAuthMu.Lock()
	flow := codexBrowser
	valid := flow != nil && flow.State == state && time.Now().Before(flow.ExpiresAt)
	codexAuthMu.Unlock()
	if code == "" || !valid {
		errMsg := "Invalid or expired OAuth callback"
		h.finishCodexBrowser(false, errMsg)
		writeOAuthHTML(w, false, errMsg)
		return
	}
	entry, err := exchangeCodexAuthCode(r.Context(), code, flow.RedirectURI, flow.CodeVerifier)
	if err != nil {
		h.finishCodexBrowser(false, err.Error())
		writeOAuthHTML(w, false, err.Error())
		return
	}
	if err := addCodexCredential(entry); err != nil {
		h.finishCodexBrowser(false, err.Error())
		writeOAuthHTML(w, false, err.Error())
		return
	}
	h.finishCodexBrowser(true, "")
	writeOAuthHTML(w, true, "")
	go h.stopCodexBrowserOAuth("")
}

func (h *Handler) finishCodexBrowser(success bool, errMsg string) {
	codexAuthMu.Lock()
	if codexBrowser != nil {
		codexBrowser.Completed = success
		codexBrowser.Err = errMsg
	}
	codexAuthMu.Unlock()
}

func (h *Handler) stopCodexBrowserOAuth(errMsg string) {
	codexAuthMu.Lock()
	flow := codexBrowser
	codexBrowser = nil
	codexAuthMu.Unlock()
	if flow != nil && flow.Server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = flow.Server.Shutdown(ctx)
	}
}

func (h *Handler) codexDeviceStart(w http.ResponseWriter, r *http.Request) error {
	info, interval, err := startCodexDeviceCode(r.Context())
	if err != nil {
		return err
	}
	codexAuthMu.Lock()
	if codexDevice != nil && codexDevice.Cancel != nil {
		codexDevice.Cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	codexDevice = &codexDeviceFlow{DeviceAuthID: stringMap(info, "deviceAuthId", ""), UserCode: stringMap(info, "userCode", ""), VerificationURL: stringMap(info, "verificationUrl", ""), Interval: interval, Cancel: cancel, StartedAt: time.Now()}
	codexAuthMu.Unlock()
	go h.pollCodexDevice(ctx, stringMap(info, "deviceAuthId", ""), stringMap(info, "userCode", ""), interval)
	httpx.WriteJSON(w, http.StatusOK, info)
	return nil
}

func (h *Handler) codexDevicePoll(w http.ResponseWriter, r *http.Request) error {
	codexAuthMu.Lock()
	flow := codexDevice
	if flow == nil {
		codexAuthMu.Unlock()
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"pending": false})
		return nil
	}
	if flow.Err != "" {
		errMsg := flow.Err
		codexDevice = nil
		codexAuthMu.Unlock()
		return httpx.BadRequest(errMsg)
	}
	if flow.Completed {
		codexDevice = nil
		codexAuthMu.Unlock()
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"pending": false})
		return nil
	}
	payload := map[string]any{"pending": true, "userCode": flow.UserCode, "verificationUrl": flow.VerificationURL}
	codexAuthMu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) codexDeviceCancel(w http.ResponseWriter, r *http.Request) error {
	codexAuthMu.Lock()
	if codexDevice != nil && codexDevice.Cancel != nil {
		codexDevice.Cancel()
	}
	codexDevice = nil
	codexAuthMu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) pollCodexDevice(ctx context.Context, deviceAuthID, userCode string, interval time.Duration) {
	for {
		entry, done, err := pollCodexDeviceOnce(ctx, deviceAuthID, userCode)
		if err != nil {
			codexAuthMu.Lock()
			if codexDevice != nil && codexDevice.DeviceAuthID == deviceAuthID {
				codexDevice.Err = err.Error()
			}
			codexAuthMu.Unlock()
			return
		}
		if done {
			if err := addCodexCredential(entry); err != nil {
				codexAuthMu.Lock()
				if codexDevice != nil && codexDevice.DeviceAuthID == deviceAuthID {
					codexDevice.Err = err.Error()
				}
				codexAuthMu.Unlock()
				return
			}
			codexAuthMu.Lock()
			if codexDevice != nil && codexDevice.DeviceAuthID == deviceAuthID {
				codexDevice.Completed = true
			}
			codexAuthMu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval + 3*time.Second):
		}
	}
}

func startCodexDeviceCode(ctx context.Context) (map[string]any, time.Duration, error) {
	payload, _ := json.Marshal(map[string]any{"client_id": codexClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexIssuer+"/api/accounts/deviceauth/usercode", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "narrafork/1.0")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", "Failed to initiate device authorization: "+resp.Status+" "+string(body))
	}
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, 0, err
	}
	intervalSeconds := int(numericMapOr(data, "interval", 5))
	if intervalSeconds < 1 {
		intervalSeconds = 5
	}
	info := map[string]any{"deviceAuthId": stringMap(data, "device_auth_id", ""), "userCode": stringMap(data, "user_code", ""), "verificationUrl": codexIssuer + "/codex/device"}
	return info, time.Duration(intervalSeconds) * time.Second, nil
}

func pollCodexDeviceOnce(ctx context.Context, deviceAuthID, userCode string) (map[string]any, bool, error) {
	payload, _ := json.Marshal(map[string]any{"device_auth_id": deviceAuthID, "user_code": userCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexIssuer+"/api/accounts/deviceauth/token", bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "narrafork/1.0")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, errorsNew("Device code authorization failed: " + resp.Status + " " + string(body))
	}
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, false, err
	}
	entry, err := exchangeCodexAuthCode(ctx, stringMap(data, "authorization_code", ""), codexIssuer+"/deviceauth/callback", stringMap(data, "code_verifier", ""))
	if err != nil {
		return nil, false, err
	}
	return entry, true, nil
}

func exchangeCodexAuthCode(ctx context.Context, code, redirectURI, verifier string) (map[string]any, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", codexClientID)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errorsNew("Token exchange failed: " + resp.Status + " " + string(body))
	}
	tokens := map[string]any{}
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, err
	}
	return codexCredentialFromTokenResponse(tokens), nil
}

func codexCredentialFromTokenResponse(tokens map[string]any) map[string]any {
	idToken := stringMap(tokens, "id_token", "")
	claims := parseJWTClaims(idToken)
	expiresIn := numericMapOr(tokens, "expires_in", 3600)
	entry := map[string]any{
		"id":           id.Must(8),
		"displayName":  firstNonEmpty(stringMap(claims, "email", ""), firstNonEmpty(stringMap(claims, "chatgpt_account_id", ""), "Codex")),
		"refreshToken": stringMap(tokens, "refresh_token", ""),
		"accessToken":  stringMap(tokens, "access_token", ""),
		"expiresAt":    float64(time.Now().UnixMilli()) + expiresIn*1000,
		"priority":     float64(0),
		"disabled":     false,
	}
	if accountID := codexAccountID(claims); accountID != "" {
		entry["accountId"] = accountID
	}
	if email := stringMap(claims, "email", ""); email != "" {
		entry["email"] = email
		entry["displayName"] = email
	}
	if sub := stringMap(claims, "sub", ""); sub != "" {
		entry["sub"] = sub
	}
	return entry
}

func codexAccountID(claims map[string]any) string {
	if accountID := stringMap(claims, "chatgpt_account_id", ""); accountID != "" {
		return accountID
	}
	if nested, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		return stringMap(nested, "chatgpt_account_id", "")
	}
	if orgs, ok := claims["organizations"].([]any); ok && len(orgs) > 0 {
		if org, ok := orgs[0].(map[string]any); ok {
			return stringMap(org, "id", "")
		}
	}
	return ""
}

func addCodexCredential(entry map[string]any) error {
	if stringMap(entry, "refreshToken", "") == "" {
		return errorsNew("No refresh token in Codex token response")
	}
	entries, _ := loadCodexCredentials()
	newHash := sha256String(stringMap(entry, "refreshToken", ""))
	for i, existing := range entries {
		if stringMap(entry, "sub", "") != "" && stringMap(existing, "sub", "") == stringMap(entry, "sub", "") {
			entry["id"] = stringMap(existing, "id", id.Must(8))
			entry["priority"] = numericMapOr(existing, "priority", float64(i))
			entries[i] = entry
			return saveCodexCredentials(entries)
		}
		if sha256String(stringMap(existing, "refreshToken", "")) == newHash {
			entry["id"] = stringMap(existing, "id", id.Must(8))
			entry["priority"] = numericMapOr(existing, "priority", float64(i))
			entries[i] = entry
			return saveCodexCredentials(entries)
		}
	}
	entry["priority"] = float64(len(entries))
	entries = append(entries, entry)
	return saveCodexCredentials(entries)
}

func (h *Handler) codexCredentialEntries() []map[string]any {
	entries, _ := loadCodexCredentials()
	out := make([]map[string]any, 0, len(entries))
	for i, entry := range entries {
		if stringMap(entry, "id", "") == "" {
			entry["id"] = id.Must(8)
		}
		if _, ok := entry["priority"]; !ok {
			entry["priority"] = float64(i)
		}
		if _, ok := entry["disabled"]; !ok {
			entry["disabled"] = false
		}
		out = append(out, map[string]any{
			"id":             stringMap(entry, "id", ""),
			"displayName":    entry["displayName"],
			"accountId":      entry["accountId"],
			"email":          entry["email"],
			"priority":       numericMapOr(entry, "priority", float64(i)),
			"disabled":       boolMap(entry, "disabled"),
			"disabledReason": entry["disabledReason"],
			"successCount":   0,
			"failureCount":   0,
			"expiresAt":      entry["expiresAt"],
			"quotaResetsAt":  entry["quotaResetsAt"],
			"usage":          entry["usage"],
		})
	}
	return out
}

func (h *Handler) codexQuotaOverview(w http.ResponseWriter, r *http.Request) error {
	entries := h.codexCredentialEntries()
	overview, _, _, _ := buildCodexUsageArtifacts(entries)
	httpx.WriteJSON(w, http.StatusOK, overview)
	return nil
}

func buildCodexUsageArtifacts(entries []map[string]any) (map[string]any, map[string]any, map[string]any, map[string]any) {
	now := time.Now()
	tiers := []string{"free", "plus", "team", "prolite", "pro", "other"}
	displayTiers := []string{"free", "plus", "team", "prolite", "pro"}
	byTier := map[string]map[string]any{}
	remainingSums := map[string]float64{}
	remainingCounts := map[string]int{}
	for _, tier := range tiers {
		byTier[tier] = map[string]any{
			"tier":                        tier,
			"accountCount":                0,
			"knownUsageCount":             0,
			"zeroUsageCount":              0,
			"scheduledAccountCount":       0,
			"remainingAccountEquivalents": 0.0,
			"averageRemainingPercent":     nil,
		}
	}
	totalTracked := 0
	totalKnown := 0
	zeroUsage := 0
	scheduledCount := 0
	var nextResetAt any
	usageCache := map[string]any{}

	for _, entry := range entries {
		if !isCodexQuotaTracked(entry) {
			continue
		}
		totalTracked++
		usage, _ := entry["usage"].(map[string]any)
		tier := codexPlanTier("")
		if usage != nil {
			tier = codexPlanTier(stringMap(usage, "plan_type", ""))
		}
		stats := byTier[tier]
		stats["accountCount"] = int(numericMapOr(stats, "accountCount", 0)) + 1
		if usage == nil {
			continue
		}
		if entryID := stringMap(entry, "id", ""); entryID != "" {
			usageCache[entryID] = usage
		}
		totalKnown++
		stats["knownUsageCount"] = int(numericMapOr(stats, "knownUsageCount", 0)) + 1
		if remaining, ok := codexModeledRemainingPercent(usage); ok {
			remainingEq := roundFloat(remaining/100, 4)
			stats["remainingAccountEquivalents"] = roundFloat(numericMapOr(stats, "remainingAccountEquivalents", 0)+remainingEq, 4)
			remainingSums[tier] += remaining
			remainingCounts[tier]++
		}
		if codexZeroUsage(usage) {
			zeroUsage++
			stats["zeroUsageCount"] = int(numericMapOr(stats, "zeroUsageCount", 0)) + 1
		}
		if resetAt, ok := codexNextResetAt(usage, now); ok {
			scheduledCount++
			stats["scheduledAccountCount"] = int(numericMapOr(stats, "scheduledAccountCount", 0)) + 1
			currentTierReset, hasTierReset := stats["nextResetAt"].(float64)
			resetFloat := float64(resetAt)
			if !hasTierReset || resetFloat < currentTierReset {
				stats["nextResetAt"] = resetFloat
			}
			if nextResetAt == nil || resetFloat < numericAny(nextResetAt, resetFloat) {
				nextResetAt = resetFloat
			}
		}
	}

	segments := []any{}
	visibleTiers := []any{}
	trendByType := map[string]any{}
	totalRemaining := 0.0
	totalAccounts := 0
	for _, tier := range tiers {
		stats := byTier[tier]
		if count := remainingCounts[tier]; count > 0 {
			stats["averageRemainingPercent"] = roundFloat(remainingSums[tier]/float64(count), 2)
		}
	}
	for _, tier := range displayTiers {
		stats := byTier[tier]
		accountCount := int(numericMapOr(stats, "accountCount", 0))
		if accountCount <= 0 {
			continue
		}
		remainingEq := roundFloat(numericMapOr(stats, "remainingAccountEquivalents", 0), 4)
		totalRemaining += remainingEq
		totalAccounts += accountCount
		visibleTiers = append(visibleTiers, tier)
		trendByType[tier] = remainingEq
		segments = append(segments, map[string]any{
			"type":                        tier,
			"remainingAccountEquivalents": remainingEq,
			"totalAccountEquivalents":     accountCount,
			"averageRemainingPercent":     stats["averageRemainingPercent"],
			"nextResetAt":                 firstNonNil(stats["nextResetAt"], nil),
		})
	}

	points := []any{}
	if len(visibleTiers) > 0 {
		nowMs := now.UnixMilli()
		points = append(points, map[string]any{"timestamp": nowMs - int64(time.Minute/time.Millisecond), "byType": trendByType})
		points = append(points, map[string]any{"timestamp": nowMs, "byType": trendByType})
	}
	forecast := map[string]any{"generatedAt": now.Format(time.RFC3339), "points": points, "tiers": visibleTiers, "unit": "account_equivalent"}
	summary := map[string]any{
		"generatedAt":             now.Format(time.RFC3339),
		"totalTrackedAccounts":    totalTracked,
		"totalKnownUsageAccounts": totalKnown,
		"missingUsageAccounts":    maxInt(0, totalTracked-totalKnown),
		"zeroUsageAccounts":       zeroUsage,
		"scheduledAccountCount":   scheduledCount,
		"nextResetAt":             firstNonNil(nextResetAt, nil),
		"byTier":                  byTier,
	}
	overview := map[string]any{
		"generatedAt":                      now.Format(time.RFC3339),
		"unit":                             "account_equivalent",
		"totalRemainingAccountEquivalents": roundFloat(totalRemaining, 4),
		"totalAccountEquivalents":          totalAccounts,
		"segments":                         segments,
		"trend":                            map[string]any{"generatedAt": now.Format(time.RFC3339), "points": points, "types": visibleTiers},
		"nextResetAt":                      firstNonNil(nextResetAt, nil),
		"usageQueueRunning":                false,
		"schedulerStarted":                 false,
	}
	return overview, summary, forecast, usageCache
}

func isCodexQuotaTracked(entry map[string]any) bool {
	reason := stringMap(entry, "disabledReason", "")
	return reason != "manual" && reason != "too_many_failures" && reason != "banned"
}

func codexPlanTier(planType string) string {
	normalized := strings.ToLower(planType)
	replacer := strings.NewReplacer("-", "", "_", "", " ", "")
	normalized = replacer.Replace(normalized)
	switch {
	case strings.Contains(normalized, "prolite") || strings.Contains(normalized, "litepro"):
		return "prolite"
	case strings.Contains(normalized, "plus"):
		return "plus"
	case strings.Contains(normalized, "team") || strings.Contains(normalized, "business"):
		return "team"
	case strings.Contains(normalized, "free"):
		return "free"
	case normalized == "pro" || strings.HasSuffix(normalized, "pro") || strings.Contains(normalized, "chatgptpro"):
		return "pro"
	default:
		return "other"
	}
}

func codexUsageWindow(usage map[string]any, windowType string) (map[string]any, bool) {
	for _, key := range []string{"primary_window", "secondary_window"} {
		window, ok := usage[key].(map[string]any)
		if !ok {
			continue
		}
		if stringMap(window, "window_type", "") == windowType {
			return window, true
		}
	}
	return nil, false
}

func codexWindowRemaining(window map[string]any) float64 {
	if _, ok := window["remaining_percent"]; ok {
		return clampFloat(numericMapOr(window, "remaining_percent", 0), 0, 100)
	}
	return clampFloat(100-numericMapOr(window, "used_percent", 0), 0, 100)
}

func codexWindowExhausted(window map[string]any) bool {
	return codexWindowRemaining(window) <= 0 || numericMapOr(window, "used_percent", 0) >= 100
}

func codexModeledRemainingPercent(usage map[string]any) (float64, bool) {
	if shortTerm, ok := codexUsageWindow(usage, "5h"); ok {
		if weekly, ok := codexUsageWindow(usage, "weekly"); ok && codexWindowExhausted(weekly) {
			return 0, true
		}
		return codexWindowRemaining(shortTerm), true
	}
	if weekly, ok := codexUsageWindow(usage, "weekly"); ok {
		return codexWindowRemaining(weekly), true
	}
	return 0, false
}

func codexZeroUsage(usage map[string]any) bool {
	if shortTerm, ok := codexUsageWindow(usage, "5h"); ok {
		if weekly, ok := codexUsageWindow(usage, "weekly"); ok && codexWindowExhausted(weekly) {
			return false
		}
		return numericMapOr(shortTerm, "used_percent", 0) <= 0
	}
	if weekly, ok := codexUsageWindow(usage, "weekly"); ok {
		return numericMapOr(weekly, "used_percent", 0) <= 0
	}
	return false
}

func codexNextResetAt(usage map[string]any, now time.Time) (int64, bool) {
	if shortTerm, ok := codexUsageWindow(usage, "5h"); ok {
		if weekly, ok := codexUsageWindow(usage, "weekly"); ok && codexWindowExhausted(weekly) {
			return unixSecondsToMillis(numericMapOr(weekly, "reset_at", 0), now)
		}
		if numericMapOr(shortTerm, "used_percent", 0) > 0 {
			return unixSecondsToMillis(numericMapOr(shortTerm, "reset_at", 0), now)
		}
	}
	if weekly, ok := codexUsageWindow(usage, "weekly"); ok && numericMapOr(weekly, "used_percent", 0) > 0 {
		return unixSecondsToMillis(numericMapOr(weekly, "reset_at", 0), now)
	}
	return 0, false
}

func unixSecondsToMillis(value float64, now time.Time) (int64, bool) {
	if value <= 0 {
		return 0, false
	}
	millis := int64(value)
	if millis < 1000000000000 {
		millis *= 1000
	}
	if millis <= now.UnixMilli() {
		return 0, false
	}
	return millis, true
}

func roundFloat(value float64, places int) float64 {
	factor := math.Pow(10, float64(places))
	return math.Round(value*factor) / factor
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func numericAny(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return fallback
}

func firstNonNil(value any, fallback any) any {
	if value != nil {
		return value
	}
	return fallback
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (h *Handler) loadKiroCredentials() ([]map[string]any, error) {
	path, err := h.kiroCredentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{}, nil
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return []map[string]any{}, nil
	}
	entries := []map[string]any{}
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, err
		}
	} else {
		entry := map[string]any{}
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	for i, entry := range entries {
		if stringMap(entry, "id", "") == "" {
			entry["id"] = id.Must(8)
		}
		if _, ok := entry["priority"]; !ok {
			entry["priority"] = i
		}
		if stringMap(entry, "authMethod", "") == "builder-id" || stringMap(entry, "authMethod", "") == "iam" {
			entry["authMethod"] = "idc"
		}
	}
	return entries, nil
}

func (h *Handler) saveKiroCredentials(entries []map[string]any) error {
	path, err := h.kiroCredentialsPath()
	if err != nil {
		return err
	}
	return writeJSONFile(path, entries)
}

func (h *Handler) kiroCredentialsPath() (string, error) {
	if h.Settings != nil {
		if kiro, ok := h.Settings.Raw()["kiro"].(map[string]any); ok {
			if path := strings.TrimSpace(stringMap(kiro, "credentialsPath", "")); path != "" {
				return path, nil
			}
		}
	}
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kiro", "credentials.json"), nil
}

func (h *Handler) kiroConfigPath() (string, error) {
	if h.Settings != nil {
		if kiro, ok := h.Settings.Raw()["kiro"].(map[string]any); ok {
			if path := strings.TrimSpace(stringMap(kiro, "configPath", "")); path != "" {
				return path, nil
			}
		}
	}
	credsPath, err := h.kiroCredentialsPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(credsPath), "config.json"), nil
}

func (h *Handler) ensureKiroPaths() error {
	credsPath, err := h.kiroCredentialsPath()
	if err != nil {
		return err
	}
	configPath, err := h.kiroConfigPath()
	if err != nil {
		return err
	}
	if h.Settings != nil {
		_, err = h.Settings.Patch(map[string]any{"kiro": map[string]any{"credentialsPath": credsPath, "configPath": configPath}})
		return err
	}
	return nil
}

func (h *Handler) loadKiroConfig() map[string]any {
	path, err := h.kiroConfigPath()
	if err != nil {
		return kiroDefaultConfig()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return kiroDefaultConfig()
	}
	config := kiroDefaultConfig()
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return config
	}
	for k, v := range parsed {
		config[k] = v
	}
	return config
}

func (h *Handler) saveKiroConfig(config map[string]any) error {
	path, err := h.kiroConfigPath()
	if err != nil {
		return err
	}
	return writeJSONFile(path, config)
}

func (h *Handler) loadKiroUsageCache() map[string]any {
	credsPath, err := h.kiroCredentialsPath()
	if err != nil {
		return map[string]any{}
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(credsPath), "kiro_usage.json"))
	if err != nil {
		return map[string]any{}
	}
	cache := map[string]any{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return map[string]any{}
	}
	return cache
}

func (h *Handler) patchKiroSetting(key string, value any) error {
	if h.Settings == nil {
		return nil
	}
	_, err := h.Settings.Patch(map[string]any{"kiro": map[string]any{key: value}})
	return err
}

func (h *Handler) kiroSettingValue(key string, fallback any) any {
	if h.Settings == nil {
		return fallback
	}
	if kiro, ok := h.Settings.Raw()["kiro"].(map[string]any); ok {
		if v, ok := kiro[key]; ok {
			return v
		}
	}
	return fallback
}

func kiroDefaultConfig() map[string]any {
	return map[string]any{"region": "us-east-1", "kiroVersion": "1.26.1", "systemVersion": runtime.GOOS, "nodeVersion": runtime.Version()}
}

func kiroCredentialSnapshots(entries []map[string]any) []any {
	out := make([]any, 0, len(entries))
	for i, entry := range entries {
		priority := int(numericMapOr(entry, "priority", float64(i)))
		out = append(out, map[string]any{
			"id":                stringMap(entry, "id", ""),
			"priority":          priority,
			"disabled":          boolMap(entry, "disabled"),
			"disabledReason":    stringMap(entry, "disabledReason", ""),
			"failureCount":      int(numericMapOr(entry, "failureCount", 0)),
			"successCount":      int(numericMapOr(entry, "successCount", 0)),
			"lastUsedAt":        entry["lastUsedAt"],
			"email":             stringMap(entry, "email", ""),
			"displayName":       stringMap(entry, "displayName", ""),
			"hasProfileArn":     stringMap(entry, "profileArn", "") != "" || stringMap(entry, "ssoProfile", "") != "",
			"expiresAt":         entry["expiresAt"],
			"authMethod":        stringMap(entry, "authMethod", ""),
			"subscriptionTitle": stringMap(entry, "subscriptionTitle", ""),
			"region":            stringMap(entry, "region", ""),
			"meterUsage":        numericMap(entry, "meterUsage"),
			"availableModels":   entry["availableModels"],
		})
	}
	return out
}

func kiroCredentialKey(entry map[string]any) string {
	if token := strings.TrimSpace(stringMap(entry, "refreshToken", "")); token != "" {
		return "refresh:" + token
	}
	clientID := strings.TrimSpace(stringMap(entry, "clientId", ""))
	clientSecret := strings.TrimSpace(stringMap(entry, "clientSecret", ""))
	if clientID != "" || clientSecret != "" {
		return "client:" + clientID + ":" + clientSecret
	}
	if email := strings.TrimSpace(stringMap(entry, "email", "")); email != "" {
		return "email:" + strings.ToLower(email)
	}
	return ""
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func loadClineCredentials() (map[string]any, error) {
	path, err := clineCredentialsPath()
	if err != nil {
		return map[string]any{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}, err
	}
	creds := map[string]any{}
	if err := json.Unmarshal(data, &creds); err != nil {
		return map[string]any{}, err
	}
	return creds, nil
}

func loadCodexCredentials() ([]map[string]any, error) {
	path, err := codexCredentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{}, nil
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		return []map[string]any{}, err
	}
	return entries, nil
}

func saveCodexCredentials(entries []map[string]any) error {
	return writeJSONFileMustDir(codexCredentialsPath, entries)
}

func clineCredentialsPath() (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cline-credentials.json"), nil
}

func codexCredentialsPath() (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "codex-credentials.json"), nil
}

func narraforkDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".narrafork"), nil
}

func writeJSONFileMustDir(pathFn func() (string, error), value any) error {
	path, err := pathFn()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func setClineHeaders(req *http.Request, accessToken string) {
	req.Header.Set("User-Agent", "Cline/3.74.0")
	req.Header.Set("X-PLATFORM", "node")
	req.Header.Set("X-PLATFORM-VERSION", runtime.Version())
	req.Header.Set("X-CLIENT-TYPE", "extension")
	req.Header.Set("X-CLIENT-VERSION", "3.74.0")
	req.Header.Set("X-CORE-VERSION", "3.74.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		if !strings.HasPrefix(accessToken, "workos:") {
			accessToken = "workos:" + accessToken
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
}

func decodeFlexibleBase64(value string) (string, error) {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if decoded, err := enc.DecodeString(value); err == nil {
			return string(decoded), nil
		}
	}
	return "", errorsNew("invalid base64")
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}
	}
	decoded, err := decodeFlexibleBase64(parts[1])
	if err != nil {
		return map[string]any{}
	}
	claims := map[string]any{}
	_ = json.Unmarshal([]byte(decoded), &claims)
	return claims
}

func randomBase64URL(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func numericMap(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	return nil
}

func numericMapOr(m map[string]any, key string, fallback float64) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func boolMap(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func writeOAuthHTML(w http.ResponseWriter, success bool, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !success {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<!doctype html><html><body><h1>Authorization Failed</h1><p>" + message + "</p></body></html>"))
		return
	}
	_, _ = w.Write([]byte("<!doctype html><html><body><h1>Authorization Successful</h1><p>You can close this window and return to NarraFork.</p><script>setTimeout(()=>window.close(),2000)</script></body></html>"))
}

func (h *Handler) scanRuntime(w http.ResponseWriter, r *http.Request) error {
	var running, exited int
	terminalSupported := false
	terminalDegraded := false
	terminalFallbacks := []any{}
	terminalError := ""
	if h.DB != nil {
		if ok, err := h.systemTableExists(r.Context(), "terminals"); err != nil {
			terminalDegraded = true
			terminalFallbacks = append(terminalFallbacks, "terminal_table_check_failed")
			terminalError = err.Error()
		} else if ok {
			terminalSupported = true
			if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM terminals WHERE status = 'running'`).Scan(&running); err != nil {
				terminalDegraded = true
				terminalFallbacks = append(terminalFallbacks, "terminal_running_count_failed")
				terminalError = err.Error()
			}
			if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM terminals WHERE status != 'running'`).Scan(&exited); err != nil {
				terminalDegraded = true
				terminalFallbacks = append(terminalFallbacks, "terminal_exited_count_failed")
				if terminalError == "" {
					terminalError = err.Error()
				}
			}
		}
	}
	orphanSockets := 0
	if orphanRuntime, ok := h.TerminalRuntime.(orphanTerminalRuntime); ok {
		orphanSockets = len(orphanRuntime.OrphanIDs(r.Context()))
		terminalSupported = true
	}
	terminalPayload := map[string]any{"running": running, "exited": exited, "orphanSockets": orphanSockets, "supported": terminalSupported, "degraded": terminalDegraded}
	if terminalDegraded {
		terminalPayload["fallback"] = true
		terminalPayload["fallbacks"] = terminalFallbacks
		terminalPayload["error"] = terminalError
	}
	if !terminalSupported {
		terminalPayload["fallback"] = true
		terminalPayload["message"] = "terminal runtime state table is not available"
	}
	containersRunning, containersStopped, podmanAvailable := podmanCounts(r.Context())
	containerPayload := map[string]any{"running": containersRunning, "stopped": containersStopped, "podmanAvailable": podmanAvailable, "supported": podmanAvailable, "fallback": !podmanAvailable}
	if !podmanAvailable {
		containerPayload["message"] = "podman is not available; runtime scan uses persisted/non-container state only"
	}
	browserPayload := h.browserRuntimePayload(r.Context())
	result := map[string]any{
		"terminals":  terminalPayload,
		"containers": containerPayload,
		"browsers":   browserPayload,
		"scannedAt":  time.Now().UnixMilli(),
	}
	runtimeCache = result
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) browserRuntimePayload(ctx context.Context) map[string]any {
	ids := map[string]bool{}
	for _, id := range agentbasic.ActiveBrowserSessionIDs() {
		if strings.TrimSpace(id) != "" {
			ids[id] = true
		}
	}
	memoryCount := len(ids)
	dbTableAvailable := false
	dbError := ""
	if h.DB != nil {
		if ok, err := h.systemTableExists(ctx, "narrator_browser_sessions"); err != nil {
			dbError = err.Error()
		} else if ok {
			dbTableAvailable = true
			rows, err := h.DB.QueryContext(ctx, `SELECT id FROM narrator_browser_sessions WHERE status IN ('active', 'running', 'idle') ORDER BY id ASC LIMIT 1000`)
			if err != nil {
				dbError = err.Error()
			} else {
				for rows.Next() {
					var id string
					if err := rows.Scan(&id); err != nil {
						dbError = err.Error()
						continue
					}
					if strings.TrimSpace(id) != "" {
						ids[id] = true
					}
				}
				if err := rows.Close(); err != nil && dbError == "" {
					dbError = err.Error()
				}
			}
		}
	}
	payload := map[string]any{
		"processRunning":      memoryCount > 0,
		"connected":           memoryCount > 0,
		"activeSessions":      len(ids),
		"memorySessions":      memoryCount,
		"persistedSessions":   len(ids) - memoryCount,
		"supported":           true,
		"fallback":            false,
		"runtime":             "go-rod",
		"dbTableAvailable":    dbTableAvailable,
		"requiresChrome":      true,
		"cleanupSupported":    true,
		"sessionApiSupported": true,
	}
	if dbError != "" {
		payload["dbError"] = dbError
	}
	return payload
}

func (h *Handler) cachedRuntime(w http.ResponseWriter, r *http.Request) error {
	if runtimeCache == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"cached": false})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cached": true, "data": runtimeCache})
	return nil
}

const runtimeCleanupItemLimit = 100

var runtimeCleanupTargets = []string{"terminals", "containers", "browsers", "worktrees"}

type runtimeCleanupRequest struct {
	Target  string `json:"target"`
	DryRun  *bool  `json:"dryRun"`
	Execute bool   `json:"execute"`
	Mode    string `json:"mode"`
}

type runtimeCleanupError struct {
	Target string `json:"target"`
	ID     string `json:"id,omitempty"`
	Error  string `json:"error"`
}

type runtimeCommandOutput struct {
	Target    string `json:"target"`
	ID        string `json:"id,omitempty"`
	Command   string `json:"command"`
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type runtimeCleanupResult struct {
	OK             bool                   `json:"ok"`
	Target         string                 `json:"target"`
	DryRun         bool                   `json:"dryRun"`
	Removed        map[string][]string    `json:"removed"`
	RemovedCounts  map[string]int         `json:"removedCounts"`
	Errors         []runtimeCleanupError  `json:"errors"`
	ErrorCount     int                    `json:"errorCount"`
	Outputs        []runtimeCommandOutput `json:"outputs,omitempty"`
	OutputCount    int                    `json:"outputCount"`
	Truncated      map[string]bool        `json:"truncated,omitempty"`
	Supported      map[string]bool        `json:"supported,omitempty"`
	Fallback       map[string]bool        `json:"fallback,omitempty"`
	Killed         *int                   `json:"killed,omitempty"`
	Stopped        *int                   `json:"stopped,omitempty"`
	BrowserClosed  *bool                  `json:"browserClosed,omitempty"`
	ClosedSessions *int                   `json:"closedSessions,omitempty"`
	ScannedAt      int64                  `json:"scannedAt"`
	ExecutedAt     *int64                 `json:"executedAt,omitempty"`
}

func newRuntimeCleanupResult(target string, dryRun bool) *runtimeCleanupResult {
	result := &runtimeCleanupResult{
		OK:            true,
		Target:        target,
		DryRun:        dryRun,
		Removed:       map[string][]string{},
		RemovedCounts: map[string]int{},
		Truncated:     map[string]bool{},
		Supported:     map[string]bool{},
		Fallback:      map[string]bool{},
		ScannedAt:     time.Now().UnixMilli(),
	}
	for _, scope := range runtimeCleanupTargets {
		result.Removed[scope] = []string{}
		result.Supported[scope] = true
		result.Fallback[scope] = false
	}
	if !dryRun {
		now := time.Now().UnixMilli()
		result.ExecutedAt = &now
	}
	return result
}

func (r *runtimeCleanupResult) addRemoved(scope, id string) {
	r.RemovedCounts[scope]++
	if len(r.Removed[scope]) < runtimeCleanupItemLimit {
		r.Removed[scope] = append(r.Removed[scope], id)
		return
	}
	r.Truncated[scope] = true
}

func (r *runtimeCleanupResult) addError(scope, id string, err error) {
	if err == nil {
		return
	}
	r.OK = false
	r.ErrorCount++
	if len(r.Errors) < runtimeCleanupItemLimit {
		r.Errors = append(r.Errors, runtimeCleanupError{Target: scope, ID: id, Error: err.Error()})
		return
	}
	r.Truncated[scope+"Errors"] = true
}

func (r *runtimeCleanupResult) addOutput(output runtimeCommandOutput) {
	r.OutputCount++
	if len(r.Outputs) < 20 {
		r.Outputs = append(r.Outputs, output)
		return
	}
	r.Truncated["outputs"] = true
}

func (h *Handler) cleanupRuntime(w http.ResponseWriter, r *http.Request) error {
	var body runtimeCleanupRequest
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	target := normalizeRuntimeCleanupTarget(body.Target)
	if target == "" {
		return httpx.BadRequest("invalid runtime cleanup target")
	}
	// Frontend cleanup actions are confirmed destructive actions and historically send
	// only {target}. Treat an omitted dryRun flag as execute=true so the route does
	// not report ok:true for a preview that performed no cleanup.
	dryRun := false
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if body.Execute || mode == "execute" {
		dryRun = false
	}
	if mode == "dry-run" || mode == "dry_run" || mode == "preview" {
		dryRun = true
	}

	result := newRuntimeCleanupResult(target, dryRun)
	targets := []string{target}
	if target == "all" {
		targets = runtimeCleanupTargets
	}
	for _, cleanupTarget := range targets {
		switch cleanupTarget {
		case "terminals":
			h.cleanupRuntimeTerminals(r.Context(), dryRun, result)
		case "containers":
			h.cleanupRuntimeContainers(r.Context(), dryRun, result)
		case "browsers":
			h.cleanupRuntimeBrowsers(r.Context(), dryRun, result)
		case "worktrees":
			h.cleanupRuntimeWorktrees(r.Context(), dryRun, result)
		}
	}
	runtimeCache = nil
	addRuntimeCleanupLegacyFields(result)
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func normalizeRuntimeCleanupTarget(target string) string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", "all":
		return "all"
	case "terminal", "terminals":
		return "terminals"
	case "container", "containers":
		return "containers"
	case "browser", "browsers":
		return "browsers"
	case "worktree", "worktrees":
		return "worktrees"
	default:
		return ""
	}
}

func addRuntimeCleanupLegacyFields(result *runtimeCleanupResult) {
	killed := result.RemovedCounts["terminals"]
	stopped := result.RemovedCounts["containers"]
	browserClosed := result.RemovedCounts["browsers"] > 0
	if result.Target == "terminals" || result.Target == "all" {
		result.Killed = &killed
	}
	if result.Target == "containers" || result.Target == "all" {
		result.Stopped = &stopped
	}
	if result.Target == "browsers" || result.Target == "all" {
		closedSessions := result.RemovedCounts["browsers"]
		result.BrowserClosed = &browserClosed
		result.ClosedSessions = &closedSessions
	}
}

type runtimeTerminalRecord struct {
	ID     string
	Status string
	Orphan bool
}

func (h *Handler) cleanupRuntimeTerminals(ctx context.Context, dryRun bool, result *runtimeCleanupResult) {
	candidates := map[string]runtimeTerminalRecord{}
	if exists, err := h.runtimeTableExists(ctx, "terminals"); err != nil {
		result.addError("terminals", "terminals", err)
	} else if exists {
		rows, err := h.DB.QueryContext(ctx, `SELECT id, COALESCE(status, '') FROM terminals WHERE COALESCE(status, '') IN ('exited', 'orphan') ORDER BY id ASC`)
		if err != nil {
			result.addError("terminals", "terminals", err)
		} else {
			for rows.Next() {
				var id, status string
				if err := rows.Scan(&id, &status); err != nil {
					result.addError("terminals", "terminals", err)
					continue
				}
				id = strings.TrimSpace(id)
				if id != "" {
					candidates[id] = runtimeTerminalRecord{ID: id, Status: status, Orphan: status == "orphan"}
				}
			}
			if err := rows.Close(); err != nil {
				result.addError("terminals", "terminals", err)
			}
		}
	}
	if orphanRuntime, ok := h.TerminalRuntime.(orphanTerminalRuntime); ok {
		for _, id := range orphanRuntime.OrphanIDs(ctx) {
			id = strings.TrimSpace(id)
			if id != "" {
				record := candidates[id]
				record.ID = id
				record.Orphan = true
				candidates[id] = record
			}
		}
	}
	ids := sortedRuntimeKeys(candidates)
	for _, terminalID := range ids {
		record := candidates[terminalID]
		if !dryRun && h.TerminalRuntime != nil {
			if record.Orphan {
				if orphanRuntime, ok := h.TerminalRuntime.(orphanTerminalRuntime); ok {
					if err := orphanRuntime.KillOrphan(ctx, terminalID); err != nil {
						result.addError("terminals", terminalID, err)
						continue
					}
				} else if err := h.TerminalRuntime.Kill(ctx, terminalID); err != nil {
					result.addError("terminals", terminalID, err)
					continue
				}
			} else if err := h.TerminalRuntime.Kill(ctx, terminalID); err != nil {
				result.addError("terminals", terminalID, err)
				continue
			}
		}
		if !dryRun {
			if exists, _ := h.runtimeTableExists(ctx, "terminals"); exists && h.DB != nil {
				_, err := h.DB.ExecContext(ctx, `UPDATE terminals SET status = 'exited', exit_code = COALESCE(exit_code, -1) WHERE id = ?`, terminalID)
				if err != nil {
					result.addError("terminals", terminalID, err)
					continue
				}
			}
		}
		result.addRemoved("terminals", terminalID)
	}
}

type runtimeContainerRecord struct {
	ID          string
	ContainerID string
	Status      string
}

func (h *Handler) cleanupRuntimeContainers(ctx context.Context, dryRun bool, result *runtimeCleanupResult) {
	exists, err := h.runtimeTableExists(ctx, "container_instances")
	if err != nil {
		result.addError("containers", "container_instances", err)
		return
	}
	if !exists {
		return
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id, COALESCE(container_id, ''), COALESCE(status, '') FROM container_instances WHERE COALESCE(status, '') = 'running' ORDER BY id ASC`)
	if err != nil {
		result.addError("containers", "container_instances", err)
		return
	}
	candidates := []runtimeContainerRecord{}
	for rows.Next() {
		var record runtimeContainerRecord
		if err := rows.Scan(&record.ID, &record.ContainerID, &record.Status); err != nil {
			result.addError("containers", "container_instances", err)
			continue
		}
		candidates = append(candidates, record)
	}
	if err := rows.Close(); err != nil {
		result.addError("containers", "container_instances", err)
	}
	if len(candidates) == 0 {
		return
	}
	requiresPodman := false
	for _, record := range candidates {
		if strings.TrimSpace(record.ContainerID) != "" {
			requiresPodman = true
			break
		}
	}
	podmanPath := ""
	if requiresPodman {
		var podmanErr error
		podmanPath, podmanErr = exec.LookPath("podman")
		if podmanErr != nil {
			result.Supported["containers"] = false
			result.Fallback["containers"] = true
			if !dryRun {
				result.addError("containers", "podman", errors.New("PODMAN_NOT_FOUND: podman is not available; container cleanup skipped"))
				return
			}
		}
	}
	for _, record := range candidates {
		removedID := firstRuntimeNonEmpty(record.ContainerID, record.ID)
		if !dryRun && strings.TrimSpace(record.ContainerID) != "" {
			output, err := runRuntimeCommand(ctx, "", 32*1024, podmanPath, "stop", record.ContainerID)
			output.Target = "containers"
			output.ID = removedID
			result.addOutput(output)
			if err != nil {
				result.addError("containers", removedID, err)
				continue
			}
		}
		if !dryRun {
			sets := []string{"status = 'stopped'"}
			args := []any{}
			if ok, _ := h.runtimeColumnExists(ctx, "container_instances", "updated_at"); ok {
				sets = append(sets, "updated_at = ?")
				args = append(args, db.NowISO())
			}
			args = append(args, record.ID)
			if _, err := h.DB.ExecContext(ctx, `UPDATE container_instances SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
				result.addError("containers", removedID, err)
				continue
			}
		}
		result.addRemoved("containers", removedID)
	}
}

func (h *Handler) cleanupRuntimeBrowsers(ctx context.Context, dryRun bool, result *runtimeCleanupResult) {
	ids := map[string]bool{}
	for _, id := range agentbasic.ActiveBrowserSessionIDs() {
		if strings.TrimSpace(id) != "" {
			ids[id] = true
		}
	}
	exists, err := h.runtimeTableExists(ctx, "narrator_browser_sessions")
	if err != nil {
		result.addError("browsers", "narrator_browser_sessions", err)
	} else if exists {
		rows, err := h.DB.QueryContext(ctx, `SELECT id FROM narrator_browser_sessions WHERE status IN ('active', 'running', 'idle') ORDER BY id ASC`)
		if err != nil {
			result.addError("browsers", "narrator_browser_sessions", err)
		} else {
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					result.addError("browsers", "narrator_browser_sessions", err)
					continue
				}
				ids[id] = true
			}
			if err := rows.Close(); err != nil {
				result.addError("browsers", "narrator_browser_sessions", err)
			}
		}
	}
	if !dryRun {
		for _, id := range agentbasic.CloseAllBrowserSessions() {
			ids[id] = true
		}
		if exists && h.DB != nil {
			sets := []string{"status = 'closed'"}
			if ok, _ := h.runtimeColumnExists(ctx, "narrator_browser_sessions", "closed_at"); ok {
				sets = append(sets, "closed_at = COALESCE(closed_at, ?)")
			}
			if ok, _ := h.runtimeColumnExists(ctx, "narrator_browser_sessions", "updated_at"); ok {
				sets = append(sets, "updated_at = ?")
			}
			args := []any{}
			now := db.NowISO()
			for _, set := range sets {
				if strings.Contains(set, "?") {
					args = append(args, now)
				}
			}
			query := `UPDATE narrator_browser_sessions SET ` + strings.Join(sets, ", ") + ` WHERE status IN ('active', 'running', 'idle')`
			if _, err := h.DB.ExecContext(ctx, query, args...); err != nil {
				result.addError("browsers", "narrator_browser_sessions", err)
			}
		}
	}
	for _, id := range sortedBoolKeys(ids) {
		result.addRemoved("browsers", id)
	}
}

type runtimeWorktreeRecord struct {
	ID        string
	ProjectID string
	GitPath   string
	Path      string
	Reason    string
	Chapter   bool
}

func (h *Handler) cleanupRuntimeWorktrees(ctx context.Context, dryRun bool, result *runtimeCleanupResult) {
	if h.DB == nil {
		return
	}
	chaptersExist, err := h.runtimeTableExists(ctx, "chapters")
	if err != nil {
		result.addError("worktrees", "chapters", err)
		return
	}
	projectsExist, err := h.runtimeTableExists(ctx, "projects")
	if err != nil {
		result.addError("worktrees", "projects", err)
		return
	}
	if !chaptersExist || !projectsExist {
		return
	}
	candidates := []runtimeWorktreeRecord{}
	referenced := map[string]bool{}
	rows, err := h.DB.QueryContext(ctx, `SELECT c.id, c.project_id, COALESCE(p.git_path, ''), COALESCE(c.worktree_path, ''), COALESCE(c.status, '') FROM chapters c JOIN projects p ON p.id = c.project_id WHERE COALESCE(c.worktree_path, '') != '' ORDER BY c.id ASC`)
	if err != nil {
		result.addError("worktrees", "chapters", err)
		return
	}
	for rows.Next() {
		var chapterID, projectID, gitPath, worktreePath, status string
		if err := rows.Scan(&chapterID, &projectID, &gitPath, &worktreePath, &status); err != nil {
			result.addError("worktrees", "chapters", err)
			continue
		}
		absPath := cleanAbsPath(worktreePath)
		if absPath != "" {
			referenced[absPath] = true
		}
		if !inactiveRuntimeWorktreeStatus(status) || !safeRuntimeWorktreePath(gitPath, worktreePath) {
			continue
		}
		candidates = append(candidates, runtimeWorktreeRecord{ID: chapterID, ProjectID: projectID, GitPath: gitPath, Path: worktreePath, Reason: "inactiveChapter", Chapter: true})
	}
	if err := rows.Close(); err != nil {
		result.addError("worktrees", "chapters", err)
	}
	projectRows, err := h.DB.QueryContext(ctx, `SELECT id, COALESCE(git_path, '') FROM projects WHERE COALESCE(git_path, '') != '' ORDER BY id ASC`)
	if err != nil {
		result.addError("worktrees", "projects", err)
		return
	}
	for projectRows.Next() {
		var projectID, gitPath string
		if err := projectRows.Scan(&projectID, &gitPath); err != nil {
			result.addError("worktrees", "projects", err)
			continue
		}
		base := filepath.Join(gitPath, ".worktrees")
		entries, err := os.ReadDir(base)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				result.addError("worktrees", base, err)
			}
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(base, entry.Name())
			absPath := cleanAbsPath(path)
			if referenced[absPath] || !safeRuntimeWorktreePath(gitPath, path) {
				continue
			}
			candidates = append(candidates, runtimeWorktreeRecord{ID: projectID + ":" + entry.Name(), ProjectID: projectID, GitPath: gitPath, Path: path, Reason: "orphanDirectory"})
		}
	}
	if err := projectRows.Close(); err != nil {
		result.addError("worktrees", "projects", err)
	}
	for _, record := range candidates {
		removedID := firstRuntimeNonEmpty(record.ID, record.Path)
		if !dryRun {
			output, err := runRuntimeCommand(ctx, record.GitPath, 32*1024, "git", "worktree", "remove", "--force", record.Path)
			output.Target = "worktrees"
			output.ID = removedID
			result.addOutput(output)
			if err != nil {
				result.addError("worktrees", removedID, err)
				continue
			}
			if record.Chapter {
				if _, err := h.DB.ExecContext(ctx, `UPDATE chapters SET worktree_path = NULL WHERE id = ?`, record.ID); err != nil {
					result.addError("worktrees", removedID, err)
					continue
				}
			}
		}
		result.addRemoved("worktrees", removedID)
	}
}

func (h *Handler) runtimeTableExists(ctx context.Context, table string) (bool, error) {
	if h.DB == nil {
		return false, nil
	}
	return h.systemTableExists(ctx, table)
}

func (h *Handler) runtimeColumnExists(ctx context.Context, table, column string) (bool, error) {
	if h.DB == nil {
		return false, nil
	}
	rows, err := h.DB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func sortedRuntimeKeys(records map[string]runtimeTerminalRecord) []string {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstRuntimeNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runRuntimeCommand(ctx context.Context, cwd string, outputLimit int, name string, args ...string) (runtimeCommandOutput, error) {
	if outputLimit <= 0 {
		outputLimit = 32 * 1024
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}
	writer := &runtimeLimitedWriter{limit: outputLimit}
	cmd.Stdout = writer
	cmd.Stderr = writer
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	output := runtimeCommandOutput{Command: command, ExitCode: exitCode, Output: writer.String(), Truncated: writer.truncated}
	return output, err
}

func cleanAbsPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func inactiveRuntimeWorktreeStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "dormant", "merged", "abandoned", "frozen", "closed", "inactive":
		return true
	default:
		return false
	}
}

func safeRuntimeWorktreePath(gitPath, worktreePath string) bool {
	gitPath = strings.TrimSpace(gitPath)
	worktreePath = strings.TrimSpace(worktreePath)
	if gitPath == "" || worktreePath == "" {
		return false
	}
	base := cleanAbsPath(filepath.Join(gitPath, ".worktrees"))
	target := cleanAbsPath(worktreePath)
	if base == "" || target == "" || target == base {
		return false
	}
	rel, err := filepath.Rel(base, target)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type runtimeLimitedWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *runtimeLimitedWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		w.limit = 32 * 1024
	}
	if w.buf.Len() < w.limit {
		remaining := w.limit - w.buf.Len()
		if len(p) <= remaining {
			_, _ = w.buf.Write(p)
		} else {
			_, _ = w.buf.Write(p[:remaining])
			w.truncated = true
		}
	} else {
		w.truncated = true
	}
	return len(p), nil
}

func (w *runtimeLimitedWriter) String() string {
	out := w.buf.String()
	if w.truncated {
		out += "\n[truncated]"
	}
	return out
}

func podmanCounts(ctx context.Context) (int, int, bool) {
	if _, err := exec.LookPath("podman"); err != nil {
		return 0, 0, false
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "podman", "ps", "-a", "--format", "{{.State}}")
	writer := &runtimeLimitedWriter{limit: 32 * 1024}
	cmd.Stdout = writer
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return 0, 0, false
	}
	running, stopped := 0, 0
	for _, line := range strings.Split(writer.String(), "\n") {
		s := strings.TrimSpace(strings.ToLower(line))
		switch s {
		case "running", "up":
			running++
		case "", "created":
		default:
			stopped++
		}
	}
	return running, stopped, true
}

func (h *Handler) diagnostics(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"goRoutines": runtime.NumGoroutine(), "memory": map[string]any{}, "eventLoopLagMs": 0})
	return nil
}

func (h *Handler) codexStatus(w http.ResponseWriter, r *http.Request) {
	entries := h.codexCredentialEntries()
	available := []map[string]any{}
	unavailable := []map[string]any{}
	currentID := ""
	for _, entry := range entries {
		if boolMap(entry, "disabled") {
			unavailable = append(unavailable, entry)
			continue
		}
		if currentID == "" {
			currentID = stringMap(entry, "id", "")
		}
		available = append(available, entry)
	}
	codexAuthMu.Lock()
	lastBrowserAuthError := ""
	if codexBrowser != nil && codexBrowser.Err != "" {
		lastBrowserAuthError = codexBrowser.Err
	}
	codexAuthMu.Unlock()
	_, usageSummary, usageForecast, usageCache := buildCodexUsageArtifacts(entries)
	usageScheduler := map[string]any{"scheduledCredentialCount": numericMapOr(usageSummary, "scheduledAccountCount", 0), "dueCredentialCount": 0, "started": false}
	loadBalancingMode := h.codexSettingsValue("loadBalancingMode", "tiered_sticky")
	tierOrder, _ := h.codexSettingsValue("tierOrder", []any{}).([]any)
	payload := map[string]any{"entries": entries, "availableEntries": available, "unavailableEntries": unavailable, "availableTotal": len(available), "unavailableTotal": len(unavailable), "currentId": currentID, "loadBalancingMode": loadBalancingMode, "tierOrder": tierOrder, "effectiveTierOrder": tierOrder, "proxy": h.codexSettingsValue("proxy", ""), "defaultReasoningEffort": h.codexSettingsValue("defaultReasoningEffort", nil), "useWebSocket": h.codexSettingsValue("useWebSocket", true), "total": len(entries), "available": len(available), "stickySessionCount": 0, "usageCache": usageCache, "usageSummary": usageSummary, "usageForecast": usageForecast, "usageScheduler": usageScheduler, "usageQueue": map[string]any{"items": []any{}, "isRunning": false}}
	if lastBrowserAuthError != "" {
		payload["lastBrowserAuthError"] = lastBrowserAuthError
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func modelList(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": []any{}, "fromCache": true})
}
func ok(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func okErr(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
func unavailableAuth(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, httpx.NotImplemented("Provider authentication"))
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}
