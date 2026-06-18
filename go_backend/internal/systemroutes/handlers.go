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
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	"narrafork-go/internal/buildinfo"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/restart"
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

type VNetStatsProvider interface {
	VNetStats(settings *config.Store) map[string]any
}

type GatewayRuntime interface {
	Status(ctx context.Context) map[string]any
	Reload(ctx context.Context, platforms []string) (map[string]any, error)
}

type GatewayNarratorRuntime interface {
	Create(ctx context.Context, input narrators.CreateInput) (narrators.Narrator, error)
	AddUserMessage(ctx context.Context, narratorID, userID, text string) (narrators.Message, error)
}

type Handler struct {
	DB               *sql.DB
	RuntimeDB        *sql.DB
	Settings         *config.Store
	TerminalRuntime  TerminalRuntime
	Narrators        *narrators.Service
	GatewayNarrators GatewayNarratorRuntime
	VNet             VNetStatsProvider
	Restart          *restart.Manager
	Gateway          GatewayRuntime
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

// runtimeCacheTTL mirrors the TypeScript runtime-service CACHE_TTL_MS (30s):
// runtime state changes fast, so a cached scan older than this window must be
// treated as expired and cleared instead of served indefinitely.
const runtimeCacheTTL = 30 * time.Second

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
	if err := validateBenchmarkSuiteBody(body); err != nil {
		return err
	}
	suiteID := id.New()
	now := db.NowISO()
	tasks, _ := jsonMarshalString(body["tasks"])
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO benchmark_suites (id,name,version,description,tasks_json,created_at) VALUES (?,?,?,?,?,?)`, suiteID, strings.TrimSpace(stringMap(body, "name", "Suite")), nullableMapString(body, "version"), nullableMapString(body, "description"), tasks, now)
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
	// Mirror the TypeScript createRunSchema (server/routes/benchmarks.ts):
	// suiteId min1/max120, name min1/max120, model min1/max200, systemPrompt
	// max100000 optional, permissionMode strict 5-value enum optional. Go
	// previously read these via stringMap defaults with no bounds/enum checks, so
	// a missing/over-long name/model, an empty suiteId (which fell through to a 404
	// instead of a 400), or an invalid permissionMode was accepted.
	suiteID := strings.TrimSpace(stringMap(body, "suiteId", ""))
	if suiteID == "" || httpx.JSStringLength(suiteID) > 120 {
		return httpx.BadRequest("benchmark run suiteId must be 1-120 characters")
	}
	runName := strings.TrimSpace(stringMap(body, "name", ""))
	if runName == "" || httpx.JSStringLength(runName) > 120 {
		return httpx.BadRequest("benchmark run name must be 1-120 characters")
	}
	model := strings.TrimSpace(stringMap(body, "model", ""))
	if model == "" || httpx.JSStringLength(model) > 200 {
		return httpx.BadRequest("benchmark run model must be 1-200 characters")
	}
	if value, ok := body["systemPrompt"]; ok && value != nil {
		systemPrompt, isStr := value.(string)
		if !isStr || httpx.JSStringLength(systemPrompt) > 100_000 {
			return httpx.BadRequest("benchmark run systemPrompt must be <= 100000 characters")
		}
	}
	if value, ok := body["permissionMode"]; ok && value != nil {
		mode, isStr := value.(string)
		if !isStr || !validStrictBenchmarkPermissionMode(mode) {
			return httpx.BadRequest("permissionMode must be one of: default, acceptEdits, bypassPermissions, readOnly, dontAsk")
		}
	}
	// config: optional metadataSchema (record key max100, jsonSize <= 200000).
	if value, ok := body["config"]; ok && value != nil {
		if err := validateBenchmarkMetadata(value, "benchmark run config"); err != nil {
			return err
		}
	}
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
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO benchmark_runs (id,suite_id,name,model,system_prompt,permission_mode,status,config,total_tasks,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, runID, suiteID, runName, model, nullableMapString(body, "systemPrompt"), stringMap(body, "permissionMode", "bypassPermissions"), "pending", config, len(tasks), now)
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
func validStrictBenchmarkPermissionMode(value string) bool {
	switch value {
	case "default", "acceptEdits", "bypassPermissions", "readOnly", "dontAsk":
		return true
	default:
		return false
	}
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

func writeBenchmarkError(w http.ResponseWriter, status int, code, reason string) {
	httpx.WriteJSON(w, status, map[string]any{
		"ok":      false,
		"code":    code,
		"reason":  reason,
		"error":   reason,
		"message": reason,
	})
}

func (h *Handler) compareBenchmarkRuns(w http.ResponseWriter, r *http.Request) error {
	raw := strings.TrimSpace(r.URL.Query().Get("runIds"))
	if raw == "" {
		writeBenchmarkError(w, http.StatusBadRequest, "BENCHMARK_COMPARE_RUN_IDS_REQUIRED", "runIds query parameter required")
		return nil
	}
	runIDs := []string{}
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			runIDs = append(runIDs, value)
		}
	}
	if len(runIDs) < 2 {
		writeBenchmarkError(w, http.StatusBadRequest, "BENCHMARK_COMPARE_MIN_RUNS_REQUIRED", "At least 2 run IDs required")
		return nil
	}
	summaries := []map[string]any{}
	for _, runID := range runIDs {
		summary, err := h.benchmarkRunSummary(r.Context(), runID)
		if err != nil {
			writeBenchmarkError(w, http.StatusBadRequest, "BENCHMARK_RUN_SUMMARY_FAILED", err.Error())
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
	payload := map[string]any{
		"ok":                 true,
		"supported":          true,
		"fallback":           false,
		"mode":               "relay",
		"ws":                 true,
		"peerCleanup":        true,
		"udpRendezvous":      false,
		"networks":           0,
		"peers":              0,
		"connections":        0,
		"maxPeersPerNetwork": 64,
		"maxMessageBytes":    1024 * 1024,
		"relay":              map[string]any{"clients": 0, "rooms": 0, "peers": 0, "running": true},
		"udp":                map[string]any{"enabled": false},
	}
	if h.VNet != nil {
		payload = h.VNet.VNetStats(h.Settings)
	}
	payload["network"] = map[string]any{"hostname": hostname(), "addresses": addrs}
	httpx.WriteJSON(w, http.StatusOK, payload)
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
		runtime := h.gatewayNarratorRuntime()
		if runtime == nil {
			payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_RUNTIME_UNAVAILABLE", "Narrator runtime is not attached to the public gateway route")
			payload["chatId"] = msg.ChatID
			payload["userId"] = msg.UserID
			httpx.WriteJSON(w, http.StatusAccepted, payload)
			return nil
		}
		if h.DB == nil {
			payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_STORAGE_UNAVAILABLE", "Gateway session storage is not available")
			payload["chatId"] = msg.ChatID
			payload["userId"] = msg.UserID
			httpx.WriteJSON(w, http.StatusAccepted, payload)
			return nil
		}
		if tableOK, err := h.systemTableExists(r.Context(), "gateway_session_mappings"); err != nil || !tableOK {
			if err != nil {
				return err
			}
			payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_STORAGE_UNAVAILABLE", "Gateway session storage is not available")
			payload["chatId"] = msg.ChatID
			payload["userId"] = msg.UserID
			httpx.WriteJSON(w, http.StatusAccepted, payload)
			return nil
		}
		title := "Webhook: " + msg.Username
		narrator, err := runtime.Create(r.Context(), narrators.CreateInput{Title: &title, PermissionMode: h.gatewayDefaultPermissionMode(r.Context())})
		if err != nil {
			return err
		}
		mappingID, err := h.insertGatewayMappingForMessage(r.Context(), msg, narrator.ID)
		if err != nil {
			return err
		}
		creator := "gateway:" + msg.UserID
		if _, err := runtime.AddUserMessage(r.Context(), narrator.ID, creator, msg.Text); err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "supported": true, "fallback": false, "platform": msg.Platform, "chatId": msg.ChatID, "narratorId": narrator.ID, "mappingId": mappingID})
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
	runtime := h.gatewayNarratorRuntime()
	if runtime == nil {
		payload := gatewayFallbackPayload("GATEWAY_WEBHOOK_RUNTIME_UNAVAILABLE", "Narrator runtime is not attached to the public gateway route")
		payload["chatId"] = msg.ChatID
		payload["narratorId"] = narratorID
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return nil
	}

	creator := firstNonEmpty(stringMap(mapping, "app_user_id", ""), "gateway:"+msg.UserID)
	if _, err := runtime.AddUserMessage(r.Context(), narratorID, creator, msg.Text); err != nil {
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

func (h *Handler) insertGatewayMappingForMessage(ctx context.Context, msg gatewayInboundMessage, narratorID string) (string, error) {
	mappingID := id.New()
	now := db.NowISO()
	_, err := h.DB.ExecContext(ctx, `INSERT INTO gateway_session_mappings (id, platform, chat_id, user_id, username, narrator_id, last_message_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, mappingID, msg.Platform, msg.ChatID, msg.UserID, msg.Username, narratorID, now, now, now)
	return mappingID, err
}

func (h *Handler) gatewayNarratorRuntime() GatewayNarratorRuntime {
	if h.GatewayNarrators != nil {
		return h.GatewayNarrators
	}
	if h.Narrators != nil {
		return h.Narrators
	}
	return nil
}

func (h *Handler) gatewayDefaultPermissionMode(ctx context.Context) string {
	if h.DB != nil {
		if cfg, ok := h.gatewayDBConfig(ctx); ok {
			if value := strings.TrimSpace(stringMap(cfg, "defaultPermissionMode", "")); value != "" {
				return value
			}
		}
	}
	return ""
}

const gatewayRuntimeFallbackReason = "Persistent gateway runtimes are not bundled in go_backend"
const gatewayWebhookFallbackReason = "Gateway webhook narrator runtime is not attached in go_backend"

func gatewayWebhookStatusPayload(configured bool, supported bool) map[string]any {
	payload := map[string]any{"configured": configured, "supported": supported, "fallback": !supported}
	if !supported {
		payload["reason"] = gatewayWebhookFallbackReason
		payload["error"] = gatewayWebhookFallbackReason
		payload["message"] = gatewayWebhookFallbackReason
	}
	return payload
}

func (h *Handler) gatewayStatus(w http.ResponseWriter, r *http.Request) error {
	if h.Gateway != nil {
		httpx.WriteJSON(w, http.StatusOK, h.Gateway.Status(r.Context()))
		return nil
	}
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
	narratorRuntimeAttached := h.gatewayNarratorRuntime() != nil
	webhookPayload := gatewayWebhookStatusPayload(
		cfg.Enabled && cfg.Secret != "",
		narratorRuntimeAttached,
	)
	status := map[string]any{
		"started":         false,
		"platforms":       platforms,
		"sessions":        len(sessions),
		"configuredUsers": configuredUsers,
		"mode":            "fallback",
		"supported":       false,
		"fallback":        true,
		"reason":          gatewayRuntimeFallbackReason,
		"error":           gatewayRuntimeFallbackReason,
		"message":         gatewayRuntimeFallbackReason,
		"webhook":         webhookPayload,
	}
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
	if h.Gateway != nil {
		var body map[string]any
		if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
			return err
		}
		platforms := stringList(body["platforms"])
		payload, err := h.Gateway.Reload(r.Context(), platforms)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	cfg := h.gatewayWebhookConfig(r.Context())
	platforms := []string{}
	if cfg.Enabled {
		platforms = append(platforms, "webhook")
	}
	narratorRuntimeAttached := h.gatewayNarratorRuntime() != nil
	status := map[string]any{
		"started":   false,
		"platforms": platforms,
		"mode":      "fallback",
		"supported": false,
		"fallback":  true,
		"reason":    gatewayRuntimeFallbackReason,
		"error":     gatewayRuntimeFallbackReason,
		"message":   gatewayRuntimeFallbackReason,
		"webhook": gatewayWebhookStatusPayload(
			cfg.Enabled && cfg.Secret != "",
			narratorRuntimeAttached,
		),
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "reloaded": []string{}, "status": status})
	return nil
}

const gatewayWeixinQRUnsupportedReason = "Weixin QR gateway runtime is not bundled in go_backend."

func (h *Handler) gatewayWeixinQRStart(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "status": "unavailable", "supported": false, "fallback": true, "code": "WEIXIN_QR_UNSUPPORTED", "qrcodeUrl": "", "qrcodeToken": "", "reason": gatewayWeixinQRUnsupportedReason, "error": gatewayWeixinQRUnsupportedReason, "message": gatewayWeixinQRUnsupportedReason})
	return nil
}

func (h *Handler) gatewayWeixinQRPoll(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"ok": false, "status": "unavailable", "supported": false, "fallback": true, "code": "WEIXIN_QR_UNSUPPORTED", "reason": gatewayWeixinQRUnsupportedReason, "error": gatewayWeixinQRUnsupportedReason, "message": "No active QR session: " + gatewayWeixinQRUnsupportedReason})
	return nil
}

