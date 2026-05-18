package systemroutes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/narrators"
)

func TestBenchmarkFakeExecutionCompletesWithMetrics(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	h := &Handler{DB: sqlDB, Narrators: narrators.NewService(sqlDB, nil, nil)}
	insertBenchmarkSuite(t, sqlDB, "suite-1", "humaneval", []map[string]any{{
		"id":             "task-1",
		"name":           "Echo",
		"prompt":         "Say expected",
		"expectedOutput": "expected",
	}})
	insertBenchmarkRun(t, sqlDB, "run-1", "suite-1", map[string]any{
		"fake":          true,
		"fakeOutput":    "the expected answer",
		"fakeTokensIn":  11,
		"fakeTokensOut": 7,
		"fakeCostUsd":   0.000123,
	})

	run, err := h.claimBenchmarkRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("claim run: %v", err)
	}
	h.executeClaimedBenchmarkRun(context.Background(), run, func() {})

	var status string
	var completed, passed, failed, tokensIn, tokensOut int
	var cost float64
	if err := sqlDB.QueryRow(`SELECT status, completed_tasks, passed_tasks, failed_tasks, total_tokens_in, total_tokens_out, total_cost_usd FROM benchmark_runs WHERE id='run-1'`).Scan(&status, &completed, &passed, &failed, &tokensIn, &tokensOut, &cost); err != nil {
		t.Fatalf("load run: %v", err)
	}
	if status != "completed" || completed != 1 || passed != 1 || failed != 0 {
		t.Fatalf("unexpected run aggregate: status=%s completed=%d passed=%d failed=%d", status, completed, passed, failed)
	}
	if tokensIn != 11 || tokensOut != 7 || cost != 0.000123 {
		t.Fatalf("unexpected metrics: in=%d out=%d cost=%v", tokensIn, tokensOut, cost)
	}
	var taskStatus, output string
	if err := sqlDB.QueryRow(`SELECT status, output FROM benchmark_task_results WHERE run_id='run-1'`).Scan(&taskStatus, &output); err != nil {
		t.Fatalf("load task: %v", err)
	}
	if taskStatus != "passed" || !strings.Contains(output, "expected") {
		t.Fatalf("unexpected task result: status=%s output=%q", taskStatus, output)
	}
	var narratorCount int
	_ = sqlDB.QueryRow(`SELECT COUNT(*) FROM narrators`).Scan(&narratorCount)
	if narratorCount != 1 {
		t.Fatalf("expected benchmark narrator to be created, got %d", narratorCount)
	}
}

func TestRecordBenchmarkTaskErrorReturnsUpdateErrors(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	insertBenchmarkSuite(t, sqlDB, "suite-1", "suite", []map[string]any{{"id": "task-1", "name": "Task", "prompt": "prompt"}})
	insertBenchmarkRun(t, sqlDB, "run-1", "suite-1", map[string]any{"fake": true})
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_benchmark_task_error_update BEFORE UPDATE OF status ON benchmark_task_results BEGIN SELECT RAISE(ABORT, 'benchmark task status update blocked'); END;`); err != nil {
		t.Fatalf("create task status trigger: %v", err)
	}
	h := &Handler{DB: sqlDB}

	err := h.recordBenchmarkTaskError(context.Background(), "run-1-task", "error", "failed", 12)
	if err == nil || !strings.Contains(err.Error(), "benchmark task status update blocked") {
		t.Fatalf("expected task status update error, got %v", err)
	}
}

func TestRecordBenchmarkTaskTimeoutReturnsUpdateErrors(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	insertBenchmarkSuite(t, sqlDB, "suite-1", "suite", []map[string]any{{"id": "task-1", "name": "Task", "prompt": "prompt"}})
	insertBenchmarkRun(t, sqlDB, "run-1", "suite-1", map[string]any{"fake": true})
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_benchmark_task_timeout_update BEFORE UPDATE OF status ON benchmark_task_results BEGIN SELECT RAISE(ABORT, 'benchmark timeout update blocked'); END;`); err != nil {
		t.Fatalf("create task timeout trigger: %v", err)
	}
	h := &Handler{DB: sqlDB}

	err := h.recordBenchmarkTaskTimeout(context.Background(), "run-1-task", benchmarkMetrics{}, time.Second, 1000)
	if err == nil || !strings.Contains(err.Error(), "benchmark timeout update blocked") {
		t.Fatalf("expected timeout update error, got %v", err)
	}
}