func gatewayFallbackPayload(code, message string) map[string]any {
	return map[string]any{
		"ok":        false,
		"supported": false,
		"fallback":  true,
		"code":      code,
		"reason":    message,
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
	current := buildinfo.VersionValue()
	canAuto := h.canApplySelfUpdate()
	payload := map[string]any{
		"updateAvailable":     false,
		"currentVersion":      current,
		"latestVersion":       current,
		"releaseInfo":         nil,
		"source":              "go_backend",
		"selfUpdateAvailable": canAuto,
		"canAutoRestart":      canAuto,
		"manualOnly":          !canAuto,
		"instructions":        updateInstructions("", canAuto),
	}
	if source, ok := h.trustedUpdateSource(); ok {
		payload["updateSource"] = map[string]any{"product": source.Product, "channel": source.Channel}
	} else {
		payload["reason"] = "settings.update.serverUrl is not configured"
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	release, err := h.trustedLatestUpdate(ctx)
	if err != nil {
		payload["error"] = err.Error()
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	if release.LatestVersion != "" {
		payload["latestVersion"] = release.LatestVersion
	}
	if release.ReleaseInfo != nil {
		payload["releaseInfo"] = release.ReleaseInfo
	}
	payload["updateAvailable"] = release.Available
	if release.Available {
		payload["downloadSize"] = release.DownloadSize
		payload["totalSize"] = release.TotalSize
		if release.Strategy != "" {
			payload["strategy"] = release.Strategy
		}
		if len(release.PatchChain) > 0 {
			payload["patchChain"] = release.PatchChain
		}
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

type trustedUpdateSource struct {
	ServerURL string
	Product   string
	Channel   string
	Platform  string
}

type trustedUpdateRelease struct {
	Available     bool
	Current       string
	LatestVersion string
	ReleaseInfo   map[string]any
	Files         []updateDownloadFile
	ExpectedBytes int64
	DownloadSize  int64
	TotalSize     int64
	Strategy      string
	PatchChain    []map[string]any
}

func (h *Handler) trustedUpdateSource() (trustedUpdateSource, bool) {
	update := map[string]any{}
	if h != nil && h.Settings != nil {
		if rawUpdate, ok := h.Settings.Raw()["update"].(map[string]any); ok {
			update = rawUpdate
		}
	}
	serverURL := strings.TrimSpace(stringMap(update, "serverUrl", ""))
	serverURL = strings.TrimRight(serverURL, "/")
	if serverURL == "" {
		return trustedUpdateSource{}, false
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return trustedUpdateSource{}, false
	}
	product := strings.TrimSpace(stringMap(update, "product", ""))
	if product == "" {
		product = "narrafork"
	}
	channel := strings.TrimSpace(stringMap(update, "channel", ""))
	if channel == "" {
		channel = "stable"
	}
	return trustedUpdateSource{ServerURL: serverURL, Product: product, Channel: channel, Platform: updatePlatform()}, true
}

func (h *Handler) trustedLatestUpdate(ctx context.Context) (trustedUpdateRelease, error) {
	source, ok := h.trustedUpdateSource()
	if !ok {
		return trustedUpdateRelease{Current: buildinfo.VersionValue()}, errorsNew("settings.update.serverUrl is not configured")
	}
	return fetchTrustedLatestUpdate(ctx, source, buildinfo.VersionValue())
}

func fetchTrustedLatestUpdate(ctx context.Context, source trustedUpdateSource, current string) (trustedUpdateRelease, error) {
	checkURL := trustedUpdateCheckURL(source, current)
	data, err := getJSON(ctx, checkURL, map[string]string{})
	if err != nil {
		return trustedUpdateRelease{Current: current}, err
	}
	return trustedUpdateReleaseFromV2(source, current, data), nil
}

func trustedUpdateCheckURL(source trustedUpdateSource, current string) string {
	endpoint := source.ServerURL + "/api/v2/products/" + url.PathEscape(source.Product) + "/releases/latest"
	values := url.Values{}
	values.Set("channel", source.Channel)
	values.Set("platform", source.Platform)
	values.Set("version", current)
	return endpoint + "?" + values.Encode()
}

func trustedUpdateReleaseFromV2(source trustedUpdateSource, current string, data map[string]any) trustedUpdateRelease {
	version := strings.TrimSpace(stringMap(data, "version", ""))
	release := trustedUpdateRelease{Current: current, LatestVersion: version}
	if !boolMap(data, "updateAvailable") || version == "" {
		return release
	}
	if !updateCurrentVersionAllowsDownload(current) {
		return release
	}
	file, _ := data["file"].(map[string]any)
	filename := strings.TrimSpace(stringMap(file, "filename", ""))
	if filename == "" {
		return release
	}
	size := int64(numericMapOr(file, "size", 0))
	sha := strings.TrimSpace(stringMap(file, "sha512", ""))
	downloadURL := trustedUpdateArtifactURL(source, version, filename)
	fileInfo := map[string]any{"url": downloadURL, "size": size, "sha512": sha}
	releaseInfo := map[string]any{
		"version":     version,
		"releaseDate": stringMap(data, "releaseDate", time.Now().Format(time.RFC3339)),
		"path":        filename,
		"sha512":      sha,
		"files":       []any{fileInfo},
	}
	if notes, ok := data["releaseNotes"]; ok {
		releaseInfo["releaseNotes"] = notes
	}
	if notes, ok := data["releaseNotesPerVersion"]; ok {
		releaseInfo["releaseNotesPerVersion"] = notes
	}
	v2 := map[string]any{}
	if zstd, ok := data["zstdPatch"].(map[string]any); ok && zstd != nil {
		if patchURL := trustedUpdateAbsoluteURL(source.ServerURL, stringMap(zstd, "url", "")); patchURL != "" {
			v2["zstdPatchUrl"] = patchURL
		}
		if metaURL := trustedUpdateAbsoluteURL(source.ServerURL, stringMap(zstd, "metaUrl", "")); metaURL != "" {
			v2["zstdPatchMetaUrl"] = metaURL
		}
	}
	patchChain := trustedPatchChain(source.ServerURL, data["patchChain"])
	if len(patchChain) > 0 {
		v2["patchChain"] = patchChain
	}
	if len(v2) > 0 {
		releaseInfo["_v2"] = v2
	}
	expectedBytes := size
	downloadSize := size
	strategy := ""
	// Mirror TS update-service: only select the zstd delta-patch download
	// strategy when the patch's fromVersion matches the CURRENT build version
	// (`data.zstdPatch.fromVersion === APP_VERSION`). A delta patch is only valid
	// from its declared base version; applying one whose fromVersion differs from
	// the running binary would corrupt the result, so a mismatch falls through to
	// the patch chain (if any) or a full download.
	if zstd, ok := data["zstdPatch"].(map[string]any); ok && zstd != nil && trustedUpdateVersionMatches(stringMap(zstd, "fromVersion", ""), current) {
		if patchSize := int64(numericMapOr(zstd, "patchSize", 0)); patchSize > 0 {
			downloadSize = patchSize
			strategy = "zstd"
		}
	} else if len(patchChain) > 0 {
		downloadSize = 0
		for _, step := range patchChain {
			downloadSize += int64(numericMapOr(step, "patchSize", 0))
		}
		strategy = "zstd"
	}
	release.Available = true
	release.ReleaseInfo = releaseInfo
	release.Files = []updateDownloadFile{{URL: downloadURL, Name: "artifact", Size: size, SHA512: sha}}
	release.ExpectedBytes = expectedBytes
	release.DownloadSize = downloadSize
	release.TotalSize = size
	release.Strategy = strategy
	release.PatchChain = patchChain
	return release
}

func trustedPatchChain(serverURL string, raw any) []map[string]any {
	items, _ := raw.([]any)
	chain := make([]map[string]any, 0, len(items))
	for _, item := range items {
		step, _ := item.(map[string]any)
		if step == nil {
			continue
		}
		trusted := map[string]any{}
		for key, value := range step {
			trusted[key] = value
		}
		if patchURL := trustedUpdateAbsoluteURL(serverURL, stringMap(step, "url", "")); patchURL != "" {
			trusted["url"] = patchURL
		}
		if metaURL := trustedUpdateAbsoluteURL(serverURL, stringMap(step, "metaUrl", "")); metaURL != "" {
			trusted["metaUrl"] = metaURL
		}
		chain = append(chain, trusted)
	}
	return chain
}

func trustedUpdateArtifactURL(source trustedUpdateSource, version, filename string) string {
	return source.ServerURL + "/api/v2/products/" + url.PathEscape(source.Product) + "/releases/" + url.PathEscape(version) + "/download/" + url.PathEscape(filename)
}

func trustedUpdateAbsoluteURL(serverURL, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	base, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.IsAbs() {
		if (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Scheme, base.Scheme) && strings.EqualFold(parsed.Host, base.Host) {
			return parsed.String()
		}
		return ""
	}
	ref := &url.URL{Path: value}
	if !strings.HasPrefix(value, "/") {
		ref.Path = "/" + value
	}
	return base.ResolveReference(ref).String()
}

// trustedUpdateVersionMatches reports whether a patch's declared fromVersion
// equals the current build version, mirroring the TS `=== APP_VERSION` check.
// Both sides are trimmed and a leading "v" is ignored defensively; an empty
// fromVersion never matches (so a patch with no base version is not applied).
func trustedUpdateVersionMatches(fromVersion, current string) bool {
	f := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(fromVersion), "v"))
	c := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(current), "v"))
	if f == "" {
		return false
	}
	return f == c
}

func updateCurrentVersionAllowsDownload(current string) bool {
	current = strings.TrimSpace(strings.TrimPrefix(current, "v"))
	if current == "" || strings.EqualFold(current, "go-backend-dev") || strings.EqualFold(current, "dev") {
		return false
	}
	base := current
	if idx := strings.IndexAny(base, "-+"); idx >= 0 {
		base = base[:idx]
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func updatePlatform() string {
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	switch runtime.GOOS {
	case "darwin":
		return "darwin-" + arch
	case "windows":
		return "win-" + arch
	default:
		return "linux-" + arch
	}
}

func (h *Handler) updateVersion(w http.ResponseWriter, r *http.Request) error {
	canAuto := h.canApplySelfUpdate()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": buildinfo.VersionValue(), "commit": buildinfo.CommitValue(), "backend": buildinfo.Backend, "buildChannel": buildinfo.ChannelValue(), "platform": runtime.GOOS, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "selfUpdateAvailable": canAuto, "manualOnly": !canAuto, "canAutoRestart": canAuto})
	return nil
}

func (h *Handler) updateDirectory(w http.ResponseWriter, r *http.Request) error {
	dir, err := updateBaseDir()
	if err != nil {
		return err
	}
	canAuto := h.canApplySelfUpdate()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"directory": dir, "selfUpdateAvailable": canAuto, "canAutoRestart": canAuto, "manualOnly": !canAuto, "instructions": updateInstructions(dir, canAuto)})
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
	canApply := h.canApplySelfUpdate()
	ready := boolMap(status, "ready")
	canAuto := canApply && ready
	status["canAutoRestart"] = canAuto
	status["selfUpdateAvailable"] = canApply
	status["manualOnly"] = !canApply
	status["instructions"] = updateInstructions(stringMap(status, "updatePath", ""), canAuto)
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
	requestedVersion := updateDownloadRequestedVersion(body)
	checkCtx, checkCancel := context.WithTimeout(r.Context(), 10*time.Second)
	release, err := h.trustedLatestUpdate(checkCtx)
	checkCancel()
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "No update available"})
		return nil
	}
	version := release.LatestVersion
	if !release.Available || version == "" || len(release.Files) == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "No update available"})
		return nil
	}
	if requestedVersion != "" && requestedVersion != version {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "Requested update version no longer matches the latest server metadata", "requestedVersion": requestedVersion, "latestVersion": version})
		return nil
	}
	send := newUpdateSSESender(w)
	files := release.Files
	expectedBytes := release.ExpectedBytes
	for _, file := range files {
		parsed, err := url.Parse(file.URL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			send("error", updateDownloadErrorPayload(version, "trusted update source returned an invalid artifact URL"))
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
	canAuto := h.canApplySelfUpdate()
	metadata := updateDownloadMetadata{Version: version, Directory: dir, Files: downloaded, TotalBytes: totalWritten, DownloadedAt: time.Now().Format(time.RFC3339), SelfUpdateAvailable: canAuto}
	if err := writeUpdateDownloadMetadata(dir, metadata); err != nil {
		cleanupDownloadedUpdateFiles(downloadedPaths)
		send("error", updateDownloadErrorPayload(version, err.Error()))
		return nil
	}

	completePayload := updateDownloadCompletePayload(version, dir, downloaded, totalWritten, totalBytes, canAuto)
	send("complete", completePayload)
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

func updateDownloadRequestedVersion(body map[string]any) string {
	releaseInfo, _ := body["releaseInfo"].(map[string]any)
	return strings.TrimSpace(firstNonEmpty(
		stringMap(body, "requestedVersion", ""),
		stringMap(releaseInfo, "version", ""),
		stringMap(body, "version", ""),
	))
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
	payload["code"] = "UPDATE_DOWNLOAD_FAILED"
	payload["version"] = version
	payload["reason"] = message
	payload["error"] = message
	payload["message"] = message
	payload["selfUpdateAvailable"] = false
	payload["canAutoRestart"] = false
	payload["manualOnly"] = true
	payload["instructions"] = updateManualInstructions("")
	return payload
}

func updateDownloadCompletePayload(version, directory string, files []updateDownloadedFileInfo, bytesDownloaded, totalBytes int64, canAuto bool) map[string]any {
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
	payload["selfUpdateAvailable"] = canAuto
	payload["canAutoRestart"] = canAuto
	payload["manualOnly"] = !canAuto
	payload["instructions"] = updateInstructions(updatePath, canAuto)
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
		return false
	}
	expected = strings.TrimPrefix(expected, "sha512-")
	return strings.EqualFold(expected, actualHex) || expected == actualBase64
}

func updateInstructions(updatePath string, canAuto bool) map[string]any {
	if canAuto {
		return updateAutomaticInstructions(updatePath)
	}
	return updateManualInstructions(updatePath)
}

func updateAutomaticInstructions(updatePath string) map[string]any {
	message := "Update artifact is verified and ready for automatic apply. Use the apply endpoint to start the replacement NarraFork process and gracefully stop this one."
	instructions := map[string]any{"manual": false, "message": message, "action": "apply"}
	if updatePath != "" {
		instructions["updatePath"] = updatePath
	}
	return instructions
}

func updateManualInstructions(updatePath string) map[string]any {
	message := "Automatic self-update/apply is not available for this prepared artifact. Stop NarraFork and manually replace or run the downloaded artifact as appropriate."
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
		"manualOnly":          true,
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
	_ = h.ensureDefaultClineProvider("")
	r.Route("/kiro", func(kr chi.Router) {
		kr.Use(h.adminMiddleware)
		kr.Get("/status", httpx.Handler(h.kiroStatus))
		kr.Get("/models", httpx.Handler(h.kiroModels))
		kr.Post("/models/refresh", httpx.Handler(h.kiroModels))
		kr.Get("/mcp-tools", httpx.Handler(h.kiroMcpTools))
		kr.Post("/mcp-search", httpx.Handler(h.kiroMcpSearch))
		kr.Post("/chat", httpx.Handler(h.kiroChat))
		kr.Post("/global-proxy", httpx.Handler(h.kiroSetGlobalProxy))
		kr.Post("/load-balancing-mode", httpx.Handler(h.kiroSetLoadBalancingMode))
		kr.Post("/credentials/import", httpx.Handler(h.kiroImportCredentials))
		kr.Post("/credentials/{id}/disable", httpx.Handler(h.kiroCredentialDisable))
		kr.Post("/credentials/{id}/enable", httpx.Handler(h.kiroCredentialEnable))
		kr.Post("/credentials/{id}/reset", httpx.Handler(h.kiroCredentialReset))
		kr.Post("/credentials/{id}/priority", httpx.Handler(h.kiroCredentialPriority))
		kr.Post("/credentials/{id}/refresh", httpx.Handler(h.kiroCredentialRefresh))
		kr.Get("/credentials/{id}/usage", httpx.Handler(h.kiroCredentialUsage))
		kr.Patch("/credentials/{id}", httpx.Handler(h.kiroCredentialUpdate))
		kr.Delete("/credentials/{id}", httpx.Handler(h.kiroCredentialDelete))
	})

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
	r.Get("/nug/quotas", httpx.Handler(h.nugQuotas))
	r.Get("/nug/providers/{id}/billing/config", httpx.Handler(h.nugBillingConfig))
	r.Post("/nug/providers/{id}/billing/orders", httpx.Handler(h.nugBillingCreateOrder))
	r.Get("/nug/providers/{id}/billing/orders/{orderId}", httpx.Handler(h.nugBillingGetOrder))
	r.Post("/nug/providers/{id}/billing/orders/{orderId}/repay", httpx.Handler(h.nugBillingRepayOrder))
	r.Get("/nug/providers/{id}/channels/health", httpx.Handler(h.nugChannelsHealth))
	r.Get("/nug/providers/{id}/usage", httpx.Handler(h.nugUsage))
	r.Get("/nug/providers/{id}/usage/summary", httpx.Handler(h.nugUsageSummary))
	r.Get("/nug/providers/{id}/oauth/start", httpx.Handler(h.nugOAuthStart))

	r.Get("/codex/quota-overview", httpx.Handler(h.codexQuotaOverview))
	r.Route("/codex", func(cr chi.Router) {
		cr.Use(h.adminMiddleware)
		cr.Get("/status", h.codexStatus)
		cr.Get("/default-reasoning-effort", httpx.Handler(h.codexGetDefaultReasoningEffort))
		cr.Post("/default-reasoning-effort", httpx.Handler(h.codexSetDefaultReasoningEffort))
		cr.Post("/use-websocket", httpx.Handler(h.codexSetUseWebSocket))
		cr.Post("/use-web-search", httpx.Handler(h.codexSetUseWebSearch))
		cr.Post("/use-image-generation", httpx.Handler(h.codexSetUseImageGeneration))
		cr.Post("/load-balancing-mode", httpx.Handler(h.codexSetLoadBalancingMode))
		cr.Post("/tier-order", httpx.Handler(h.codexSetTierOrder))
		cr.Post("/global-proxy", httpx.Handler(h.codexSetGlobalProxy))
		cr.Post("/import", httpx.Handler(h.codexImport))
		cr.Post("/usage-queue/clear", httpx.Handler(h.adminOnly(func(w http.ResponseWriter, r *http.Request) error {
			return httpx.NotImplemented("providers.codex.usageQueueClear")
		})))
		cr.Post("/auth/browser", httpx.Handler(h.codexAuthBrowser))
		cr.Post("/auth/browser/wait", httpx.Handler(h.codexAuthBrowserWait))
		cr.Post("/auth/browser/cancel", httpx.Handler(h.codexAuthCancel))
		cr.Post("/auth/device/start", httpx.Handler(h.codexDeviceStart))
		cr.Post("/auth/device/poll", httpx.Handler(h.codexDevicePoll))
		cr.Post("/auth/device/cancel", httpx.Handler(h.codexDeviceCancel))
		cr.Post("/credentials/{id}/disable", httpx.Handler(h.codexCredentialDisable))
		cr.Post("/credentials/{id}/enable", httpx.Handler(h.codexCredentialEnable))
		cr.Post("/credentials/{id}/reset", httpx.Handler(h.codexCredentialReset))
		cr.Post("/credentials/{id}/refresh", httpx.Handler(h.codexCredentialRefresh))
		cr.Post("/credentials/{id}/usage", httpx.Handler(h.codexCredentialUsage))
		cr.Patch("/credentials/{id}", httpx.Handler(h.codexCredentialUpdate))
		cr.Delete("/credentials/unhealthy", httpx.Handler(h.codexCredentialDeleteUnhealthy))
		cr.Delete("/credentials/batch", httpx.Handler(h.codexCredentialBatchDelete))
		cr.Delete("/credentials/{id}", httpx.Handler(h.codexCredentialDelete))
	})

	r.Route("/cline", func(cr chi.Router) {
		cr.Use(h.adminMiddleware)
		cr.Get("/status", httpx.Handler(h.clineStatus))
		cr.Get("/models", httpx.Handler(h.providerCachedModels("cline", false)))
		cr.Post("/models/refresh", httpx.Handler(h.providerCachedModels("cline", true)))
		cr.Get("/providers/{id}/models", httpx.Handler(h.providerCachedModels("cline", false)))
		cr.Post("/providers/{id}/models/refresh", httpx.Handler(h.providerCachedModels("cline", true)))
		cr.Get("/balance", httpx.Handler(h.clineBalance))
		cr.Get("/recommended-models", httpx.Handler(h.clineRecommendedModels))
		cr.Get("/pool/search", httpx.Handler(h.clinePoolSearch))
		cr.Get("/pool/count", httpx.Handler(h.clinePoolCount))
		cr.Post("/enabled-models", httpx.Handler(h.clineSetEnabledModels))
		cr.Post("/user-info/refresh", httpx.Handler(h.clineUserInfoRefresh))
		cr.Post("/auth/browser", httpx.Handler(h.clineAuthBrowser))
		cr.Post("/auth/cancel", httpx.Handler(h.clineAuthCancel))
		cr.Post("/auth/callback", httpx.Handler(h.clineAuthCallback))
		cr.Post("/auth/logout", httpx.Handler(h.clineAuthLogout))
	})
}

const kiroSuspiciousThrottleCooldown = 5 * time.Minute

var kiroMCPHTTPClient = &http.Client{Timeout: 30 * time.Second}

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
	if r.Method == http.MethodPost {
		reason := "Kiro model refresh requires the TypeScript Kiro plugin bridge; go_backend can only read cached per-credential model metadata."
		httpx.WriteFeatureDisabled(w, "providers.kiro.modelsRefresh", reason)
		return nil
	}
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
		httpx.WriteJSON(w, http.StatusOK, normalizeKiroMCPToolsResponse(result))
		return nil
	}
	reason := "Kiro MCP tools bridge is unavailable in go_backend; returning the built-in fallback tool list."
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": "go-backend-kiro-tools", "jsonrpc": "2.0", "tools": kiroFallbackMcpTools(), "result": map[string]any{"tools": kiroFallbackMcpTools()}, "fallback": true, "reason": reason, "error": reason, "message": reason})
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
		reason := "Kiro MCP search unavailable in go_backend: " + err.Error()
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"id":       "go-backend-kiro-search",
			"jsonrpc":  "2.0",
			"error":    map[string]any{"code": -32000, "message": reason},
			"fallback": true,
			"reason":   reason,
			"message":  reason,
		})
		return nil
	}
}

func (h *Handler) kiroChat(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	httpx.WriteFeatureDisabled(w, "kiro.chat", "Kiro chat streaming is not implemented in go_backend; use the narrator/provider agent routes or the TypeScript backend for Kiro chat streaming.")
	return nil
}

func (h *Handler) callKiroMCP(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return nil, err
	}
	maxRetries := kiroMaxCredentialRetries(len(entries))
	failedCredentialIDs := map[string]bool{}
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		entry, _, ok := firstUsableKiroCredentialFromEntries(entries, failedCredentialIDs)
		if !ok {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, errorsNew("No enabled Kiro credential with access token")
		}

		result, status, bodyText, err := h.callKiroMCPWithCredential(ctx, entry, method, params, attempt, maxRetries)
		if err != nil {
			lastErr = err
			continue
		}
		if result != nil {
			return result, nil
		}

		lastErr = errorsNew(status + ": " + bodyText)
		if !isKiroSuspiciousThrottle(bodyText) {
			return nil, lastErr
		}

		credentialID := stringMap(entry, "id", "")
		if hasAlternativeUsableKiroCredential(entries, credentialID, failedCredentialIDs) {
			entry["disabled"] = true
			entry["disabledReason"] = "suspicious_throttle"
			entry["disabledAt"] = time.Now().Format(time.RFC3339)
			if err := h.saveKiroCredentials(entries); err != nil {
				return nil, err
			}
			failedCredentialIDs[credentialID] = true
			continue
		}

		entry["lastUsedAt"] = time.Now().Format(time.RFC3339)
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errorsNew("No enabled Kiro credential with access token")
}

func normalizeKiroMCPToolsResponse(result map[string]any) map[string]any {
	if result == nil {
		return result
	}
	if _, exists := result["tools"]; exists {
		return result
	}
	resultPayload, _ := result["result"].(map[string]any)
	if tools, ok := resultPayload["tools"].([]any); ok {
		result["tools"] = tools
	}
	return result
}

func splitKiroSystemVersion(systemVersion string) (string, string) {
	parts := strings.SplitN(systemVersion, "#", 2)
	osName := strings.TrimSpace(parts[0])
	if osName == "" {
		osName = runtime.GOOS
	}
	osVersion := "unknown"
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		osVersion = strings.TrimSpace(parts[1])
	}
	return osName, osVersion
}

func (h *Handler) callKiroMCPWithCredential(ctx context.Context, entry map[string]any, method string, params map[string]any, attempt int, maxRetries int) (map[string]any, string, string, error) {
	config := h.loadKiroConfig()
	region := firstNonEmpty(stringMap(config, "apiRegion", ""), stringMap(config, "region", "us-east-1"), "us-east-1")
	kiroVersion := firstNonEmpty(stringMap(config, "kiroVersion", ""), "1.26.1")
	systemVersion := firstNonEmpty(stringMap(config, "systemVersion", ""), runtime.GOOS+"#unknown")
	osName, osVersion := splitKiroSystemVersion(systemVersion)
	token := strings.TrimSpace(stringMap(entry, "accessToken", ""))
	if token == "" {
		return nil, "", "", errorsNew("No Kiro access token available")
	}
	body := map[string]any{"id": id.Must(12), "jsonrpc": "2.0", "method": method, "params": params}
	if profileArn := stringMap(entry, "profileArn", ""); profileArn != "" {
		body["profileArn"] = profileArn
	}
	payload, _ := json.Marshal(body)
	urlValue := "https://q." + region + ".amazonaws.com"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlValue, bytes.NewReader(payload))
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("x-amz-target", "AmazonCodeWhispererStreamingService.InvokeMCP")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	req.Header.Set("User-Agent", "AmazonQ-For-CLI/Version/"+kiroVersion)
	req.Header.Set("Host", "q."+region+".amazonaws.com")
	req.Header.Set("amz-sdk-invocation-id", id.Must(32))
	req.Header.Set("amz-sdk-request", fmt.Sprintf("attempt=%d; max=%d", attempt+1, maxRetries))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-amzn-codewhisperer-os", osName)
	req.Header.Set("x-amzn-codewhisperer-os-version", osVersion)
	req.Header.Set("x-amzn-codewhisperer-ide", "kiro")
	req.Header.Set("x-amzn-codewhisperer-ide-version", kiroVersion)
	resp, err := kiroMCPHTTPClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1000000))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Status, string(data), nil
	}
	result := map[string]any{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, "", "", err
	}
	return result, resp.Status, string(data), nil
}

func (h *Handler) firstUsableKiroCredential() (map[string]any, error) {
	entries, err := h.loadKiroCredentials()
	if err != nil {
		return nil, err
	}
	entry, _, ok := firstUsableKiroCredentialFromEntries(entries, nil)
	if !ok {
		return nil, errorsNew("No enabled Kiro credential with access token")
	}
	return entry, nil
}

func firstUsableKiroCredentialFromEntries(entries []map[string]any, excludeIDs map[string]bool) (map[string]any, int, bool) {
	for i, entry := range entries {
		credentialID := stringMap(entry, "id", "")
		if excludeIDs != nil && excludeIDs[credentialID] {
			continue
		}
		if boolMap(entry, "disabled") {
			continue
		}
		if strings.TrimSpace(stringMap(entry, "accessToken", "")) != "" {
			return entry, i, true
		}
	}
	return nil, -1, false
}

func hasAlternativeUsableKiroCredential(entries []map[string]any, currentID string, excludeIDs map[string]bool) bool {
	for _, entry := range entries {
		credentialID := stringMap(entry, "id", "")
		if credentialID == currentID {
			continue
		}
		if excludeIDs != nil && excludeIDs[credentialID] {
			continue
		}
		if boolMap(entry, "disabled") {
			continue
		}
		if strings.TrimSpace(stringMap(entry, "accessToken", "")) != "" {
			return true
		}
	}
	return false
}

func isKiroSuspiciousThrottle(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "throttlingexception") && strings.Contains(lower, "suspicious activity")
}

func kiroMaxCredentialRetries(total int) int {
	if total <= 0 {
		return 1
	}
	retries := total * 3
	if retries > 9 {
		return 9
	}
	return retries
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
	// Mirror the TypeScript /credentials/import validation (server/routes/kiro.ts):
	// it reads `body?.credentials` and requires Array.isArray(credentials) (else
	// 400 "credentials must be an array"), and every entry must be a plain object —
	// typeof === "object" && !null && !Array (else 400 "each credential must be a
	// plain object"). A bare-array body has no `.credentials`, so TS rejects it too.
	// Go previously initialized inputs to an empty slice and only rejected a nil
	// slice (unreachable), so a missing/non-array `credentials` silently imported 0
	// entries with a 200, and non-object entries were silently skipped.
	bodyMap, ok := body.(map[string]any)
	if !ok {
		return httpx.BadRequest("credentials must be an array")
	}
	inputs, ok := bodyMap["credentials"].([]any)
	if !ok {
		return httpx.BadRequest("credentials must be an array")
	}
	for _, item := range inputs {
		if _, ok := item.(map[string]any); !ok {
			return httpx.BadRequest("each credential must be a plain object")
		}
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
		delete(entry, "disabledAt")
		return nil
	})
}

func (h *Handler) kiroCredentialReset(w http.ResponseWriter, r *http.Request) error {
	return h.updateKiroCredential(w, chi.URLParam(r, "id"), func(entry map[string]any) error {
		entry["disabled"] = false
		entry["failureCount"] = 0
		delete(entry, "disabledReason")
		delete(entry, "disabledAt")
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
	// Mirror the TypeScript updateCredentialSchema Zod bounds (server/routes/kiro.ts):
	// email/displayName max 200, region max 50. Go previously applied no length
	// bounds, so an over-long value was persisted instead of returning a 400.
	for _, field := range []struct {
		key string
		max int
	}{
		{"email", 200},
		{"displayName", 200},
		{"region", 50},
	} {
		if value, ok := body[field.key]; ok && value != nil {
			text, isString := value.(string)
			if !isString {
				return httpx.BadRequest(field.key + " must be a string")
			}
			if httpx.JSStringLength(text) > field.max {
				return httpx.BadRequest(field.key + " must be at most " + strconv.Itoa(field.max) + " characters")
			}
		}
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

func loadNugQuotaCache() (map[string]map[string]any, error) {
	path, err := nugQuotaCachePath()
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

func saveNugQuotaCache(cache map[string]map[string]any) error {
	path, err := nugQuotaCachePath()
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

func nugQuotaCachePath() (string, error) {
	dir, err := narraforkDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nug-quotas.json"), nil
}

func (h *Handler) providerCachedModels(kind string, refresh bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
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
				httpx.WriteJSON(w, http.StatusOK, providerModelRefreshResponse(kind, models, extra))
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
			models := cache[providerID]
			if models == nil {
				models = []map[string]any{}
			}
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": models, "fromCache": true})
			return nil
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": aggregateProviderModels(kind, h.providerConfigs(kind), cache), "fromCache": true})
		return nil
	}
}

func providerModelRefreshResponse(kind string, models []map[string]any, extra map[string]any) map[string]any {
	if kind == "cline" {
		return map[string]any{"count": len(models), "fromCache": false}
	}
	response := map[string]any{"models": models, "fromCache": false}
	for key, value := range extra {
		response[key] = value
	}
	return response
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
	return nil, httpx.Error(http.StatusNotFound, "NOT_FOUND", "Provider \""+providerID+"\" not found")
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
	data, err := getJSON(ctx, clineOpenRouterModelsURL(), clineOpenRouterHeaders())
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
	sort.Slice(models, func(i, j int) bool {
		return stringMap(models[i], "id", "") < stringMap(models[j], "id", "")
	})
	return models, nil
}

func clineOpenRouterModelsURL() string {
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_CLINE_OPENROUTER_MODELS_URL")); value != "" {
		return value
	}
	return "https://openrouter.ai/api/v1/models"
}

func clineRecommendedModelsURL() string {
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_CLINE_RECOMMENDED_MODELS_URL")); value != "" {
		return value
	}
	return "https://api.cline.bot/api/v1/ai/cline/recommended-models"
}

func clineAPIBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_CLINE_API_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return strings.TrimRight(clineDefaultBaseURL, "/")
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
	if err := json.Unmarshal(data, &raw); err == nil {
		return raw, nil
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return map[string][]map[string]any{}, err
	}
	providers := decoded
	if nested, ok := decoded["providers"].(map[string]any); ok {
		providers = nested
	}
	for providerID, value := range providers {
		switch typed := value.(type) {
		case []any:
			raw[providerID] = mapsFromAnyModels(typed)
		case map[string]any:
			if models, ok := typed["models"].([]any); ok {
				raw[providerID] = mapsFromAnyModels(models)
			}
		}
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

func loadProviderModelHashCache(kind string) map[string]string {
	path, err := providerModelCachePath(kind)
	if err != nil {
		return map[string]string{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return map[string]string{}
	}
	providers, _ := decoded["providers"].(map[string]any)
	if providers == nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for providerID, value := range providers {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if hash := firstNonEmpty(stringMap(entry, "modelHash", ""), stringMap(entry, "hash", "")); strings.TrimSpace(hash) != "" {
			out[providerID] = strings.TrimSpace(hash)
		}
	}
	return out
}

func saveNugProviderModelCache(value map[string][]map[string]any, hashes map[string]string) error {
	path, err := providerModelCachePath("nug")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	providers := map[string]any{}
	for providerID, models := range value {
		entry := map[string]any{"models": models}
		if hash := strings.TrimSpace(hashes[providerID]); hash != "" {
			entry["modelHash"] = hash
		}
		providers[providerID] = entry
	}
	data, err := json.Marshal(map[string]any{"version": 1, "providers": providers})
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
	providers := h.providerConfigs("nug")
	cache, _ := loadProviderModelCache("nug")
	hashes := loadProviderModelHashCache("nug")
	if r.Method == http.MethodGet {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": aggregateProviderModels("nug", providers, cache), "fromCache": true})
		return nil
	}

	results := []map[string]any{}
	for _, provider := range providers {
		providerID := stringMap(provider, "id", "")
		providerName := stringMap(provider, "name", "")
		result := map[string]any{"providerId": providerID, "name": providerName, "count": 0}
		if strings.TrimSpace(stringMap(provider, "apiKey", "")) == "" {
			result["error"] = fmt.Sprintf("NUG API key not configured for provider %q", providerName)
			results = append(results, result)
			continue
		}
		data, err := h.fetchNugJSON(r.Context(), providerID, "/v1/models", url.Values{})
		if err != nil {
			result["error"] = err.Error()
			results = append(results, result)
			continue
		}
		modelHash := strings.TrimSpace(firstNonEmpty(stringMap(data, "modelHash", ""), stringMap(data, "hash", "")))
		if modelHash != "" {
			result["modelHash"] = modelHash
			hashes[providerID] = modelHash
		} else {
			delete(hashes, providerID)
		}
		models := normalizeNugModelMaps(data)
		cache[providerID] = models
		if _, err := h.mergeNugModelContextWindows(provider, models); err != nil {
			result["error"] = err.Error()
			results = append(results, result)
			continue
		}
		result["count"] = len(models)
		results = append(results, result)
	}
	_ = saveNugProviderModelCache(cache, hashes)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results, "models": aggregateProviderModels("nug", providers, cache), "fromCache": false, "modelContextWindows": h.currentModelContextWindows()})
	return nil
}

func (h *Handler) nugModels(w http.ResponseWriter, r *http.Request) error {
	providerID := chi.URLParam(r, "id")
	cache, _ := loadProviderModelCache("nug")
	hashes := loadProviderModelHashCache("nug")
	if r.Method == http.MethodGet {
		models := uniqueModelMaps(cache[providerID], "id")
		if models == nil {
			models = []map[string]any{}
		}
		response := map[string]any{"models": models, "fromCache": true}
		if hash := strings.TrimSpace(hashes[providerID]); hash != "" {
			response["modelHash"] = hash
		}
		httpx.WriteJSON(w, http.StatusOK, response)
		return nil
	}

	provider, _, err := h.nugProvider(providerID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(stringMap(provider, "apiKey", "")) == "" {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"error": fmt.Sprintf("NUG API key not configured for provider %q", stringMap(provider, "name", "")),
		})
		return nil
	}
	data, err := h.fetchNugJSON(r.Context(), providerID, "/v1/models", url.Values{})
	if err != nil {
		return err
	}
	modelHash := strings.TrimSpace(firstNonEmpty(stringMap(data, "modelHash", ""), stringMap(data, "hash", "")))
	models := normalizeNugModelMaps(data)
	cache[providerID] = models
	if modelHash != "" {
		hashes[providerID] = modelHash
	} else {
		delete(hashes, providerID)
	}
	_ = saveNugProviderModelCache(cache, hashes)
	modelContextWindows, err := h.mergeNugModelContextWindows(provider, models)
	if err != nil {
		return err
	}
	response := map[string]any{"models": models, "fromCache": false, "modelContextWindows": modelContextWindows}
	if modelHash != "" {
		response["modelHash"] = modelHash
	}
	httpx.WriteJSON(w, http.StatusOK, response)
	return nil
}

func (h *Handler) nugQuota(w http.ResponseWriter, r *http.Request) error {
	providerID := chi.URLParam(r, "id")
	data, err := h.fetchNugJSON(r.Context(), providerID, "/v1/quota", url.Values{})
	if err != nil {
		return err
	}
	quota := normalizeNugQuotaResponse(data)
	cache, _ := loadNugQuotaCache()
	entry := map[string]any{
		"balance":      quota["balance"],
		"totalGranted": quota["totalGranted"],
		"fetchedAt":    time.Now().UnixMilli(),
	}
	if detailedQuotaBalance := stringMap(quota, "detailedQuotaBalance", ""); detailedQuotaBalance != "" {
		entry["detailedQuotaBalance"] = detailedQuotaBalance
	}
	if extra, ok := quota["extra"]; ok {
		entry["extra"] = extra
	}
	cache[providerID] = entry
	_ = saveNugQuotaCache(cache)
	httpx.WriteJSON(w, http.StatusOK, quota)
	return nil
}

func (h *Handler) nugQuotas(w http.ResponseWriter, r *http.Request) error {
	cache, _ := loadNugQuotaCache()
	out := map[string]map[string]any{}
	for providerID, entry := range cache {
		detailedQuotaBalance := strings.TrimSpace(stringMap(entry, "detailedQuotaBalance", ""))
		item := map[string]any{
			"balance":              entry["balance"],
			"totalGranted":         entry["totalGranted"],
			"detailedQuotaBalance": nil,
		}
		if detailedQuotaBalance != "" {
			item["detailedQuotaBalance"] = detailedQuotaBalance
		}
		if extra, ok := entry["extra"]; ok {
			item["extra"] = extra
		}
		out[providerID] = item
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (h *Handler) nugBillingConfig(w http.ResponseWriter, r *http.Request) error {
	providerID := chi.URLParam(r, "id")
	data, err := h.fetchNugJSON(r.Context(), providerID, "/v1/billing/config", url.Values{})
	if err != nil {
		return err
	}
	quota := normalizeNugQuotaResponse(data)
	cache, _ := loadNugQuotaCache()
	entry := cache[providerID]
	if entry == nil {
		entry = map[string]any{}
	}
	entry["balance"] = quota["balance"]
	entry["totalGranted"] = quota["totalGranted"]
	entry["fetchedAt"] = time.Now().UnixMilli()
	if detailedQuotaBalance := stringMap(quota, "detailedQuotaBalance", ""); detailedQuotaBalance != "" {
		entry["detailedQuotaBalance"] = detailedQuotaBalance
	}
	if extra, ok := quota["extra"]; ok {
		entry["extra"] = extra
	}
	cache[providerID] = entry
	_ = saveNugQuotaCache(cache)
	httpx.WriteJSON(w, http.StatusOK, quota)
	return nil
}

// nugCoerceAmount mirrors the TypeScript `Number(body.amount)` + Number.isFinite
// check for billing-order amount: a JSON number is used directly (NaN/Inf
// rejected), a string is coerced via JS Number() semantics (httpx.CoerceNumberJS,
// where ""/whitespace -> 0), and booleans/null/objects/absent are treated as
// non-finite (NaN) and rejected.
func nugCoerceAmount(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	case string:
		return httpx.CoerceNumberJS(v)
	default:
		return 0, false
	}
}

func (h *Handler) nugBillingCreateOrder(w http.ResponseWriter, r *http.Request) error {
	var payload map[string]any
	if err := httpx.DecodeLooseJSON(r, &payload); err != nil {
		return err
	}
	// Mirror the TypeScript billing-orders create validation (server/routes/nug.ts):
	// amount must coerce to a finite number > 0, and provider is required. TS runs
	// these BEFORE the upstream call; Go previously forwarded the raw payload with
	// no validation, so a missing/zero/negative amount or missing provider reached
	// the upstream instead of returning a 400.
	amount, ok := nugCoerceAmount(payload["amount"])
	if !ok || amount <= 0 {
		return httpx.BadRequest("amount must be greater than 0")
	}
	if strings.TrimSpace(stringMap(payload, "provider", "")) == "" {
		return httpx.BadRequest("provider is required")
	}
	data, err := h.fetchNugJSONWithBody(r.Context(), chi.URLParam(r, "id"), http.MethodPost, "/v1/billing/orders", url.Values{}, payload)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, data)
	return nil
}

func (h *Handler) nugBillingGetOrder(w http.ResponseWriter, r *http.Request) error {
	endpoint := "/v1/billing/orders/" + url.PathEscape(chi.URLParam(r, "orderId"))
	data, err := h.fetchNugJSON(r.Context(), chi.URLParam(r, "id"), endpoint, url.Values{})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, data)
	return nil
}

func (h *Handler) nugBillingRepayOrder(w http.ResponseWriter, r *http.Request) error {
	endpoint := "/v1/billing/orders/" + url.PathEscape(chi.URLParam(r, "orderId")) + "/repay"
	data, err := h.fetchNugJSONWithBody(r.Context(), chi.URLParam(r, "id"), http.MethodPost, endpoint, url.Values{}, map[string]any{})
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
	query := url.Values{
		"limit":  []string{strconv.Itoa(limit)},
		"offset": []string{strconv.Itoa(offset)},
		"period": []string{nugUsagePeriod(r.URL.Query().Get("range"))},
	}
	data, err := h.fetchNugJSON(r.Context(), chi.URLParam(r, "id"), "/v1/usage", query)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, normalizeNugUsageResponse(data))
	return nil
}