func TestFinishBenchmarkRunWithStatusReturnsUpdateErrors(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	insertBenchmarkSuite(t, sqlDB, "suite-1", "suite", []map[string]any{{"id": "task-1", "name": "Task", "prompt": "prompt"}})
	insertBenchmarkRun(t, sqlDB, "run-1", "suite-1", map[string]any{"fake": true})
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_benchmark_run_status_update BEFORE UPDATE OF status ON benchmark_runs BEGIN SELECT RAISE(ABORT, 'benchmark run status update blocked'); END;`); err != nil {
		t.Fatalf("create run status trigger: %v", err)
	}
	h := &Handler{DB: sqlDB}

	err := h.finishBenchmarkRunWithStatus(context.Background(), "run-1", "completed")
	if err == nil || !strings.Contains(err.Error(), "benchmark run status update blocked") {
		t.Fatalf("expected run status update error, got %v", err)
	}
}

func TestBenchmarkContainerTaskUsesExplicitFallback(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	h := &Handler{DB: sqlDB, Narrators: narrators.NewService(sqlDB, nil, nil)}
	insertBenchmarkSuite(t, sqlDB, "suite-1", "swe-bench", []map[string]any{{
		"id":       "task-1",
		"name":     "Needs container",
		"prompt":   "Fix repo",
		"metadata": map[string]any{"dockerImage": "python:3.11"},
	}})
	insertBenchmarkRun(t, sqlDB, "run-1", "suite-1", map[string]any{"fake": true})

	run, err := h.claimBenchmarkRun(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("claim run: %v", err)
	}
	h.executeClaimedBenchmarkRun(context.Background(), run, func() {})

	var taskStatus, message string
	if err := sqlDB.QueryRow(`SELECT status, error_message FROM benchmark_task_results WHERE run_id='run-1'`).Scan(&taskStatus, &message); err != nil {
		t.Fatalf("load task: %v", err)
	}
	if taskStatus != "error" || !strings.Contains(message, "container execution is not implemented") {
		t.Fatalf("expected explicit container fallback, got status=%s message=%q", taskStatus, message)
	}
	var failed int
	_ = sqlDB.QueryRow(`SELECT failed_tasks FROM benchmark_runs WHERE id='run-1'`).Scan(&failed)
	if failed != 1 {
		t.Fatalf("expected failed aggregate after fallback, got %d", failed)
	}
}

func TestBenchmarkRunExistsForCancelReturnsQueryErrors(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	for _, stmt := range []string{
		`CREATE TABLE benchmark_runs_real (id TEXT PRIMARY KEY)`,
		`INSERT INTO benchmark_runs_real (id) VALUES ('run-1')`,
		`CREATE VIEW benchmark_runs AS SELECT json_extract('not-json', '$') AS id FROM benchmark_runs_real`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken benchmark_runs view: %v", err)
		}
	}
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	h := &Handler{DB: sqlDB}

	_, err = h.benchmarkRunExistsForCancel(context.Background(), tx, "run-1")
	if err == nil {
		t.Fatalf("expected benchmark run existence query error")
	}
}

func TestBenchmarkCancelAndRetryResetRunnableStatuses(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	h := &Handler{DB: sqlDB, Narrators: narrators.NewService(sqlDB, nil, nil)}
	insertBenchmarkSuite(t, sqlDB, "suite-1", "humaneval", []map[string]any{{"id": "task-1", "name": "A", "prompt": "A"}})
	insertBenchmarkRun(t, sqlDB, "run-1", "suite-1", map[string]any{"fake": true})
	if _, err := sqlDB.Exec(`UPDATE benchmark_runs SET status='running' WHERE id='run-1'`); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/benchmarks/runs/run-1/cancel", nil)
	req = withChiParam(req, "id", "run-1")
	w := httptest.NewRecorder()
	if err := h.cancelBenchmarkRun(w, req); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", w.Code, w.Body.String())
	}
	var runStatus, taskStatus string
	if err := sqlDB.QueryRow(`SELECT status FROM benchmark_runs WHERE id='run-1'`).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM benchmark_task_results WHERE run_id='run-1'`).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "cancelled" || taskStatus != "cancelled" {
		t.Fatalf("expected cancelled run/task, got %s/%s", runStatus, taskStatus)
	}

	if err := h.resetBenchmarkRunForRetry(context.Background(), "run-1"); err != nil {
		t.Fatalf("retry reset: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM benchmark_runs WHERE id='run-1'`).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM benchmark_task_results WHERE run_id='run-1'`).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "pending" || taskStatus != "pending" {
		t.Fatalf("expected retry reset to pending, got %s/%s", runStatus, taskStatus)
	}
}

func newBenchmarkTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := createBenchmarkTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return sqlDB
}

func createBenchmarkTestSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE benchmark_suites (id TEXT PRIMARY KEY, name TEXT, version TEXT, description TEXT, tasks_json TEXT, created_at TEXT)`,
		`CREATE TABLE benchmark_runs (id TEXT PRIMARY KEY, suite_id TEXT, name TEXT, model TEXT, system_prompt TEXT, permission_mode TEXT, status TEXT DEFAULT 'pending', config TEXT, total_tasks INTEGER DEFAULT 0, completed_tasks INTEGER DEFAULT 0, passed_tasks INTEGER DEFAULT 0, failed_tasks INTEGER DEFAULT 0, total_cost_usd REAL DEFAULT 0, total_tokens_in INTEGER DEFAULT 0, total_tokens_out INTEGER DEFAULT 0, total_duration_ms INTEGER DEFAULT 0, started_at TEXT, completed_at TEXT, created_at TEXT)`,
		`CREATE TABLE benchmark_task_results (id TEXT PRIMARY KEY, run_id TEXT, task_id TEXT, task_name TEXT, narrator_id TEXT, status TEXT DEFAULT 'pending', score REAL, max_score REAL, output TEXT, eval_output TEXT, error_message TEXT, tokens_in INTEGER, tokens_out INTEGER, cost_usd REAL, duration_ms INTEGER, tool_call_count INTEGER, message_count INTEGER, metadata TEXT, started_at TEXT, completed_at TEXT, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, api_conversation_id TEXT, fork_message_id TEXT, type TEXT, subagent_type TEXT, title TEXT, inherit_mode TEXT, parent_narrator_id TEXT, context_summary TEXT, model TEXT, pending_model_restore TEXT, system_prompt TEXT, permission_mode TEXT, previous_permission_mode TEXT, plan_file_id TEXT, reasoning_effort TEXT, fast_mode INTEGER DEFAULT 0, relaxed_plan INTEGER DEFAULT 0, plan_reflection_auto_approve_override TEXT DEFAULT 'inherit', danger_reflection_override TEXT DEFAULT 'inherit', message_count INTEGER DEFAULT 0, total_cost_usd REAL DEFAULT 0, last_message_at TEXT, status TEXT DEFAULT 'idle', substatus TEXT DEFAULT '[]', plan_mode INTEGER DEFAULT 0, cwd TEXT, error_message TEXT, todos_json TEXT, todos_tool_use_id TEXT, prune_boundary_message_id TEXT, pruned_percent INTEGER, prune_enabled INTEGER DEFAULT 1, enabled_tools TEXT, variant TEXT DEFAULT 'primary', traits TEXT DEFAULT '[]', is_background INTEGER DEFAULT 0, background_status TEXT, background_result TEXT, background_completed_at TEXT, is_ask_in_passing INTEGER DEFAULT 0, turn_started_at TEXT, message_version INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, sdk_message_uuid TEXT, parent_tool_use_id TEXT, role TEXT, content_json TEXT, content_text TEXT, tokens_in INTEGER DEFAULT 0, cost_usd REAL DEFAULT 0, turn_usage_json TEXT, provider TEXT, credential_id TEXT, model TEXT, output_tokens INTEGER DEFAULT 0, cached_input_tokens INTEGER DEFAULT 0, cache_creation_input_tokens INTEGER DEFAULT 0, cache_creation_5m_tokens INTEGER DEFAULT 0, cache_creation_1h_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0, ttft_ms INTEGER, duration_ms INTEGER, context_percent REAL, meter_usage REAL, meter_unit TEXT, commit_sha TEXT, command_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0, pruned_percent INTEGER, segment_compact_id TEXT)`,
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, tool_name TEXT, input_json TEXT, output_json TEXT, status TEXT, duration_ms INTEGER, stream_started_at INTEGER, permission_started_at TEXT, execution_started_at TEXT, completed_at TEXT, error_message TEXT, permission_decided_by TEXT, permission_decided_at TEXT, permission_deny_message TEXT, permission_decision_reason TEXT, permission_suggestions TEXT, is_background INTEGER DEFAULT 0, input_tokens INTEGER, output_tokens INTEGER, cache_creation_tokens INTEGER, cache_read_tokens INTEGER, cache_creation_5m_tokens INTEGER, cache_creation_1h_tokens INTEGER, input_cost REAL, output_cost REAL, cache_creation_cost REAL, cache_read_cost REAL, total_cost REAL, provider TEXT, model TEXT, result_message_id TEXT, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func insertBenchmarkSuite(t *testing.T, db *sql.DB, idValue, name string, tasks []map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(tasks)
	if _, err := db.Exec(`INSERT INTO benchmark_suites (id,name,tasks_json,created_at) VALUES (?,?,?,?)`, idValue, name, string(payload), "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert suite: %v", err)
	}
}

func insertBenchmarkRun(t *testing.T, db *sql.DB, idValue, suiteID string, config map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(config)
	if _, err := db.Exec(`INSERT INTO benchmark_runs (id,suite_id,name,model,permission_mode,status,config,total_tasks,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, idValue, suiteID, "Run", "fake-model", "bypassPermissions", "pending", string(payload), 1, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO benchmark_task_results (id,run_id,task_id,task_name,status,created_at) VALUES (?,?,?,?,?,?)`, idValue+"-task", idValue, "task-1", "Task", "pending", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("insert task result: %v", err)
	}
}

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