func (h *Handler) nugUsageSummary(w http.ResponseWriter, r *http.Request) error {
	period := nugUsagePeriod(r.URL.Query().Get("range"))
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
	return h.fetchNugJSONWithBody(ctx, providerID, http.MethodGet, endpoint, query, nil)
}

func (h *Handler) fetchNugJSONWithBody(ctx context.Context, providerID, method, endpoint string, query url.Values, payload any) (map[string]any, error) {
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
	var reqBody io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
	if headerHash := strings.TrimSpace(resp.Header.Get("X-NUG-Model-Hash")); headerHash != "" {
		if strings.TrimSpace(stringMap(data, "modelHash", "")) == "" {
			data["modelHash"] = headerHash
		}
		if strings.TrimSpace(stringMap(data, "hash", "")) == "" {
			data["hash"] = headerHash
		}
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

func normalizeNugQuotaResponse(data map[string]any) map[string]any {
	quota := map[string]any{}
	for key, value := range data {
		quota[key] = value
	}
	quota["balance"] = nugFirstNumber(data, 0, "balance", "quotaBalance", "remaining", "creditsRemaining")
	quota["totalGranted"] = nugFirstNumber(data, 0, "totalGranted", "quotaTotalGranted", "total_granted", "creditsTotal", "total")
	quota["detailedQuotaBalance"] = nil
	if detailedQuotaBalance := nugQuotaDetails(data); detailedQuotaBalance != "" {
		quota["detailedQuotaBalance"] = detailedQuotaBalance
	}
	return quota
}

func nugQuotaDetails(data map[string]any) string {
	return firstNonEmpty(stringMap(data, "detailedQuotaBalance", ""), formatNugExtraDetails(data["extra"]))
}

func formatNugExtraDetails(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64, int, bool:
		return fmt.Sprint(typed)
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key, entryValue := range typed {
			if entryValue != nil {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, key := range keys {
			entryValue := typed[key]
			switch v := entryValue.(type) {
			case string:
				lines = append(lines, key+": "+v)
			case float64, int, bool:
				lines = append(lines, key+": "+fmt.Sprint(v))
			default:
				encoded, err := json.Marshal(v)
				if err == nil {
					lines = append(lines, key+": "+string(encoded))
				}
			}
		}
		return strings.Join(lines, "\n")
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func nugFirstNumber(data map[string]any, fallback float64, keys ...string) float64 {
	for _, key := range keys {
		if _, ok := data[key]; ok {
			return numericMapOr(data, key, fallback)
		}
	}
	return fallback
}

func normalizeNugModels(data map[string]any) []any {
	models := normalizeNugModelMaps(data)
	out := make([]any, 0, len(models))
	for _, model := range models {
		out = append(out, model)
	}
	return out
}

func nugModelContextLength(model map[string]any) (any, bool) {
	for _, key := range []string{"contextLength", "contextWindow", "context_length", "context_window"} {
		if _, ok := model[key]; !ok {
			continue
		}
		value := numericMapOr(model, key, 0)
		if value > 0 {
			return int64(value), true
		}
	}
	return nil, false
}

func (h *Handler) currentModelContextWindows() map[string]any {
	if h.Settings == nil {
		return map[string]any{}
	}
	raw := h.Settings.Raw()
	agent, _ := raw["agent"].(map[string]any)
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if windows == nil {
		return map[string]any{}
	}
	return windows
}

func (h *Handler) mergeNugModelContextWindows(provider map[string]any, models []map[string]any) (map[string]any, error) {
	if h.Settings == nil {
		return map[string]any{}, nil
	}
	raw := h.Settings.Raw()
	agent, _ := raw["agent"].(map[string]any)
	if agent == nil {
		agent = map[string]any{}
	}
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if windows == nil {
		windows = map[string]any{}
	}
	prefix := firstNonEmpty(stringMap(provider, "prefix", ""), "nug")
	changed := false
	for _, model := range models {
		modelID := strings.TrimSpace(stringMap(model, "id", ""))
		if modelID == "" {
			continue
		}
		contextLength, ok := nugModelContextLength(model)
		if !ok || windows[prefix+":"+modelID] != nil {
			continue
		}
		windows[prefix+":"+modelID] = contextLength
		changed = true
	}
	if !changed {
		return windows, nil
	}
	agent["modelContextWindows"] = windows
	patched, err := h.Settings.Patch(map[string]any{"agent": agent})
	if err != nil {
		return nil, err
	}
	if patchedAgent, ok := patched["agent"].(map[string]any); ok {
		if patchedWindows, ok := patchedAgent["modelContextWindows"].(map[string]any); ok {
			return patchedWindows, nil
		}
	}
	return windows, nil
}

func normalizeNugModelMaps(data map[string]any) []map[string]any {
	rawModels, _ := data["models"].([]any)
	if rawModels == nil {
		rawModels, _ = data["data"].([]any)
	}
	out := make([]map[string]any, 0, len(rawModels))
	seen := map[string]bool{}
	for _, item := range rawModels {
		var model map[string]any
		switch m := item.(type) {
		case map[string]any:
			model = m
		case string:
			model = map[string]any{"id": m, "name": m}
		default:
			continue
		}
		modelID := strings.TrimSpace(stringMap(model, "id", ""))
		if modelID == "" || seen[modelID] {
			continue
		}
		seen[modelID] = true
		model["id"] = modelID
		if contextLength, ok := nugModelContextLength(model); ok {
			model["contextLength"] = contextLength
		}
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return stringMap(out[i], "id", "") < stringMap(out[j], "id", "") })
	return out
}

func nugUsagePeriod(rangeValue string) string {
	period := map[string]string{
		"today":  "today",
		"7days":  "7days",
		"30days": "month",
		"month":  "month",
		"all":    "all",
	}[rangeValue]
	if period == "" {
		period = "month"
	}
	return period
}

var nugUsageResponseStandardKeys = map[string]struct{}{
	"id": {}, "requestId": {}, "request_id": {},
	"channelType": {}, "channel_type": {}, "channel": {}, "provider": {}, "providerType": {},
	"model": {}, "model_id": {}, "modelId": {}, "modelName": {},
	"inputTokens": {}, "input_tokens": {}, "promptTokens": {}, "prompt_tokens": {}, "tokensIn": {}, "tokens_in": {},
	"outputTokens": {}, "output_tokens": {}, "completionTokens": {}, "completion_tokens": {},
	"cacheCreationInputTokens": {}, "cache_creation_input_tokens": {}, "cacheCreationTokens": {}, "cache_creation_tokens": {}, "cacheWriteInputTokens": {}, "cache_write_input_tokens": {}, "cacheWriteTokens": {}, "cache_write_tokens": {},
	"cacheReadInputTokens": {}, "cache_read_input_tokens": {}, "cachedInputTokens": {}, "cached_input_tokens": {}, "cacheReadTokens": {}, "cache_read_tokens": {},
	"reasoningTokens": {}, "reasoning_tokens": {},
	"quotaCost": {}, "quota_cost": {}, "cost": {}, "quota": {},
	"meterUsage": {}, "meter_usage": {}, "meter": {}, "usageAmount": {},
	"status": {}, "state": {}, "durationMs": {}, "duration_ms": {}, "latencyMs": {}, "latency_ms": {},
	"createdAt": {}, "created_at": {}, "timestamp": {}, "time": {},
	"extra": {}, "metadata": {}, "usage": {}, "usageData": {},
}

func normalizeNugUsageResponse(data map[string]any) map[string]any {
	rawEvents, _ := data["events"].([]any)
	if rawEvents == nil {
		rawEvents, _ = data["data"].([]any)
	}
	events := make([]any, 0, len(rawEvents))
	for index, item := range rawEvents {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sources := nugUsageSources(record)
		id := nugStringFrom(sources, "id", "requestId", "request_id")
		if id == "" {
			id = "usage-" + strconv.Itoa(index)
		}
		event := map[string]any{}
		event["id"] = id
		event["channelType"] = firstNonEmpty(nugStringFrom(sources, "channelType", "channel_type", "channel", "provider", "providerType"), "unknown")
		event["model"] = nugStringFrom(sources, "model", "model_id", "modelId", "modelName")
		event["inputTokens"] = nugNumberFrom(sources, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens", "tokensIn", "tokens_in")
		event["outputTokens"] = nugNumberFrom(sources, "outputTokens", "output_tokens", "completionTokens", "completion_tokens")
		event["cacheCreationInputTokens"] = nugNumberFrom(sources, "cacheCreationInputTokens", "cache_creation_input_tokens", "cacheCreationTokens", "cache_creation_tokens", "cacheWriteInputTokens", "cache_write_input_tokens", "cacheWriteTokens", "cache_write_tokens")
		event["cacheReadInputTokens"] = nugNumberFrom(sources, "cacheReadInputTokens", "cache_read_input_tokens", "cachedInputTokens", "cached_input_tokens", "cacheReadTokens", "cache_read_tokens")
		event["reasoningTokens"] = nugNumberFrom(sources, "reasoningTokens", "reasoning_tokens")
		event["quotaCost"] = nugNumberFrom(sources, "quotaCost", "quota_cost", "cost", "quota")
		event["meterUsage"] = nugNumberFrom(sources, "meterUsage", "meter_usage", "meter", "usageAmount")
		event["status"] = firstNonEmpty(nugStringFrom(sources, "status", "state"), "unknown")
		event["durationMs"] = nugNumberFrom(sources, "durationMs", "duration_ms", "latencyMs", "latency_ms")
		event["createdAt"] = nugStringFrom(sources, "createdAt", "created_at", "timestamp", "time")
		if extra := safeNugUsageExtra(record); len(extra) > 0 {
			event["extra"] = extra
		}
		events = append(events, event)
	}
	total := float64(len(events))
	if _, ok := data["total"]; ok {
		total = nugNumber(data, "total")
	} else if _, ok := data["count"]; ok {
		total = nugNumber(data, "count")
	}
	return map[string]any{"events": events, "total": total}
}

func safeNugUsageExtra(record map[string]any) map[string]any {
	out := map[string]any{}
	addSource := func(source map[string]any, prefix string) {
		if source == nil || len(out) >= 12 {
			return
		}
		for key, value := range source {
			if len(out) >= 12 || value == nil || isNugUsageStandardKey(key) || isSensitiveNugUsageKey(key) {
				continue
			}
			label := key
			if prefix != "" {
				label = prefix + "." + key
			}
			// Mirror TS `label.slice(0, USAGE_EXTRA_KEY_MAX_CHARS)` (80, UTF-16 code
			// units; nug.ts:399). Go previously used a byte slice, which for a
			// non-ASCII usage key would cut at the wrong boundary / mid-rune.
			if httpx.JSStringLength(label) > 80 {
				label = httpx.JSStringSlicePrefix(label, 80)
			}
			if _, exists := out[label]; exists {
				continue
			}
			if sanitized, ok := sanitizeNugUsageExtraValue(value); ok {
				out[label] = sanitized
			}
		}
	}
	extra := nugOptionalRecord(record["extra"])
	metadata := nugOptionalRecord(record["metadata"])
	usage := nugOptionalRecord(record["usage"])
	addSource(record, "")
	addSource(extra, "")
	if extra != nil {
		addSource(nugOptionalRecord(extra["usage"]), "usage")
		addSource(nugOptionalRecord(extra["metadata"]), "metadata")
	}
	addSource(metadata, "metadata")
	if metadata != nil {
		addSource(nugOptionalRecord(metadata["usage"]), "metadata.usage")
	}
	addSource(usage, "usage")
	addSource(nugOptionalRecord(record["usageData"]), "usageData")
	return out
}

func isNugUsageStandardKey(key string) bool {
	_, ok := nugUsageResponseStandardKeys[key]
	return ok
}

func isSensitiveNugUsageKey(key string) bool {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, key))
	for _, needle := range []string{"apikey", "authorization", "token", "secret", "password", "credential", "cookie", "session", "raw", "dump", "body", "headers", "prompt", "completion", "content"} {
		if strings.Contains(normalized, needle) {
			return true
		}
	}
	return false
}

func sanitizeNugUsageExtraValue(value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil, false
		}
		// Mirror TS `text.length > 300 ? text.slice(0, 300)+"…"` (UTF-16 code units;
		// nug.ts:383). Go previously used byte length + a byte slice, which for a
		// non-ASCII usage value diverged on the >300 gate and could cut mid-rune.
		if httpx.JSStringLength(text) > 300 {
			text = httpx.JSStringSlicePrefix(text, 300) + "…"
		}
		return text, true
	case float64, float32, int, int64, bool, json.Number:
		return typed, true
	default:
		encoded, err := json.Marshal(typed)
		if err != nil || len(encoded) == 0 || string(encoded) == "null" {
			return nil, false
		}
		text := string(encoded)
		if httpx.JSStringLength(text) > 300 {
			text = httpx.JSStringSlicePrefix(text, 300) + "…"
		}
		return text, true
	}
}

func nugOptionalRecord(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		trimmed := strings.TrimSpace(typed)
		if !strings.HasPrefix(trimmed, "{") {
			return nil
		}
		parsed := map[string]any{}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return nil
		}
		return parsed
	default:
		return nil
	}
}

func nugUsageSources(record map[string]any) []map[string]any {
	sources := []map[string]any{record}
	appendRecord := func(value any) map[string]any {
		parsed := nugOptionalRecord(value)
		if parsed != nil {
			sources = append(sources, parsed)
		}
		return parsed
	}
	extra := appendRecord(record["extra"])
	metadata := appendRecord(record["metadata"])
	usage := appendRecord(record["usage"])
	appendRecord(record["usageData"])
	if extra != nil {
		appendRecord(extra["usage"])
		appendRecord(extra["metadata"])
	}
	if metadata != nil {
		appendRecord(metadata["usage"])
		appendRecord(metadata["extra"])
	}
	if usage != nil {
		appendRecord(usage["extra"])
	}
	return sources
}

func nugStringFrom(records []map[string]any, keys ...string) string {
	for _, record := range records {
		if value := nugString(record, keys...); value != "" {
			return value
		}
	}
	return ""
}

func nugNumberFrom(records []map[string]any, keys ...string) float64 {
	for _, record := range records {
		for _, key := range keys {
			if _, ok := record[key]; ok {
				return numericMapOr(record, key, 0)
			}
		}
	}
	return 0
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
	providerID := chi.URLParam(r, "id")
	_, baseURL, err := h.nugProvider(providerID)
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
	apiKey := firstNonEmpty(stringMap(apiKeyData, "apiKey", ""), stringMap(apiKeyData, "api_key", ""))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"apiKey": apiKey, "user": loginData["user"]})
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
	return h.updateNugProviderCredentials(providerID, map[string]any{
		"apiKey":        stringMap(tokenData, "api_key", ""),
		"oauthDeviceId": stringMap(tokenData, "device_id", ""),
		"nugUsername":   stringMap(tokenData, "username", ""),
		"nugUserId":     stringMap(tokenData, "user_id", ""),
	})
}

func (h *Handler) updateNugProviderCredentials(providerID string, updates map[string]any) error {
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
		for key, value := range updates {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				provider[key] = value
			}
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
		baseURL := normalizeNugBaseURL(stringMap(provider, "baseUrl", ""))
		if baseURL == "" {
			return nil, "", httpx.BadRequest("Provider base URL not configured")
		}
		return provider, baseURL, nil
	}
	return nil, "", httpx.Error(http.StatusNotFound, "NOT_FOUND", "Provider \""+providerID+"\" not found")
}

func normalizeNugBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/api/v1"), "/v1")
		trimmed = strings.TrimSuffix(trimmed, "/api")
		if trimmed == baseURL {
			return baseURL
		}
		baseURL = trimmed
	}
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

func (h *Handler) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			httpx.WriteError(w, httpx.Forbidden("Admin access required"))
			return
		}
		next.ServeHTTP(w, r)
	})
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

var adminUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (h *Handler) adminUserCount(ctx context.Context) (int, error) {
	var count int
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (h *Handler) adminUserResponse(ctx context.Context, userID string) (map[string]any, error) {
	var idValue, username, role string
	if err := h.DB.QueryRowContext(ctx, `SELECT id, username, role FROM users WHERE id = ?`, userID).Scan(&idValue, &username, &role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NotFound("User")
		}
		return nil, err
	}
	return map[string]any{"id": idValue, "username": username, "role": role}, nil
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	ctx := r.Context()
	targetID := chi.URLParam(r, "id")

	// Mirror the TS admin update-user route ordering exactly:
	//   1. adminUpdateUserSchema.safeParse validates ALL field shapes/bounds up
	//      front (username regex+3..50, password 8..128, role enum) -> 400.
	//   2. at-least-one-field.
	//   3. self-demote (SELF_DEMOTE).
	//   4. last-admin (LAST_ADMIN) — fetches target role only here.
	//   5. username-taken (USERNAME_TAKEN 409).
	//   6. password hash.
	//   7. UPDATE; a missing target surfaces as 404 from the zero-rows result.
	// Go previously interleaved per-field shape checks with business rules and
	// fetched the target up front, so error precedence (e.g. invalid password vs
	// self-demote, username-taken vs self-demote, validation vs 404) diverged.

	// Step 1: validate every present field's shape/bounds before any business rule.
	var (
		username, password, role          string
		hasUsername, hasPassword, hasRole bool
	)
	if raw, ok := body["username"]; ok {
		s, isStr := raw.(string)
		if !isStr || !adminUsernamePattern.MatchString(s) || httpx.JSStringLength(s) < 3 || httpx.JSStringLength(s) > 50 {
			return httpx.ValidationError("username must be 3-50 characters and contain only letters, numbers, hyphens, and underscores")
		}
		username, hasUsername = s, true
	}
	if raw, ok := body["password"]; ok {
		s, isStr := raw.(string)
		if !isStr || httpx.JSStringLength(s) < 8 || httpx.JSStringLength(s) > 128 {
			return httpx.ValidationError("password must be 8-128 characters")
		}
		password, hasPassword = s, true
	}
	if raw, ok := body["role"]; ok {
		s, isStr := raw.(string)
		if !isStr || (s != "admin" && s != "user") {
			return httpx.ValidationError("role must be admin or user")
		}
		role, hasRole = s, true
	}

	// Step 2: at least one field.
	if !hasUsername && !hasPassword && !hasRole {
		// TS: throw new AppError("At least one field is required", 400,
		// "VALIDATION_ERROR"). Go previously used BadRequest (code BAD_REQUEST).
		return httpx.ValidationError("At least one field is required")
	}

	// Step 3: self-demote.
	if hasRole && role != "admin" {
		if claims := auth.UserFromContext(ctx); claims != nil && claims.Subject == targetID {
			return httpx.Error(http.StatusBadRequest, "SELF_DEMOTE", "Cannot demote yourself")
		}
	}

	// Step 4: last-admin (only when demoting someone else who is currently admin).
	if hasRole && role == "user" {
		claims := auth.UserFromContext(ctx)
		if claims == nil || claims.Subject != targetID {
			var targetRole string
			err := h.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, targetID).Scan(&targetRole)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if targetRole == "admin" {
				adminCount, err := h.adminUserCount(ctx)
				if err != nil {
					return err
				}
				if adminCount <= 1 {
					return httpx.Error(http.StatusBadRequest, "LAST_ADMIN", "Cannot demote the last administrator")
				}
			}
		}
	}

	sets := []string{}
	args := []any{}
	if hasRole {
		sets = append(sets, "role = ?")
		args = append(args, role)
	}
	// Step 5: username uniqueness (after role business rules, matching TS).
	if hasUsername {
		username = strings.TrimSpace(username)
		var existingID string
		err := h.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ? AND id != ? LIMIT 1`, username, targetID).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existingID != "" {
			return httpx.Error(http.StatusConflict, "USERNAME_TAKEN", "Username already taken")
		}
		sets = append(sets, "username = ?")
		args = append(args, username)
	}
	// Step 6: password hash.
	if hasPassword {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
		if err != nil {
			return err
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, string(hash))
	}
	args = append(args, targetID)
	res, err := h.DB.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return httpx.NotFound("User")
	}
	user, err := h.adminUserResponse(ctx, targetID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, user)
	return nil
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	targetID := chi.URLParam(r, "id")
	claims := auth.UserFromContext(ctx)
	if claims != nil && claims.Subject == targetID {
		return httpx.Error(http.StatusBadRequest, "SELF_DELETE", "Cannot delete your own account")
	}
	var targetRole string
	if err := h.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, targetID).Scan(&targetRole); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("User")
		}
		return err
	}
	if targetRole == "admin" {
		adminCount, err := h.adminUserCount(ctx)
		if err != nil {
			return err
		}
		if adminCount <= 1 {
			return httpx.Error(http.StatusBadRequest, "LAST_ADMIN", "Cannot delete the last administrator")
		}
	}
	res, err := h.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, targetID)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return httpx.NotFound("User")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) updateAdminSettings(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	// Mirror the TS adminUpdateSettingsSchema (z.object({registrationOpen:
	// z.boolean()})): registrationOpen is REQUIRED and must be a boolean, so a
	// missing or non-boolean value is a 400 VALIDATION_ERROR. Go previously
	// passed body["registrationOpen"] straight into Settings.Patch, which would
	// clobber the persisted boolean to null (missing) or write a non-boolean.
	registrationOpen, ok := body["registrationOpen"].(bool)
	if !ok {
		return httpx.ValidationError("registrationOpen must be a boolean")
	}
	next, err := h.Settings.Patch(map[string]any{"auth": map[string]any{"registrationOpen": registrationOpen}})
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
	var raw map[string]any
	if err := httpx.DecodeJSON(r, &raw); err != nil {
		return err
	}
	// Mirror TS (server/routes/admin.ts): `if (!Array.isArray(ids) || ids.length
	// === 0 || !ids.every(id => typeof id === "string")) throw 400 "ids must be a
	// non-empty string array"`. Go previously decoded into a typed []string (a
	// non-array/non-string value gave a generic "invalid JSON" message) AND ran no
	// non-empty/string check, so an empty array silently returned {results:[]} 200
	// instead of the TS 400.
	idsRaw, ok := raw["ids"].([]any)
	if !ok || len(idsRaw) == 0 {
		return httpx.Error(http.StatusBadRequest, "VALIDATION_ERROR", "ids must be a non-empty string array")
	}
	ids := make([]string, 0, len(idsRaw))
	for _, item := range idsRaw {
		text, isStr := item.(string)
		if !isStr {
			return httpx.Error(http.StatusBadRequest, "VALIDATION_ERROR", "ids must be a non-empty string array")
		}
		ids = append(ids, text)
	}
	results := []map[string]any{}
	for _, id := range ids {
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
	terminalID, err := decodeRequiredTerminalID(r)
	if err != nil {
		return err
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
	terminalID, err := decodeRequiredTerminalID(r)
	if err != nil {
		return err
	}
	return h.reattachTerminalID(w, r, terminalID, "admin.terminal.reattachOrphan")
}

// decodeRequiredTerminalID mirrors the TS admin terminal orphan routes
// (server/routes/admin.ts): `if (!terminalId || typeof terminalId !== "string")
// throw new AppError("terminalId is required", 400, "VALIDATION_ERROR")`. A
// missing, empty, or non-string terminalId rejects with a VALIDATION_ERROR (not
// the generic BAD_REQUEST/"invalid JSON" Go previously produced for a non-string
// value, nor BAD_REQUEST for the missing/empty case).
func decodeRequiredTerminalID(r *http.Request) (string, error) {
	var raw map[string]any
	if err := httpx.DecodeJSON(r, &raw); err != nil {
		return "", err
	}
	terminalID, ok := raw["terminalId"].(string)
	if !ok || terminalID == "" {
		return "", httpx.Error(http.StatusBadRequest, "VALIDATION_ERROR", "terminalId is required")
	}
	return terminalID, nil
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
		"reason":    message,
		"error":     message,
		"message":   message,
	})
}

func (h *Handler) usageHistoryList(w http.ResponseWriter, r *http.Request) error {
	// Mirror TS listQuerySchema: page = z.coerce.number().int().positive()
	// .default(1); pageSize = ...positive().max(100).default(50). Invalid or
	// out-of-range values reject with 400 instead of silently clamping.
	page, err := coercePositiveIntQuery(r, "page", 1, 0)
	if err != nil {
		return err
	}
	pageSize, err := coercePositiveIntQuery(r, "pageSize", 50, 100)
	if err != nil {
		return err
	}
	schema, err := h.usageHistorySchema(r.Context())
	if err != nil {
		return err
	}
	if !schema.hasAPIRequests {
		httpx.WriteJSON(w, http.StatusOK, usageHistoryEmptyList(page, pageSize))
		return nil
	}
	where, args, impossible := usageFiltersForSchema(r.URL.Query(), schema, "", "")
	if impossible {
		httpx.WriteJSON(w, http.StatusOK, usageHistoryEmptyList(page, pageSize))
		return nil
	}
	offset := (page - 1) * pageSize
	var total int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM api_requests ar`+usageHistoryJoins(schema)+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, pageSize, offset)
	query := `SELECT ` + usageHistoryListSelect(schema) + ` FROM api_requests ar` + usageHistoryJoins(schema) + where + ` ORDER BY ` + usageHistoryOrderExpr(schema) + ` LIMIT ? OFFSET ?`
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
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
	schema, err := h.usageHistorySchema(r.Context())
	if err != nil {
		return err
	}
	if !schema.hasAPIRequests || !schema.apiColumn("provider") {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": []string{}})
		return nil
	}
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
	return rows.Err()
}

func (h *Handler) usageHistoryStats(w http.ResponseWriter, r *http.Request) error {
	endDate := parseUsageDate(r.URL.Query().Get("endDate"), time.Now().UTC())
	startDate := parseUsageDate(r.URL.Query().Get("startDate"), endDate.AddDate(0, 0, -90))
	bounded := strings.TrimSpace(r.URL.Query().Get("startDate")) == "" || strings.TrimSpace(r.URL.Query().Get("endDate")) == ""
	if startDate.After(endDate) {
		startDate = endDate
	}
	schema, err := h.usageHistorySchema(r.Context())
	if err != nil {
		return err
	}
	if !schema.hasAPIRequests {
		httpx.WriteJSON(w, http.StatusOK, usageHistoryStatsPayload(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, bounded, startDate, endDate))
		return nil
	}
	where, args, impossible := usageFiltersForSchema(r.URL.Query(), schema, startDate.Format(time.RFC3339), endDate.Format(time.RFC3339))
	if impossible {
		httpx.WriteJSON(w, http.StatusOK, usageHistoryStatsPayload(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, bounded, startDate, endDate))
		return nil
	}
	query := `SELECT COUNT(*), ` + usageSumExpr(schema, "input_tokens") + `, ` + usageSumExpr(schema, "output_tokens") + `, ` + usageSumExpr(schema, "cache_creation_input_tokens") + `, ` + usageSumExpr(schema, "cached_input_tokens") + `, ` + usageSumExpr(schema, "cache_creation_5m_tokens") + `, ` + usageSumExpr(schema, "cache_creation_1h_tokens") + `, ` + usageSumExpr(schema, "reasoning_tokens") + `, ` + usageSumExpr(schema, "cost_usd") + `, ` + usageAvgExpr(schema, "duration_ms") + `, ` + usageAvgExpr(schema, "ttft_ms") + ` FROM api_requests ar` + usageHistoryJoins(schema) + where
	var total, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning int
	var cost, avgDuration, avgTtft float64
	if err := h.DB.QueryRowContext(r.Context(), query, args...).Scan(&total, &input, &output, &cacheCreate, &cacheRead, &cache5m, &cache1h, &reasoning, &cost, &avgDuration, &avgTtft); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, usageHistoryStatsPayload(total, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning, cost, avgDuration, avgTtft, bounded, startDate, endDate))
	return nil
}

func (h *Handler) usageHistoryTimeseries(w http.ResponseWriter, r *http.Request) error {
	// Mirror TS timeSeriesQuerySchema: granularity =
	// z.enum(["hour","day","month"]).default("day"). An absent param defaults to
	// "day"; a present but out-of-enum value rejects with 400 rather than
	// silently falling back to "day".
	granularity := "day"
	if r.URL.Query().Has("granularity") {
		granularity = r.URL.Query().Get("granularity")
		if granularity != "hour" && granularity != "day" && granularity != "month" {
			return httpx.BadRequest("granularity must be one of hour, day, month")
		}
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
	schema, err := h.usageHistorySchema(r.Context())
	if err != nil {
		return err
	}
	byBucket := map[string]map[string]any{}
	if schema.hasAPIRequests && schema.apiColumn("created_at") {
		where, args, impossible := usageFiltersForSchema(r.URL.Query(), schema, effectiveStart.Format(time.RFC3339), requestedEnd.Format(time.RFC3339))
		if !impossible {
			bucketExpr := usageBucketExpression(granularity)
			query := `SELECT ` + bucketExpr + ` AS bucket, COUNT(*), ` + usageSumExpr(schema, "input_tokens") + `, ` + usageSumExpr(schema, "output_tokens") + `, ` + usageSumExpr(schema, "cache_creation_input_tokens") + `, ` + usageSumExpr(schema, "cached_input_tokens") + `, ` + usageSumExpr(schema, "cache_creation_5m_tokens") + `, ` + usageSumExpr(schema, "cache_creation_1h_tokens") + `, ` + usageSumExpr(schema, "reasoning_tokens") + `, ` + usageSumExpr(schema, "cost_usd") + `, ` + usageAvgExpr(schema, "duration_ms") + `, ` + usageAvgExpr(schema, "ttft_ms") + `, ` + usageErrorCountExpr(schema) + `, ` + usageSumExpr(schema, "meter_usage") + `, ` + usageMeterUnitExpr(schema) + ` FROM api_requests ar` + usageHistoryJoins(schema) + where + ` GROUP BY bucket ORDER BY bucket`
			rows, err := h.DB.QueryContext(r.Context(), query, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
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
			if err := rows.Err(); err != nil {
				return err
			}
		}
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
	return nil
}

func (h *Handler) usageHistoryGet(w http.ResponseWriter, r *http.Request) error {
	schema, err := h.usageHistorySchema(r.Context())
	if err != nil {
		return err
	}
	if !schema.hasAPIRequests || !schema.apiColumn("id") {
		return httpx.NotFound("Usage record")
	}
	query := `SELECT ` + usageHistoryDetailSelect(schema) + ` FROM api_requests ar` + usageHistoryJoins(schema) + ` WHERE ar.id = ?`
	rows, err := h.DB.QueryContext(r.Context(), query, chi.URLParam(r, "id"))
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

type usageHistorySchemaInfo struct {
	apiRequestsColumns map[string]bool
	narratorColumns    map[string]bool
	chapterColumns     map[string]bool
	hasAPIRequests     bool
}

func (h *Handler) usageHistorySchema(ctx context.Context) (usageHistorySchemaInfo, error) {
	var schema usageHistorySchemaInfo
	if h == nil || h.DB == nil {
		return schema, nil
	}
	apiColumns, err := db.TableColumnSet(ctx, h.DB, "api_requests")
	if err != nil {
		return schema, err
	}
	schema.apiRequestsColumns = apiColumns
	schema.hasAPIRequests = len(apiColumns) > 0
	if narratorColumns, err := db.TableColumnSet(ctx, h.DB, "narrators"); err != nil {
		return schema, err
	} else {
		schema.narratorColumns = narratorColumns
	}
	if chapterColumns, err := db.TableColumnSet(ctx, h.DB, "chapters"); err != nil {
		return schema, err
	} else {
		schema.chapterColumns = chapterColumns
	}
	return schema, nil
}

func (s usageHistorySchemaInfo) apiColumn(column string) bool {
	return s.apiRequestsColumns[column]
}

func (s usageHistorySchemaInfo) canJoinNarrators() bool {
	return s.apiColumn("narrator_id") && s.narratorColumns["id"]
}

func (s usageHistorySchemaInfo) canJoinChapters() bool {
	return s.canJoinNarrators() && s.narratorColumns["chapter_id"] && s.chapterColumns["id"]
}

func usageHistoryJoins(schema usageHistorySchemaInfo) string {
	joins := ""
	if schema.canJoinNarrators() {
		joins += " LEFT JOIN narrators n ON n.id = ar.narrator_id"
	}
	if schema.canJoinChapters() {
		joins += " LEFT JOIN chapters c ON c.id = n.chapter_id"
	}
	return joins
}

func usageHistoryEmptyList(page, pageSize int) map[string]any {
	return map[string]any{"records": []map[string]any{}, "total": 0, "page": page, "pageSize": pageSize, "totalPages": 0}
}

func usageHistoryStatsPayload(total, input, output, cacheCreate, cacheRead, cache5m, cache1h, reasoning int, cost, avgDuration, avgTtft float64, bounded bool, startDate, endDate time.Time) map[string]any {
	return map[string]any{"totalRequests": total, "totalInputTokens": input, "totalOutputTokens": output, "totalCacheCreationTokens": cacheCreate, "totalCacheReadTokens": cacheRead, "totalCacheCreation5mTokens": cache5m, "totalCacheCreation1hTokens": cache1h, "totalReasoningTokens": reasoning, "totalTokens": input + output + cacheCreate + cacheRead, "totalCost": cost, "totalCostUsd": cost, "averageDurationMs": avgDuration, "averageTtftMs": avgTtft, "bounded": bounded, "effectiveStartDate": startDate.Format(time.RFC3339), "effectiveEndDate": endDate.Format(time.RFC3339)}
}

func usageHistoryListSelect(schema usageHistorySchemaInfo) string {
	return strings.Join([]string{
		usageSelectString(schema, "id"),
		usageSelectString(schema, "narrator_id"),
		usageSelectString(schema, "message_id"),
		usageSelectString(schema, "kind"),
		usageSelectString(schema, "provider"),
		usageSelectString(schema, "credential_id"),
		usageSelectString(schema, "model"),
		usageSelectInt(schema, "input_tokens"),
		usageSelectInt(schema, "output_tokens"),
		usageSelectInt(schema, "cached_input_tokens"),
		usageSelectInt(schema, "cache_creation_input_tokens"),
		usageSelectInt(schema, "cache_creation_5m_tokens"),
		usageSelectInt(schema, "cache_creation_1h_tokens"),
		usageSelectInt(schema, "reasoning_tokens"),
		usageSelectNullable(schema, "ttft_ms"),
		usageSelectNullable(schema, "duration_ms"),
		usageSelectNullable(schema, "cost_usd"),
		usageSelectNullable(schema, "context_percent"),
		usageSelectNullable(schema, "meter_usage"),
		usageSelectString(schema, "meter_unit"),
		usageSelectString(schema, "error_message"),
		usageHasRawDumpExpr(schema),
		usageRawDumpBytesExpr(schema),
		usageSelectString(schema, "created_at"),
		usageNarratorTitleExpr(schema),
		usageChapterTitleExpr(schema),
		usageChapterIDExpr(schema),
		usageProjectIDExpr(schema),
	}, ",")
}

func usageHistoryDetailSelect(schema usageHistorySchemaInfo) string {
	return strings.Join([]string{
		usageSelectString(schema, "id"),
		usageSelectString(schema, "narrator_id"),
		usageSelectString(schema, "message_id"),
		usageSelectString(schema, "kind"),
		usageSelectString(schema, "provider"),
		usageSelectString(schema, "credential_id"),
		usageSelectString(schema, "model"),
		usageSelectInt(schema, "input_tokens"),
		usageSelectInt(schema, "output_tokens"),
		usageSelectInt(schema, "cached_input_tokens"),
		usageSelectInt(schema, "cache_creation_input_tokens"),
		usageSelectInt(schema, "cache_creation_5m_tokens"),
		usageSelectInt(schema, "cache_creation_1h_tokens"),
		usageSelectInt(schema, "reasoning_tokens"),
		usageSelectNullable(schema, "ttft_ms"),
		usageSelectNullable(schema, "duration_ms"),
		usageSelectNullable(schema, "cost_usd"),
		usageSelectNullable(schema, "context_percent"),
		usageSelectNullable(schema, "meter_usage"),
		usageSelectString(schema, "meter_unit"),
		usageSelectString(schema, "error_message"),
		usageSelectString(schema, "raw_dump_json"),
		usageSelectString(schema, "created_at"),
		usageNarratorTitleExpr(schema),
		usageChapterTitleExpr(schema),
		usageChapterIDExpr(schema),
		usageProjectIDExpr(schema),
	}, ",")
}

func usageSelectString(schema usageHistorySchemaInfo, column string) string {
	if schema.apiColumn(column) {
		return "ar." + column
	}
	return "NULL"
}

func usageSelectInt(schema usageHistorySchemaInfo, column string) string {
	if schema.apiColumn(column) {
		return "COALESCE(ar." + column + ",0)"
	}
	return "0"
}

func usageSelectNullable(schema usageHistorySchemaInfo, column string) string {
	if schema.apiColumn(column) {
		return "ar." + column
	}
	return "NULL"
}

func usageHasRawDumpExpr(schema usageHistorySchemaInfo) string {
	if schema.apiColumn("raw_dump_json") {
		return "CASE WHEN ar.raw_dump_json IS NOT NULL AND ar.raw_dump_json != '' THEN 1 ELSE 0 END"
	}
	return "0"
}

func usageRawDumpBytesExpr(schema usageHistorySchemaInfo) string {
	if schema.apiColumn("raw_dump_json") {
		return "COALESCE(LENGTH(ar.raw_dump_json),0)"
	}
	return "0"
}

func usageNarratorTitleExpr(schema usageHistorySchemaInfo) string {
	if schema.canJoinNarrators() && schema.narratorColumns["title"] {
		return "n.title"
	}
	return "NULL"
}

func usageChapterTitleExpr(schema usageHistorySchemaInfo) string {
	if schema.canJoinChapters() && schema.chapterColumns["title"] {
		return "c.title"
	}
	return "NULL"
}

func usageChapterIDExpr(schema usageHistorySchemaInfo) string {
	if schema.canJoinNarrators() && schema.narratorColumns["chapter_id"] {
		return "n.chapter_id"
	}
	return "NULL"
}

func usageProjectIDExpr(schema usageHistorySchemaInfo) string {
	if schema.canJoinChapters() && schema.chapterColumns["project_id"] {
		return "c.project_id"
	}
	return "NULL"
}

func usageHistoryOrderExpr(schema usageHistorySchemaInfo) string {
	if schema.apiColumn("created_at") {
		if schema.apiColumn("id") {
			return "ar.created_at DESC, ar.id DESC"
		}
		return "ar.created_at DESC"
	}
	if schema.apiColumn("id") {
		return "ar.id DESC"
	}
	return "1"
}

func usageSumExpr(schema usageHistorySchemaInfo, column string) string {
	if schema.apiColumn(column) {
		return "COALESCE(SUM(ar." + column + "),0)"
	}
	return "0"
}

func usageAvgExpr(schema usageHistorySchemaInfo, column string) string {
	if schema.apiColumn(column) {
		return "COALESCE(AVG(ar." + column + "),0)"
	}
	return "0"
}

func usageErrorCountExpr(schema usageHistorySchemaInfo) string {
	if schema.apiColumn("error_message") {
		return "COALESCE(SUM(CASE WHEN ar.error_message IS NOT NULL AND TRIM(ar.error_message) != '' THEN 1 ELSE 0 END),0)"
	}
	return "0"
}

func usageMeterUnitExpr(schema usageHistorySchemaInfo) string {
	if schema.apiColumn("meter_unit") {
		return "CASE WHEN COUNT(DISTINCT ar.meter_unit) = 1 THEN MAX(ar.meter_unit) WHEN COUNT(DISTINCT ar.meter_unit) > 1 THEN 'mixed' ELSE NULL END"
	}
	return "NULL"
}

func usageFiltersForSchema(values url.Values, schema usageHistorySchemaInfo, startDateOverride, endDateOverride string) (string, []any, bool) {
	clauses := []string{}
	args := []any{}
	for _, filter := range []struct{ key, column string }{{"narratorId", "narrator_id"}, {"provider", "provider"}, {"kind", "kind"}} {
		if value := strings.TrimSpace(values.Get(filter.key)); value != "" {
			if !schema.apiColumn(filter.column) {
				return "", nil, true
			}
			clauses = append(clauses, "ar."+filter.column+" = ?")
			args = append(args, value)
		}
	}
	if value := strings.TrimSpace(values.Get("model")); value != "" {
		if !schema.apiColumn("model") {
			return "", nil, true
		}
		clauses = append(clauses, "ar.model LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(values.Get("chapterId")); value != "" {
		if !schema.canJoinNarrators() || !schema.narratorColumns["chapter_id"] {
			return "", nil, true
		}
		clauses = append(clauses, "n.chapter_id = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(values.Get("projectId")); value != "" {
		if !schema.canJoinChapters() || !schema.chapterColumns["project_id"] {
			return "", nil, true
		}
		clauses = append(clauses, "c.project_id = ?")
		args = append(args, value)
	}
	startDate := firstNonEmpty(startDateOverride, strings.TrimSpace(values.Get("startDate")))
	endDate := firstNonEmpty(endDateOverride, strings.TrimSpace(values.Get("endDate")))
	if startDate != "" {
		if !schema.apiColumn("created_at") {
			return "", nil, true
		}
		clauses = append(clauses, "ar.created_at >= ?")
		args = append(args, startDate)
	}
	if endDate != "" {
		if !schema.apiColumn("created_at") {
			return "", nil, true
		}
		clauses = append(clauses, "ar.created_at <= ?")
		args = append(args, endDate)
	}
	if len(clauses) == 0 {
		return "", args, false
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, false
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
		return `substr(ar.created_at, 1, 13) || ':00:00.000Z'`
	case "month":
		return `substr(ar.created_at, 1, 7) || '-01T00:00:00.000Z'`
	default:
		return `substr(ar.created_at, 1, 10) || 'T00:00:00.000Z'`
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
		var id, narratorID, messageID, kind, provider, credentialID, model, meterUnit, errMsg, created sql.NullString
		var narratorTitle, chapterTitle, chapterID, projectID sql.NullString
		var input, output, cached, cacheCreate, cache5m, cache1h, reasoning int
		var hasRawDump, rawDumpBytes int
		var ttft, duration sql.NullInt64
		var cost, contextPercent, meter sql.NullFloat64
		if err := rows.Scan(&id, &narratorID, &messageID, &kind, &provider, &credentialID, &model, &input, &output, &cached, &cacheCreate, &cache5m, &cache1h, &reasoning, &ttft, &duration, &cost, &contextPercent, &meter, &meterUnit, &errMsg, &hasRawDump, &rawDumpBytes, &created, &narratorTitle, &chapterTitle, &chapterID, &projectID); err != nil {
			return nil, err
		}
		out = append(out, usageRecordMap(id, narratorID, messageID, kind, provider, credentialID, model, input, output, cached, cacheCreate, cache5m, cache1h, reasoning, ttft, duration, cost, contextPercent, meter, meterUnit, errMsg, sql.NullString{}, created, narratorTitle, chapterTitle, chapterID, projectID, hasRawDump != 0, rawDumpBytes))
	}
	return out, rows.Err()
}

func usageRows(rows *sql.Rows) ([]map[string]any, error) {
	out := []map[string]any{}
	for rows.Next() {
		var id, narratorID, messageID, kind, provider, credentialID, model, meterUnit, errMsg, raw, created sql.NullString
		var narratorTitle, chapterTitle, chapterID, projectID sql.NullString
		var input, output, cached, cacheCreate, cache5m, cache1h, reasoning int
		var ttft, duration sql.NullInt64
		var cost, contextPercent, meter sql.NullFloat64
		if err := rows.Scan(&id, &narratorID, &messageID, &kind, &provider, &credentialID, &model, &input, &output, &cached, &cacheCreate, &cache5m, &cache1h, &reasoning, &ttft, &duration, &cost, &contextPercent, &meter, &meterUnit, &errMsg, &raw, &created, &narratorTitle, &chapterTitle, &chapterID, &projectID); err != nil {
			return nil, err
		}
		out = append(out, usageRecordMap(id, narratorID, messageID, kind, provider, credentialID, model, input, output, cached, cacheCreate, cache5m, cache1h, reasoning, ttft, duration, cost, contextPercent, meter, meterUnit, errMsg, raw, created, narratorTitle, chapterTitle, chapterID, projectID, raw.Valid && raw.String != "", len(raw.String)))
	}
	return out, rows.Err()
}

func usageRecordMap(id, narratorID, messageID, kind, provider, credentialID, model sql.NullString, input, output, cached, cacheCreate, cache5m, cache1h, reasoning int, ttft, duration sql.NullInt64, cost, contextPercent, meter sql.NullFloat64, meterUnit, errMsg, raw, created, narratorTitle, chapterTitle, chapterID, projectID sql.NullString, hasRawDump bool, rawDumpBytes int) map[string]any {
	record := map[string]any{"id": id.String, "narratorId": nullableString(narratorID), "messageId": nullableString(messageID), "kind": stringDefault(kind, "narrator"), "provider": nullableString(provider), "credentialId": nullableString(credentialID), "credentialName": nullableString(credentialID), "model": nullableString(model), "inputTokens": input, "outputTokens": output, "cachedInputTokens": cached, "cacheCreationInputTokens": cacheCreate, "cacheCreation5mTokens": cache5m, "cacheCreation1hTokens": cache1h, "reasoningTokens": reasoning, "ttftMs": nullableInt(ttft), "durationMs": nullableInt(duration), "costUsd": nullableFloat(cost), "contextPercent": nullableFloat(contextPercent), "meterUsage": nullableFloat(meter), "meterUnit": nullableString(meterUnit), "errorMessage": nullableString(errMsg), "hasRawDump": hasRawDump, "rawDumpBytes": rawDumpBytes, "createdAt": created.String, "narratorTitle": nullableString(narratorTitle), "chapterTitle": nullableString(chapterTitle), "chapterId": nullableString(chapterID), "projectId": nullableString(projectID)}
	if hasRawDump {
		record["rawDump"] = parseUsageRawDump(raw)
	} else {
		record["rawDump"] = nil
	}
	return record
}

func parseUsageRawDump(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(raw.String), &parsed); err != nil {
		return map[string]any{"invalidJson": true, "rawText": raw.String}
	}
	return parsed
}
func intQuery(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// coercePositiveIntQuery mirrors a Zod z.coerce.number().int().positive()[.max(max)]
// .default(fallback) query param. An ABSENT param uses the default; a present
// param is coerced like JavaScript Number() (whole-string numeric, so "50x" ->
// NaN and "5.5" -> 5.5), then rejected with 400 unless it is a positive integer
// within the optional max. maxValue <= 0 means no upper bound. This matches the
// TS usage-history listQuerySchema, which throws a ValidationError (400) on
// invalid/out-of-range page/pageSize rather than silently clamping.
func coercePositiveIntQuery(r *http.Request, key string, fallback, maxValue int) (int, error) {
	raw := r.URL.Query().Get(key)
	if strings.TrimSpace(raw) == "" && !r.URL.Query().Has(key) {
		return fallback, nil
	}
	// Mirror Zod z.coerce.number().int().positive(): coerce like JS Number()
	// (httpx.CoerceIntJS handles whitespace trim, empty->0, scientific notation,
	// and hex/octal/binary integer literals such as "0x10"=16 that strconv.ParseFloat
	// rejects), then require a positive integer. A present-empty "key=" coerces to
	// Number("")===0 and fails .positive() -> 400.
	value, ok := httpx.CoerceIntJS(raw)
	if !ok || value <= 0 {
		return 0, httpx.BadRequest(key + " must be a positive integer")
	}
	if maxValue > 0 && value > maxValue {
		return 0, httpx.BadRequest(key + " must be at most " + strconv.Itoa(maxValue))
	}
	return value, nil
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

var (
	codexRefreshTokenPattern = regexp.MustCompile(`rt_[A-Za-z0-9._-]+`)
	codexEmailPattern        = regexp.MustCompile(`(?i)[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}`)
)

func (h *Handler) clineRecommendedModels(w http.ResponseWriter, r *http.Request) error {
	refresh := r.URL.Query().Get("refresh") == "1" || strings.EqualFold(r.URL.Query().Get("refresh"), "true")
	if refresh {
		data, err := getJSON(r.Context(), clineRecommendedModelsURL(), map[string]string{})
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
	var raw map[string]any
	if err := httpx.DecodeLooseJSON(r, &raw); err != nil {
		return err
	}
	// Mirror TS `if (!Array.isArray(body.models)) return 400 "models must be an
	// array"`: a missing OR non-array `models` (string/number/object/null) must
	// reject with this exact message. Go previously decoded into a typed
	// `struct{ Models []string }`, so a non-array value failed the unmarshal with a
	// generic "invalid JSON: ..." message instead of the TS message.
	modelsRaw, ok := raw["models"].([]any)
	if !ok {
		return httpx.BadRequest("models must be an array")
	}
	models := make([]string, 0, len(modelsRaw))
	for _, item := range modelsRaw {
		if text, ok := item.(string); ok {
			models = append(models, text)
		}
	}
	count, modelContextWindows, err := h.updateClineEnabledModels(models)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "count": count, "modelContextWindows": modelContextWindows})
	return nil
}

func (h *Handler) updateClineEnabledModels(models []string) (int, map[string]any, error) {
	if err := h.ensureDefaultClineProvider(""); err != nil {
		return 0, nil, err
	}
	if h.Settings == nil {
		return 0, nil, httpx.BadRequest("Settings unavailable")
	}
	raw := h.Settings.Raw()
	providers, _ := raw["clineProviders"].([]any)
	if len(providers) == 0 {
		return 0, nil, httpx.BadRequest("No Cline provider available")
	}
	modelValues := make([]any, 0, len(models))
	modelIDs := make([]string, 0, len(models))
	for _, model := range models {
		modelID := strings.TrimSpace(model)
		if modelID != "" {
			modelValues = append(modelValues, modelID)
			modelIDs = append(modelIDs, modelID)
		}
	}
	patch := map[string]any{"clineProviders": providers}
	if provider, ok := providers[0].(map[string]any); ok {
		provider["enabledModels"] = modelValues
		if provider["defaultModel"] == nil && len(modelValues) > 0 {
			provider["defaultModel"] = modelValues[0]
		}
		if agent, ok := raw["agent"].(map[string]any); ok {
			windows, _ := agent["modelContextWindows"].(map[string]any)
			if windows == nil {
				windows = map[string]any{}
			}
			cache, _ := loadProviderModelCache("cline")
			prefix := firstNonEmpty(stringMap(provider, "prefix", ""), "cline")
			changedWindows := false
			for _, modelID := range modelIDs {
				if windows[prefix+":"+modelID] != nil {
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
		return 0, nil, err
	}
	modelContextWindows := map[string]any{}
	if agent, ok := patched["agent"].(map[string]any); ok {
		if windows, ok := agent["modelContextWindows"].(map[string]any); ok {
			modelContextWindows = windows
		}
	}
	return len(modelValues), modelContextWindows, nil
}

func (h *Handler) autoAddClineFreeModels() error {
	if err := h.ensureDefaultClineProvider(""); err != nil {
		return err
	}
	if h.Settings == nil {
		return nil
	}
	recommended, err := loadClineRecommendedCache()
	if err != nil || recommended == nil {
		recommended = clineRecommendedFallback()
	}
	freeModels, _ := recommended["free"].([]any)
	if len(freeModels) == 0 {
		return nil
	}
	raw := h.Settings.Raw()
	providers, _ := raw["clineProviders"].([]any)
	if len(providers) == 0 {
		return nil
	}
	provider, ok := providers[0].(map[string]any)
	if !ok {
		return nil
	}
	enabled := stringList(provider["enabledModels"])
	existing := make(map[string]bool, len(enabled))
	for _, modelID := range enabled {
		existing[modelID] = true
	}
	changed := false
	for _, item := range freeModels {
		model, ok := item.(map[string]any)
		if !ok {
			continue
		}
		modelID := stringMap(model, "id", "")
		if modelID == "" || existing[modelID] {
			continue
		}
		enabled = append(enabled, modelID)
		existing[modelID] = true
		changed = true
	}
	if !changed {
		return nil
	}
	_, _, err = h.updateClineEnabledModels(enabled)
	return err
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
	baseURL := clineAPIBaseURL()
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, clineAPIBaseURL()+"/api/v1/auth/refresh", bytes.NewReader(payload))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clineAPIBaseURL()+"/api/v1/users/me", nil)
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
	cache, _ := loadProviderModelCache("cline")
	totalModels := len(aggregateProviderModels("cline", h.providerConfigs("cline"), cache))
	payload := map[string]any{
		"authenticated": authenticated,
		"providers":     providers,
		"totalModels":   totalModels,
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
	_, _ = h.refreshClineUserInfo(r.Context())
	if err := h.autoAddClineFreeModels(); err != nil {
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
	baseURL := strings.TrimRight(firstNonEmpty(body.APIBaseURL, clineAPIBaseURL()), "/")
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
		if err == nil {
			_, _ = h.refreshClineUserInfo(r.Context())
		}
		if err == nil {
			err = h.autoAddClineFreeModels()
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
	return h.codexSetBoolSetting(w, r, "useWebSocket")
}

func (h *Handler) codexSetUseWebSearch(w http.ResponseWriter, r *http.Request) error {
	return h.codexSetBoolSetting(w, r, "useWebSearch")
}

func (h *Handler) codexSetUseImageGeneration(w http.ResponseWriter, r *http.Request) error {
	return h.codexSetBoolSetting(w, r, "useImageGeneration")
}

func (h *Handler) codexSetBoolSetting(w http.ResponseWriter, r *http.Request, key string) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	value := true
	if raw, exists := body[key]; exists {
		parsed, ok := raw.(bool)
		if !ok {
			return httpx.BadRequest(key + " must be boolean")
		}
		value = parsed
	}
	if err := h.patchCodexSetting(key, value); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, key: value})
	return nil
}

func (h *Handler) codexSetLoadBalancingMode(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	mode := stringMap(body, "mode", "")
	if mode != "priority" && mode != "balanced" && mode != "tier-balanced" {
		return httpx.BadRequest("Invalid mode. Must be 'priority', 'balanced', or 'tier-balanced'")
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
	if len(tierOrder) > 6 {
		return httpx.BadRequest("tierOrder must contain at most 6 items")
	}
	allowed := map[string]bool{"free": true, "plus": true, "team": true, "prolite": true, "pro": true, "other": true}
	for _, item := range tierOrder {
		value, ok := item.(string)
		if !ok || !allowed[value] {
			return httpx.BadRequest("Invalid tierOrder")
		}
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
		return httpx.NotImplemented("providers.codex.settingsStore")
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
	if len(out) == 0 {
		return []any{"pro", "prolite", "plus", "team", "free"}
	}
	return out
}

func codexEffectiveTierOrder(tierOrder []any) []any {
	out := append([]any{}, normalizeCodexTierOrder(tierOrder)...)
	seen := map[string]bool{}
	for _, value := range out {
		if tier, ok := value.(string); ok {
			seen[tier] = true
		}
	}
	for _, tier := range []string{"pro", "prolite", "plus", "team", "free", "other"} {
		if !seen[tier] {
			out = append(out, tier)
			seen[tier] = true
		}
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
		delete(entry, "quotaResetsAt")
		delete(entry, "lastError")
		delete(entry, "lastRuntimeError")
		delete(entry, "lastRuntimeErrorAt")
		entry["successCount"] = float64(0)
		entry["failureCount"] = float64(0)
	})
	if err != nil {
		return err
	}
	if !updated {
		writeCodexCredentialNotFound(w, http.StatusNotFound, idParam)
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) codexCredentialUpdate(w http.ResponseWriter, r *http.Request) error {
	body, err := decodeCodexCredentialUpdateBody(r)
	if err != nil {
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
		writeCodexCredentialNotFound(w, http.StatusNotFound, idParam)
		return nil
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
		writeCodexCredentialNotFound(w, http.StatusNotFound, idParam)
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) codexCredentialDeleteUnhealthy(w http.ResponseWriter, r *http.Request) error {
	removed, err := removeCodexUnhealthyCredentials()
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed, "reasons": []string{"too_many_failures", "banned"}})
	return nil
}

func (h *Handler) codexCredentialBatchDelete(w http.ResponseWriter, r *http.Request) error {
	var raw map[string]any
	if err := httpx.DecodeLooseJSON(r, &raw); err != nil {
		return err
	}
	// Mirror TS `if (!Array.isArray(ids) || ids.length === 0) throw 400 "ids must
	// be a non-empty array"`: a missing OR non-array `ids` (string/number/object)
	// must reject with this exact message. Go previously decoded into a typed
	// `struct{ IDs []string }`, so a non-array value failed the unmarshal with a
	// generic "invalid JSON: ..." message instead of the TS message.
	idsRaw, ok := raw["ids"].([]any)
	if !ok || len(idsRaw) == 0 {
		return httpx.BadRequest("ids must be a non-empty array")
	}
	ids := make([]string, 0, len(idsRaw))
	for _, item := range idsRaw {
		if text, ok := item.(string); ok {
			ids = append(ids, text)
		}
	}
	removed, err := removeCodexCredentials(ids)
	if err != nil {
		return err
	}
	removedSet := map[string]bool{}
	for _, item := range removed {
		removedSet[item] = true
	}
	notFound := []string{}
	for _, item := range ids {
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
	writeCodexCredentialNotFound(w, http.StatusBadRequest, idParam)
	return nil
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
		accessToken := strings.TrimSpace(stringMap(entry, "accessToken", ""))
		expiresAt := numericMapOr(entry, "expiresAt", 0)
		if accessToken == "" || (expiresAt > 0 && expiresAt < float64(time.Now().UnixMilli()+30000)) {
			if strings.TrimSpace(stringMap(entry, "refreshToken", "")) == "" {
				return httpx.BadRequest("Access token is expired or unavailable and no refresh token is available")
			}
			refreshed, err := refreshCodexCredentialToken(r.Context(), entry)
			if err != nil {
				return httpx.BadRequest(err.Error())
			}
			entry = mergeCredentialMetadata(entry, refreshed)
			entries[i] = entry
			_ = saveCodexCredentials(entries)
			accessToken = strings.TrimSpace(stringMap(entry, "accessToken", ""))
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
	writeCodexCredentialNotFound(w, http.StatusBadRequest, idParam)
	return nil
}

func writeCodexCredentialNotFound(w http.ResponseWriter, status int, idParam string) {
	httpx.WriteJSON(w, status, map[string]any{"error": fmt.Sprintf("Credential not found: %s", idParam)})
}

func decodeCodexCredentialUpdateBody(r *http.Request) (map[string]any, error) {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest || !strings.HasPrefix(appErr.Message, "invalid JSON:") {
			return nil, err
		}
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, nil
}

func (h *Handler) updateCodexCredentialFlag(w http.ResponseWriter, idParam string, disabled bool) error {
	updated, err := mutateCodexCredential(idParam, func(entry map[string]any) {
		entry["disabled"] = disabled
		if !disabled {
			delete(entry, "disabledReason")
			delete(entry, "quotaResetsAt")
			entry["failureCount"] = float64(0)
		} else {
			entry["disabledReason"] = "manual"
		}
	})
	if err != nil {
		return err
	}
	if !updated {
		writeCodexCredentialNotFound(w, http.StatusNotFound, idParam)
		return nil
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

func isCodexUnhealthyCredential(entry map[string]any) bool {
	if !boolMap(entry, "disabled") {
		return false
	}
	switch stringMap(entry, "disabledReason", "") {
	case "too_many_failures", "banned":
		return true
	default:
		return false
	}
}

func removeCodexUnhealthyCredentials() ([]string, error) {
	entries, err := loadCodexCredentials()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, entry := range entries {
		if isCodexUnhealthyCredential(entry) {
			ids = append(ids, stringMap(entry, "id", ""))
		}
	}
	return removeCodexCredentials(ids)
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

func codexImportCredentialsFromBody(items []any, texts ...string) []map[string]any {
	credentials := []map[string]any{}
	for _, item := range items {
		switch value := item.(type) {
		case map[string]any:
			credentials = append(credentials, codexImportCredentialsFromMap(value)...)
		case string:
			credentials = append(credentials, codexImportCredentialsFromText(value)...)
		}
	}
	for _, text := range texts {
		credentials = append(credentials, codexImportCredentialsFromText(text)...)
	}
	return credentials
}

func codexImportCredentialsFromMap(value map[string]any) []map[string]any {
	if accounts, ok := value["accounts"].([]any); ok {
		return codexImportCredentialsFromBody(accounts)
	}
	if credentials, ok := value["credentials"].([]any); ok {
		return codexImportCredentialsFromBody(credentials)
	}
	return []map[string]any{value}
}

func codexImportCredentialsFromText(text string) []map[string]any {
	text = strings.TrimSpace(text)
	if text == "" {
		return []map[string]any{}
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		var parsed any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			switch value := parsed.(type) {
			case map[string]any:
				return codexImportCredentialsFromMap(value)
			case []any:
				return codexImportCredentialsFromBody(value)
			case string:
				return codexImportCredentialsFromText(value)
			}
		}
	}

	credentials := []map[string]any{}
	for _, record := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == '\n' }) {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		email := codexEmailFromText(record)
		for _, refreshToken := range codexRefreshTokenPattern.FindAllString(record, -1) {
			credential := map[string]any{"refreshToken": refreshToken}
			if email != "" {
				credential["email"] = email
				credential["displayName"] = email
			}
			credentials = append(credentials, credential)
		}
		parts := strings.Split(record, "----")
		for i, part := range parts {
			if !strings.EqualFold(strings.TrimSpace(part), "at") || i+1 >= len(parts) {
				continue
			}
			accessToken := strings.TrimSpace(parts[i+1])
			if accessToken == "" {
				continue
			}
			credential := map[string]any{"accessToken": accessToken}
			if email != "" {
				credential["email"] = email
				credential["displayName"] = email
			}
			credentials = append(credentials, credential)
		}
	}
	return credentials
}

func codexEmailFromText(value string) string {
	match := codexEmailPattern.FindString(value)
	return strings.TrimSpace(match)
}

func codexMap(value map[string]any, key string) map[string]any {
	if nested, ok := value[key].(map[string]any); ok {
		return nested
	}
	return map[string]any{}
}

func codexCredentialFromImport(credential map[string]any, defaultPriority float64) map[string]any {
	nestedCredentials := codexMap(credential, "credentials")
	extra := codexMap(credential, "extra")
	refreshToken := codexFirstString(
		stringMap(credential, "refreshToken", ""),
		stringMap(credential, "refresh_token", ""),
		stringMap(nestedCredentials, "refreshToken", ""),
		stringMap(nestedCredentials, "refresh_token", ""),
	)
	accessToken := codexFirstString(
		stringMap(credential, "accessToken", ""),
		stringMap(credential, "access_token", ""),
		stringMap(nestedCredentials, "accessToken", ""),
		stringMap(nestedCredentials, "access_token", ""),
	)
	if refreshToken == "" && accessToken == "" {
		return nil
	}
	info := codexTokenInfo("", accessToken)
	accountID := codexFirstString(
		stringMap(credential, "accountId", ""),
		stringMap(credential, "account_id", ""),
		stringMap(nestedCredentials, "accountId", ""),
		stringMap(nestedCredentials, "account_id", ""),
		stringMap(nestedCredentials, "chatgptAccountId", ""),
		stringMap(nestedCredentials, "chatgpt_account_id", ""),
		stringMap(info, "accountId", ""),
	)
	email := codexFirstString(
		stringMap(credential, "email", ""),
		stringMap(nestedCredentials, "email", ""),
		stringMap(extra, "email", ""),
		stringMap(info, "email", ""),
	)
	sub := codexFirstString(
		stringMap(credential, "sub", ""),
		stringMap(nestedCredentials, "sub", ""),
		stringMap(nestedCredentials, "chatgptUserId", ""),
		stringMap(nestedCredentials, "chatgpt_user_id", ""),
		stringMap(info, "sub", ""),
	)
	displayName := codexFirstString(
		stringMap(credential, "displayName", ""),
		stringMap(credential, "display_name", ""),
		stringMap(credential, "name", ""),
		stringMap(nestedCredentials, "displayName", ""),
		stringMap(nestedCredentials, "display_name", ""),
		email,
		accountID,
	)
	entry := map[string]any{
		"id":       id.Must(8),
		"priority": numericMapOr(credential, "priority", numericMapOr(nestedCredentials, "priority", defaultPriority)),
		"disabled": false,
	}
	if refreshToken != "" {
		entry["refreshToken"] = refreshToken
	}
	if accessToken != "" {
		entry["accessToken"] = accessToken
	}
	if expiresAt := codexNormalizeExpiresAt(codexFirstValue(credential["expiresAt"], credential["expires_at"], nestedCredentials["expiresAt"], nestedCredentials["expires_at"])); expiresAt > 0 {
		entry["expiresAt"] = expiresAt
	}
	if accountID != "" {
		entry["accountId"] = accountID
	}
	if email != "" {
		entry["email"] = email
	}
	if sub != "" {
		entry["sub"] = sub
	}
	if displayName != "" {
		entry["displayName"] = displayName
	}
	return entry
}

func codexCredentialDedupeKeys(entry map[string]any) []string {
	keys := []string{}
	if refreshToken := codexFirstString(stringMap(entry, "refreshToken", ""), stringMap(entry, "refresh_token", "")); refreshToken != "" {
		keys = append(keys, "rt:"+sha256String(refreshToken))
	}
	if sub := codexFirstString(stringMap(entry, "sub", "")); sub != "" {
		keys = append(keys, "sub:"+sub)
	}
	accountID := codexFirstString(stringMap(entry, "accountId", ""), stringMap(entry, "account_id", ""))
	email := codexFirstString(stringMap(entry, "email", ""))
	if accountID != "" && email != "" {
		keys = append(keys, "account-email:"+accountID+":"+email)
	}
	if accessToken := codexFirstString(stringMap(entry, "accessToken", ""), stringMap(entry, "access_token", "")); accessToken != "" {
		keys = append(keys, "at:"+sha256String(accessToken))
	}
	return keys
}

func codexFirstString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func codexFirstValue(values ...any) any {
	for _, value := range values {
		if text, ok := value.(string); ok {
			if strings.TrimSpace(text) != "" {
				return value
			}
			continue
		}
		if value != nil {
			return value
		}
	}
	return nil
}

func codexNormalizeExpiresAt(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return codexNormalizeExpiresAtNumber(typed)
	case float32:
		return codexNormalizeExpiresAtNumber(float64(typed))
	case int:
		return codexNormalizeExpiresAtNumber(float64(typed))
	case int64:
		return codexNormalizeExpiresAtNumber(float64(typed))
	case json.Number:
		parsed, _ := typed.Float64()
		return codexNormalizeExpiresAtNumber(parsed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return codexNormalizeExpiresAtNumber(parsed)
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return float64(parsed.UnixMilli())
			}
		}
	}
	return 0
}

func codexNormalizeExpiresAtNumber(value float64) float64 {
	if value > 0 && value < 100000000000 {
		return value * 1000
	}
	return value
}

func codexTokenInfo(idToken, accessToken string) map[string]any {
	result := map[string]any{}
	applyClaims := func(claims map[string]any) {
		if len(claims) == 0 {
			return
		}
		if result["email"] == nil {
			if email := stringMap(claims, "email", ""); email != "" {
				result["email"] = email
			}
		}
		if result["sub"] == nil {
			if sub := stringMap(claims, "sub", ""); sub != "" {
				result["sub"] = sub
			}
		}
		if result["accountId"] == nil {
			if accountID := codexAccountID(claims); accountID != "" {
				result["accountId"] = accountID
			}
		}
	}
	applyClaims(parseJWTClaims(idToken))
	if result["accountId"] == nil || result["email"] == nil || result["sub"] == nil {
		applyClaims(parseJWTClaims(accessToken))
	}
	return result
}

func (h *Handler) codexImport(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Credentials any    `json:"credentials"`
		Text        string `json:"text"`
		ImportText  string `json:"importText"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	// Mirror the TypeScript /import credentials coercion (server/routes/codex.ts):
	// `credentials === undefined ? [] : Array.isArray(credentials) ? credentials :
	// [credentials]`. A single object/string `credentials` is wrapped into a
	// one-element list rather than rejected. Go previously decoded `credentials`
	// into a typed []any, so a single-object `{credentials:{...}}` (or a scalar)
	// failed the unmarshal with a generic "invalid JSON" message instead of being
	// wrapped and imported.
	var credentialItems []any
	switch value := body.Credentials.(type) {
	case nil:
		credentialItems = []any{}
	case []any:
		credentialItems = value
	default:
		credentialItems = []any{value}
	}
	entries, _ := loadCodexCredentials()
	existing := map[string]bool{}
	for _, entry := range entries {
		for _, key := range codexCredentialDedupeKeys(entry) {
			existing[key] = true
		}
	}
	importCredentials := codexImportCredentialsFromBody(credentialItems, body.Text, body.ImportText)
	if len(importCredentials) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "No credentials provided"})
		return nil
	}
	added, duplicates, skipped, validCandidates := 0, 0, 0, 0
	for _, cred := range importCredentials {
		entry := codexCredentialFromImport(cred, float64(len(entries)))
		if entry == nil {
			skipped++
			continue
		}
		keys := codexCredentialDedupeKeys(entry)
		if len(keys) == 0 {
			skipped++
			continue
		}
		validCandidates++
		duplicate := false
		for _, key := range keys {
			if existing[key] {
				duplicate = true
				break
			}
		}
		if duplicate {
			duplicates++
			continue
		}
		for _, key := range keys {
			existing[key] = true
		}
		entries = append(entries, entry)
		added++
	}
	if validCandidates == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "No valid credentials: each credential must have a non-empty refreshToken or accessToken"})
		return nil
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
	info := codexTokenInfo(stringMap(tokens, "id_token", ""), stringMap(tokens, "access_token", ""))
	expiresIn := numericMapOr(tokens, "expires_in", 3600)
	entry := map[string]any{
		"id":        id.Must(8),
		"expiresAt": float64(time.Now().UnixMilli()) + expiresIn*1000,
		"priority":  float64(0),
		"disabled":  false,
	}
	if refreshToken := stringMap(tokens, "refresh_token", ""); refreshToken != "" {
		entry["refreshToken"] = refreshToken
	}
	if accessToken := stringMap(tokens, "access_token", ""); accessToken != "" {
		entry["accessToken"] = accessToken
	}
	if accountID := stringMap(info, "accountId", ""); accountID != "" {
		entry["accountId"] = accountID
	}
	if email := stringMap(info, "email", ""); email != "" {
		entry["email"] = email
		entry["displayName"] = email
	}
	if sub := stringMap(info, "sub", ""); sub != "" {
		entry["sub"] = sub
	}
	if stringMap(entry, "displayName", "") == "" {
		entry["displayName"] = firstNonEmpty(stringMap(entry, "accountId", ""), "Codex")
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
	if stringMap(entry, "refreshToken", "") == "" && stringMap(entry, "accessToken", "") == "" {
		return errorsNew("No refresh token or access token in Codex token response")
	}
	entries, _ := loadCodexCredentials()
	newKeys := map[string]bool{}
	for _, key := range codexCredentialDedupeKeys(entry) {
		newKeys[key] = true
	}
	for i, existing := range entries {
		matched := false
		for _, key := range codexCredentialDedupeKeys(existing) {
			if newKeys[key] {
				matched = true
				break
			}
		}
		if matched {
			entry["id"] = stringMap(existing, "id", id.Must(8))
			entry["priority"] = numericMapOr(existing, "priority", float64(i))
			entries[i] = mergeCredentialMetadata(existing, entry)
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
			"id":                 stringMap(entry, "id", ""),
			"displayName":        entry["displayName"],
			"accountId":          entry["accountId"],
			"email":              entry["email"],
			"priority":           numericMapOr(entry, "priority", float64(i)),
			"disabled":           boolMap(entry, "disabled"),
			"disabledReason":     entry["disabledReason"],
			"successCount":       numericMapOr(entry, "successCount", 0),
			"failureCount":       numericMapOr(entry, "failureCount", 0),
			"lastUsedAt":         entry["lastUsedAt"],
			"expiresAt":          entry["expiresAt"],
			"quotaResetsAt":      entry["quotaResetsAt"],
			"usage":              entry["usage"],
			"lastRuntimeError":   entry["lastRuntimeError"],
			"lastRuntimeErrorAt": entry["lastRuntimeErrorAt"],
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
	if recoverExpiredKiroSuspiciousThrottles(entries, time.Now()) {
		_ = writeJSONFile(path, entries)
	}
	return entries, nil
}

func recoverExpiredKiroSuspiciousThrottles(entries []map[string]any, now time.Time) bool {
	changed := false
	for _, entry := range entries {
		if !boolMap(entry, "disabled") || stringMap(entry, "disabledReason", "") != "suspicious_throttle" {
			continue
		}
		disabledAt, ok := parseKiroDisabledAt(stringMap(entry, "disabledAt", ""))
		if ok && now.Sub(disabledAt) < kiroSuspiciousThrottleCooldown {
			continue
		}
		entry["disabled"] = false
		entry["failureCount"] = 0
		delete(entry, "disabledReason")
		delete(entry, "disabledAt")
		changed = true
	}
	return changed
}

func parseKiroDisabledAt(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, true
	}
	return time.Time{}, false
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
		return httpx.NotImplemented("providers.kiro.settingsStore")
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
	return normalizeCodexCredentialEntries(entries), nil
}

func normalizeCodexCredentialEntries(entries []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		refreshToken := codexFirstString(stringMap(entry, "refreshToken", ""), stringMap(entry, "refresh_token", ""))
		accessToken := codexFirstString(stringMap(entry, "accessToken", ""), stringMap(entry, "access_token", ""))
		if refreshToken == "" && accessToken == "" {
			continue
		}
		if refreshToken != "" {
			entry["refreshToken"] = refreshToken
		}
		if accessToken != "" {
			entry["accessToken"] = accessToken
		}
		if stringMap(entry, "id", "") == "" {
			entry["id"] = id.Must(8)
		}
		if _, ok := entry["priority"]; !ok {
			entry["priority"] = float64(len(out))
		}
		if _, ok := entry["disabled"]; !ok {
			entry["disabled"] = false
		}
		keys := codexCredentialDedupeKeys(entry)
		if len(keys) == 0 {
			continue
		}
		duplicate := false
		for _, key := range keys {
			if seen[key] {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		for _, key := range keys {
			seen[key] = true
		}
		out = append(out, entry)
	}
	return out
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
		reason := terminalError
		if reason == "" {
			reason = "terminal runtime state query failed"
		}
		terminalPayload["fallback"] = true
		terminalPayload["fallbacks"] = terminalFallbacks
		terminalPayload["reason"] = reason
		terminalPayload["error"] = reason
		terminalPayload["message"] = reason
	}
	if !terminalSupported {
		reason := "terminal runtime state table is not available"
		terminalPayload["fallback"] = true
		terminalPayload["reason"] = reason
		terminalPayload["error"] = reason
		terminalPayload["message"] = reason
	}
	containersRunning, containersStopped, podmanAvailable := podmanCounts(r.Context())
	containerPayload := map[string]any{"running": containersRunning, "stopped": containersStopped, "podmanAvailable": podmanAvailable, "supported": podmanAvailable, "fallback": !podmanAvailable}
	if !podmanAvailable {
		reason := "podman is not available; runtime scan uses persisted/non-container state only"
		containerPayload["reason"] = reason
		containerPayload["error"] = reason
		containerPayload["message"] = reason
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
	browserDB := h.browserRuntimeDB()
	if browserDB != nil {
		if ok, err := tableExistsInDB(ctx, browserDB, "narrator_browser_sessions"); err != nil {
			dbError = err.Error()
		} else if ok {
			dbTableAvailable = true
			rows, err := browserDB.QueryContext(ctx, `SELECT id FROM narrator_browser_sessions WHERE status IN ('active', 'running', 'idle') ORDER BY id ASC LIMIT 1000`)
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
	if cached := validRuntimeCache(); cached != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"cached": true, "data": cached})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cached": false})
	return nil
}

// validRuntimeCache returns the cached runtime scan if present and within the
// TTL window, clearing it otherwise to match the TypeScript getCachedResult TTL
// expiry behavior.
func validRuntimeCache() map[string]any {
	if runtimeCache == nil {
		return nil
	}
	scannedAt := runtimeCacheScannedAt(runtimeCache["scannedAt"])
	if scannedAt > 0 && time.Since(time.UnixMilli(scannedAt)) > runtimeCacheTTL {
		runtimeCache = nil
		return nil
	}
	return runtimeCache
}

// runtimeCacheScannedAt extracts the cached scannedAt millisecond timestamp,
// tolerating the int64 stored by scanRuntime as well as JSON-decoded float64.
func runtimeCacheScannedAt(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
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
		Errors:        []runtimeCleanupError{},
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
		// Mirror the TypeScript runtime cleanup route (server/routes/runtime.ts):
		// an unrecognized target fails cleanupTargetSchema and returns 400 "Invalid
		// target". Go keeps its documented capability extension where an omitted/empty
		// target defaults to "all" (matrix Wave 82) and additionally accepts the
		// Go-only "worktrees" target, so only a genuinely unknown non-empty value
		// reaches this branch. The error message is aligned to the TS string.
		return httpx.BadRequest("Invalid target")
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
	browserDB := h.browserRuntimeDB()
	exists, err := h.runtimeTableExists(ctx, "narrator_browser_sessions")
	if err != nil {
		result.addError("browsers", "narrator_browser_sessions", err)
	} else if exists && browserDB != nil {
		rows, err := browserDB.QueryContext(ctx, `SELECT id FROM narrator_browser_sessions WHERE status IN ('active', 'running', 'idle') ORDER BY id ASC`)
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
		if exists && browserDB != nil {
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
			if _, err := browserDB.ExecContext(ctx, query, args...); err != nil {
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

func (h *Handler) browserRuntimeDB() *sql.DB {
	if h == nil {
		return nil
	}
	if h.RuntimeDB != nil {
		return h.RuntimeDB
	}
	return h.DB
}

func tableExistsInDB(ctx context.Context, sqlDB *sql.DB, table string) (bool, error) {
	if sqlDB == nil {
		return false, nil
	}
	var name string
	err := sqlDB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (h *Handler) runtimeTableExists(ctx context.Context, table string) (bool, error) {
	if table == "narrator_browser_sessions" {
		return tableExistsInDB(ctx, h.browserRuntimeDB(), table)
	}
	return tableExistsInDB(ctx, h.DB, table)
}

func (h *Handler) runtimeColumnExists(ctx context.Context, table, column string) (bool, error) {
	sqlDB := h.DB
	if table == "narrator_browser_sessions" {
		sqlDB = h.browserRuntimeDB()
	}
	if sqlDB == nil {
		return false, nil
	}
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
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
	unhealthyTotal := 0
	currentID := ""
	for _, entry := range entries {
		if isCodexUnhealthyCredential(entry) {
			unhealthyTotal++
		}
		if boolMap(entry, "disabled") {
			unavailable = append(unavailable, entry)
			continue
		}
		if currentID == "" {
			currentID = stringMap(entry, "id", "")
		}
		available = append(available, entry)
	}
	availablePage := intQuery(r, "availablePage", 0)
	unavailablePage := intQuery(r, "unavailablePage", 0)
	pageSize := intQuery(r, "pageSize", 0)
	pagedAvailable := paginateCodexStatusEntries(available, availablePage, pageSize)
	pagedUnavailable := paginateCodexStatusEntries(unavailable, unavailablePage, pageSize)
	pagedEntries := make([]map[string]any, 0, len(pagedAvailable)+len(pagedUnavailable))
	pagedEntries = append(pagedEntries, pagedAvailable...)
	pagedEntries = append(pagedEntries, pagedUnavailable...)

	codexAuthMu.Lock()
	lastBrowserAuthError := ""
	if codexBrowser != nil && codexBrowser.Err != "" {
		lastBrowserAuthError = codexBrowser.Err
	}
	codexAuthMu.Unlock()
	_, usageSummary, usageForecast, _ := buildCodexUsageArtifacts(entries)
	usageCache := codexUsageCacheForEntries(pagedEntries)
	usageScheduler := map[string]any{"scheduledCredentialCount": numericMapOr(usageSummary, "scheduledAccountCount", 0), "dueCredentialCount": 0, "started": false}
	loadBalancingMode := stringMap(map[string]any{"value": h.codexSettingsValue("loadBalancingMode", "tier-balanced")}, "value", "tier-balanced")
	if loadBalancingMode != "priority" && loadBalancingMode != "balanced" && loadBalancingMode != "tier-balanced" {
		loadBalancingMode = "tier-balanced"
	}
	rawTierOrder, _ := h.codexSettingsValue("tierOrder", []any{}).([]any)
	tierOrder := normalizeCodexTierOrder(rawTierOrder)
	effectiveTierOrder := codexEffectiveTierOrder(tierOrder)
	globalProxy := h.codexSettingsValue("proxy", "")
	payload := map[string]any{"entries": pagedEntries, "availableEntries": pagedAvailable, "unavailableEntries": pagedUnavailable, "availableTotal": len(available), "unavailableTotal": len(unavailable), "unhealthyTotal": unhealthyTotal, "currentId": currentID, "loadBalancingMode": loadBalancingMode, "tierOrder": tierOrder, "effectiveTierOrder": effectiveTierOrder, "globalProxy": globalProxy, "proxy": globalProxy, "defaultReasoningEffort": h.codexSettingsValue("defaultReasoningEffort", nil), "useWebSocket": h.codexSettingsValue("useWebSocket", true), "useWebSearch": h.codexSettingsValue("useWebSearch", true), "useImageGeneration": h.codexSettingsValue("useImageGeneration", true), "total": len(entries), "available": len(available), "stickySessionCount": agentbasic.CodexStickySessionCount(), "usageCache": usageCache, "usageSummary": usageSummary, "usageForecast": usageForecast, "usageScheduler": usageScheduler, "usageQueue": map[string]any{"items": []any{}, "isRunning": false}}
	if lastBrowserAuthError != "" {
		payload["lastBrowserAuthError"] = lastBrowserAuthError
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func paginateCodexStatusEntries(entries []map[string]any, page int, pageSize int) []map[string]any {
	if pageSize <= 0 || page < 1 {
		return entries
	}
	// Mirror the TS codex-manager slicePage clamp: safePage = min(page, maxPage)
	// where maxPage = max(1, ceil(len/pageSize)). An out-of-range high page must
	// return the LAST page, not an empty slice (Go previously returned [] when
	// start >= len).
	maxPage := (len(entries) + pageSize - 1) / pageSize
	if maxPage < 1 {
		maxPage = 1
	}
	if page > maxPage {
		page = maxPage
	}
	start := (page - 1) * pageSize
	if start >= len(entries) {
		return []map[string]any{}
	}
	end := start + pageSize
	if end > len(entries) {
		end = len(entries)
	}
	return entries[start:end]
}

func codexUsageCacheForEntries(entries []map[string]any) map[string]any {
	cache := map[string]any{}
	for _, entry := range entries {
		usage, ok := entry["usage"].(map[string]any)
		if !ok || usage == nil {
			continue
		}
		if entryID := stringMap(entry, "id", ""); entryID != "" {
			cache[entryID] = usage
		}
	}
	return cache
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
