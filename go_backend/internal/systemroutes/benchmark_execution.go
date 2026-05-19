package systemroutes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/narrators"
)

const (
	defaultBenchmarkTaskTimeout = 10 * time.Minute
	benchmarkPollInterval       = 200 * time.Millisecond
	maxBenchmarkOutputChars     = 50_000
	maxBenchmarkErrorChars      = 5_000
	maxBenchmarkCopyBytes       = 100 * 1024 * 1024
)

type benchmarkRunRecord struct {
	ID             string
	SuiteID        string
	Name           string
	Model          string
	SystemPrompt   sql.NullString
	PermissionMode sql.NullString
	Status         string
	Config         map[string]any
	TotalTasks     int
	CreatedAt      string
	StartedAt      sql.NullString
}

type benchmarkTask struct {
	ID             string
	Name           string
	Prompt         string
	ExpectedOutput string
	TimeoutMs      int64
	Metadata       map[string]any
}

type benchmarkTaskResultRef struct {
	ID     string
	TaskID string
	Status string
}

type benchmarkMetrics struct {
	TokensIn          int64
	TokensOut         int64
	CostUSD           float64
	DurationMs        int64
	ToolCallCount     int64
	MessageCount      int64
	LastAssistantText string
}

type benchmarkEvalResult struct {
	Passed   bool
	Score    float64
	MaxScore float64
	Details  string
	Metadata map[string]any
}

var activeBenchmarkRuns = struct {
	sync.Mutex
	cancels map[string]context.CancelFunc
}{cancels: map[string]context.CancelFunc{}}

func validateBenchmarkSuitePayload(raw any) error {
	tasks, ok := raw.([]any)
	if !ok || len(tasks) == 0 {
		return httpx.BadRequest("benchmark suite tasks are required")
	}
	if len(tasks) > 1000 {
		return httpx.BadRequest("benchmark suites may contain at most 1000 tasks")
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return httpx.BadRequest("benchmark suite tasks must be JSON serializable")
	}
	if len(payload) > 20_000_000 {
		return httpx.BadRequest("benchmark suite payload must be <= 20000000 JSON bytes")
	}
	for i, item := range tasks {
		task, ok := item.(map[string]any)
		if !ok {
			return httpx.BadRequest(fmt.Sprintf("benchmark task %d must be an object", i+1))
		}
		if strings.TrimSpace(stringMap(task, "prompt", "")) == "" {
			return httpx.BadRequest(fmt.Sprintf("benchmark task %d prompt is required", i+1))
		}
		if len(stringMap(task, "prompt", "")) > 200_000 {
			return httpx.BadRequest(fmt.Sprintf("benchmark task %d prompt must be <= 200000 characters", i+1))
		}
	}
	return nil
}

func (h *Handler) retryBenchmarkRun(w http.ResponseWriter, r *http.Request) error {
	runID := chi.URLParam(r, "id")
	if err := h.resetBenchmarkRunForRetry(r.Context(), runID); err != nil {
		return err
	}
	claimed, err := h.claimBenchmarkRun(r.Context(), runID)
	if err != nil {
		return err
	}
	h.startClaimedBenchmarkRun(claimed)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "runId": runID, "status": "running", "message": "Benchmark run retry started"})
	return nil
}

func (h *Handler) claimBenchmarkRun(ctx context.Context, runID string) (benchmarkRunRecord, error) {
	run, err := h.benchmarkRunRecord(ctx, runID)
	if err != nil {
		return benchmarkRunRecord{}, err
	}
	if _, err := h.benchmarkSuiteTasks(ctx, run.SuiteID); err != nil {
		return benchmarkRunRecord{}, err
	}
	now := db.NowISO()
	res, err := h.DB.ExecContext(ctx, `UPDATE benchmark_runs SET status='running', started_at=COALESCE(started_at, ?), completed_at=NULL WHERE id=? AND status='pending'`, now, runID)
	if err != nil {
		return benchmarkRunRecord{}, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		current, loadErr := h.benchmarkRunRecord(ctx, runID)
		if loadErr != nil {
			return benchmarkRunRecord{}, loadErr
		}
		return benchmarkRunRecord{}, httpx.Error(409, "BENCHMARK_RUN_NOT_PENDING", fmt.Sprintf("benchmark run is not pending (current: %s)", current.Status))
	}
	run, err = h.benchmarkRunRecord(ctx, runID)
	if err != nil {
		return benchmarkRunRecord{}, err
	}
	return run, nil
}

func (h *Handler) resetBenchmarkRunForRetry(ctx context.Context, runID string) error {
	var status string
	if err := h.DB.QueryRowContext(ctx, `SELECT status FROM benchmark_runs WHERE id=?`, runID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Benchmark run")
		}
		return err
	}
	if status == "running" {
		return httpx.Error(409, "BENCHMARK_RUN_RUNNING", "running benchmark runs cannot be retried")
	}
	now := db.NowISO()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `UPDATE benchmark_task_results SET status='pending', score=NULL, max_score=NULL, output=NULL, eval_output=NULL, error_message=NULL, tokens_in=NULL, tokens_out=NULL, cost_usd=NULL, duration_ms=NULL, tool_call_count=NULL, message_count=NULL, metadata=NULL, started_at=NULL, completed_at=NULL WHERE run_id=? AND status IN ('failed','error','timeout','cancelled')`, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE benchmark_runs SET status='pending', completed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status NOT IN ('pending','running')), passed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status='passed'), failed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status IN ('failed','error','timeout')), total_cost_usd=COALESCE((SELECT SUM(COALESCE(cost_usd,0)) FROM benchmark_task_results WHERE run_id=?),0), total_tokens_in=COALESCE((SELECT SUM(COALESCE(tokens_in,0)) FROM benchmark_task_results WHERE run_id=?),0), total_tokens_out=COALESCE((SELECT SUM(COALESCE(tokens_out,0)) FROM benchmark_task_results WHERE run_id=?),0), total_duration_ms=COALESCE((SELECT SUM(COALESCE(duration_ms,0)) FROM benchmark_task_results WHERE run_id=?),0), started_at=NULL, completed_at=NULL WHERE id=?`, runID, runID, runID, runID, runID, runID, runID, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE benchmark_runs SET created_at=COALESCE(created_at, ?) WHERE id=?`, now, runID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (h *Handler) startClaimedBenchmarkRun(run benchmarkRunRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	activeBenchmarkRuns.Lock()
	if existing := activeBenchmarkRuns.cancels[run.ID]; existing != nil {
		existing()
	}
	activeBenchmarkRuns.cancels[run.ID] = cancel
	activeBenchmarkRuns.Unlock()
	go h.executeClaimedBenchmarkRun(ctx, run, cancel)
}

func (h *Handler) cancelActiveBenchmarkRun(runID string) {
	activeBenchmarkRuns.Lock()
	cancel := activeBenchmarkRuns.cancels[runID]
	activeBenchmarkRuns.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *Handler) unregisterActiveBenchmarkRun(runID string, cancel context.CancelFunc) {
	activeBenchmarkRuns.Lock()
	if activeBenchmarkRuns.cancels[runID] != nil {
		delete(activeBenchmarkRuns.cancels, runID)
	}
	activeBenchmarkRuns.Unlock()
	cancel()
}

func (h *Handler) executeClaimedBenchmarkRun(ctx context.Context, run benchmarkRunRecord, cancel context.CancelFunc) {
	defer h.unregisterActiveBenchmarkRun(run.ID, cancel)
	tasks, err := h.benchmarkSuiteTasks(ctx, run.SuiteID)
	if err != nil {
		_ = h.finishBenchmarkRunWithStatus(context.Background(), run.ID, "failed")
		return
	}
	results, err := h.benchmarkTaskResultRefs(ctx, run.ID)
	if err != nil {
		_ = h.finishBenchmarkRunWithStatus(context.Background(), run.ID, "failed")
		return
	}
	resultByTask := map[string]benchmarkTaskResultRef{}
	for _, result := range results {
		resultByTask[result.TaskID] = result
	}
	for _, task := range tasks {
		if ctx.Err() != nil || h.benchmarkRunCancelled(context.Background(), run.ID) {
			break
		}
		result, ok := resultByTask[task.ID]
		if !ok || result.Status != "pending" {
			_ = h.refreshBenchmarkRunTotals(context.Background(), run.ID)
			continue
		}
		if err := h.executeBenchmarkTask(ctx, run, task, result.ID); err != nil {
			if recordErr := h.recordBenchmarkTaskError(context.Background(), result.ID, "error", err.Error(), 0); recordErr != nil {
				_ = h.finishBenchmarkRunWithStatus(context.Background(), run.ID, "failed")
				return
			}
		}
		_ = h.refreshBenchmarkRunTotals(context.Background(), run.ID)
	}
	finalStatus := "completed"
	if ctx.Err() != nil || h.benchmarkRunCancelled(context.Background(), run.ID) {
		finalStatus = "cancelled"
	}
	_ = h.finishBenchmarkRunWithStatus(context.Background(), run.ID, finalStatus)
}

func (h *Handler) executeBenchmarkTask(ctx context.Context, run benchmarkRunRecord, task benchmarkTask, resultID string) error {
	started := time.Now()
	now := db.NowISO()
	if _, err := h.DB.ExecContext(ctx, `UPDATE benchmark_task_results SET status='running', started_at=? WHERE id=? AND status='pending'`, now, resultID); err != nil {
		return err
	}
	workDir, err := os.MkdirTemp("", "nf-bench-"+safeBenchmarkPathPart(task.ID)+"-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	if err := h.prepareBenchmarkWorkspace(ctx, run, task, workDir); err != nil {
		return h.recordBenchmarkTaskError(context.Background(), resultID, "error", err.Error(), time.Since(started).Milliseconds())
	}
	containerID := ""
	if benchmarkRequiresContainer(run, task) {
		container, err := h.prepareBenchmarkContainer(ctx, run, task, workDir)
		if err != nil {
			return h.recordBenchmarkTaskError(context.Background(), resultID, "error", err.Error(), time.Since(started).Milliseconds())
		}
		containerID = container.ID
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["containerId"] = container.ID
		task.Metadata["containerSetupOutput"] = container.Output
		defer func() { _ = cleanupBenchmarkContainer(context.Background(), containerID) }()
	}

	if h.Narrators == nil {
		return h.recordBenchmarkTaskError(context.Background(), resultID, "error", "Benchmark narrator execution unavailable in go_backend: narrator service is not configured", time.Since(started).Milliseconds())
	}
	title := "Bench: " + task.Name
	systemPrompt := nullStringPtrForBenchmark(run.SystemPrompt)
	permissionMode := stringDefault(run.PermissionMode, "bypassPermissions")
	narrator, err := h.Narrators.Create(ctx, narrators.CreateInput{Title: &title, Model: run.Model, SystemPrompt: systemPrompt, PermissionMode: permissionMode, Cwd: &workDir})
	if err != nil {
		return err
	}
	if _, err := h.DB.ExecContext(ctx, `UPDATE benchmark_task_results SET narrator_id=? WHERE id=?`, narrator.ID, resultID); err != nil {
		return err
	}

	fake := benchmarkFakeEnabled(run, task)
	if fake {
		usage := benchmarkFakeUsage(run, task)
		output := benchmarkFakeOutput(run, task)
		if err := h.Narrators.RecordBenchmarkExchange(ctx, narrator.ID, "benchmark", task.Prompt, output, usage); err != nil {
			return err
		}
	} else {
		if _, err := h.Narrators.AddUserMessage(ctx, narrator.ID, "benchmark", task.Prompt); err != nil {
			return err
		}
		timeout := benchmarkTaskTimeout(run, task)
		completed := h.waitForNarratorIdle(ctx, narrator.ID, timeout)
		if ctx.Err() != nil || h.benchmarkRunCancelled(context.Background(), run.ID) {
			h.Narrators.CancelRun(narrator.ID)
			return h.recordBenchmarkTaskError(context.Background(), resultID, "cancelled", "Run cancelled", time.Since(started).Milliseconds())
		}
		if !completed {
			h.Narrators.CancelRun(narrator.ID)
			metrics, _ := h.collectBenchmarkMetrics(context.Background(), narrator.ID)
			return h.recordBenchmarkTaskTimeout(context.Background(), resultID, metrics, timeout, time.Since(started).Milliseconds())
		}
	}

	metrics, err := h.collectBenchmarkMetrics(ctx, narrator.ID)
	if err != nil {
		return err
	}
	metrics.DurationMs = time.Since(started).Milliseconds()
	evalResult := evaluateBenchmarkTask(task, metrics.LastAssistantText, run.Config)
	if containerID != "" {
		evalResult = h.evaluateBenchmarkTaskWithContainer(ctx, containerID, task, evalResult, run.Config)
	}
	return h.recordBenchmarkTaskSuccess(ctx, resultID, metrics, evalResult)
}

func (h *Handler) prepareBenchmarkWorkspace(ctx context.Context, run benchmarkRunRecord, task benchmarkTask, workDir string) error {
	sourcePath := strings.TrimSpace(stringMap(task.Metadata, "sourcePath", ""))
	if sourcePath == "" {
		return nil
	}
	if !boolMap(run.Config, "allowLocalSource") {
		return errors.New("Benchmark sourcePath copying is disabled by default in go_backend; set config.allowLocalSource=true to enable bounded local workspace copy")
	}
	limit := int64(numericMapOr(run.Config, "maxWorkspaceBytes", maxBenchmarkCopyBytes))
	if limit <= 0 || limit > maxBenchmarkCopyBytes {
		limit = maxBenchmarkCopyBytes
	}
	return copyDirLimited(ctx, sourcePath, workDir, limit)
}

func benchmarkRequiresContainer(run benchmarkRunRecord, task benchmarkTask) bool {
	if strings.EqualFold(strings.TrimSpace(run.Name), "swe-bench") || strings.EqualFold(strings.TrimSpace(stringMap(task.Metadata, "evaluator", "")), "swe-bench") {
		return true
	}
	return strings.TrimSpace(stringMap(task.Metadata, "repo", "")) != "" || strings.TrimSpace(stringMap(task.Metadata, "dockerImage", "")) != "" || strings.TrimSpace(stringMap(task.Metadata, "containerImage", "")) != ""
}

func (h *Handler) waitForNarratorIdle(ctx context.Context, narratorID string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(benchmarkPollInterval)
	defer ticker.Stop()
	for {
		var status string
		if err := h.DB.QueryRowContext(ctx, `SELECT status FROM narrators WHERE id=?`, narratorID).Scan(&status); err != nil {
			return false
		}
		if status == "idle" || status == "archived" {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func (h *Handler) collectBenchmarkMetrics(ctx context.Context, narratorID string) (benchmarkMetrics, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT role, COALESCE(content_text,''), COALESCE(tokens_in,0), COALESCE(output_tokens,0), COALESCE(reasoning_tokens,0), COALESCE(cost_usd,0) FROM narrator_messages WHERE narrator_id=? ORDER BY created_at ASC LIMIT 1000`, narratorID)
	if err != nil {
		return benchmarkMetrics{}, err
	}
	defer rows.Close()
	metrics := benchmarkMetrics{}
	for rows.Next() {
		var role, text string
		var tokensIn, outputTokens, reasoningTokens int64
		var cost float64
		if err := rows.Scan(&role, &text, &tokensIn, &outputTokens, &reasoningTokens, &cost); err != nil {
			return benchmarkMetrics{}, err
		}
		metrics.MessageCount++
		metrics.TokensIn += tokensIn
		metrics.TokensOut += outputTokens + reasoningTokens
		metrics.CostUSD += cost
		if role == "assistant" && strings.TrimSpace(text) != "" {
			metrics.LastAssistantText = text
		}
	}
	if err := rows.Err(); err != nil {
		return benchmarkMetrics{}, err
	}
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_tool_calls WHERE narrator_id=?`, narratorID).Scan(&metrics.ToolCallCount); err != nil {
		return benchmarkMetrics{}, err
	}
	metrics.CostUSD = math.Round(metrics.CostUSD*1_000_000) / 1_000_000
	return metrics, nil
}

func (h *Handler) recordBenchmarkTaskSuccess(ctx context.Context, resultID string, metrics benchmarkMetrics, eval benchmarkEvalResult) error {
	metadataJSON, _ := json.Marshal(eval.Metadata)
	status := "failed"
	if eval.Passed {
		status = "passed"
	}
	_, err := h.DB.ExecContext(ctx, `UPDATE benchmark_task_results SET status=?, score=?, max_score=?, output=?, eval_output=?, error_message=NULL, tokens_in=?, tokens_out=?, cost_usd=?, duration_ms=?, tool_call_count=?, message_count=?, metadata=?, completed_at=? WHERE id=?`, status, eval.Score, eval.MaxScore, truncateForBenchmark(metrics.LastAssistantText, maxBenchmarkOutputChars), truncateForBenchmark(eval.Details, maxBenchmarkOutputChars), metrics.TokensIn, metrics.TokensOut, metrics.CostUSD, metrics.DurationMs, metrics.ToolCallCount, metrics.MessageCount, string(metadataJSON), db.NowISO(), resultID)
	return err
}

func (h *Handler) recordBenchmarkTaskTimeout(ctx context.Context, resultID string, metrics benchmarkMetrics, timeout time.Duration, durationMs int64) error {
	_, err := h.DB.ExecContext(ctx, `UPDATE benchmark_task_results SET status='timeout', output=?, error_message=?, tokens_in=?, tokens_out=?, cost_usd=?, duration_ms=?, tool_call_count=?, message_count=?, completed_at=? WHERE id=?`, truncateForBenchmark(metrics.LastAssistantText, maxBenchmarkOutputChars), fmt.Sprintf("Task timed out after %dms", timeout.Milliseconds()), metrics.TokensIn, metrics.TokensOut, metrics.CostUSD, durationMs, metrics.ToolCallCount, metrics.MessageCount, db.NowISO(), resultID)
	return err
}

func (h *Handler) recordBenchmarkTaskError(ctx context.Context, resultID, status, message string, durationMs int64) error {
	if status == "" {
		status = "error"
	}
	_, err := h.DB.ExecContext(ctx, `UPDATE benchmark_task_results SET status=?, error_message=?, duration_ms=?, completed_at=? WHERE id=?`, status, truncateForBenchmark(message, maxBenchmarkErrorChars), durationMs, db.NowISO(), resultID)
	return err
}

func (h *Handler) refreshBenchmarkRunTotals(ctx context.Context, runID string) error {
	_, err := h.DB.ExecContext(ctx, `UPDATE benchmark_runs SET completed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status NOT IN ('pending','running')), passed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status='passed'), failed_tasks=(SELECT COUNT(*) FROM benchmark_task_results WHERE run_id=? AND status IN ('failed','error','timeout')), total_cost_usd=COALESCE((SELECT SUM(COALESCE(cost_usd,0)) FROM benchmark_task_results WHERE run_id=?),0), total_tokens_in=COALESCE((SELECT SUM(COALESCE(tokens_in,0)) FROM benchmark_task_results WHERE run_id=?),0), total_tokens_out=COALESCE((SELECT SUM(COALESCE(tokens_out,0)) FROM benchmark_task_results WHERE run_id=?),0), total_duration_ms=COALESCE((SELECT SUM(COALESCE(duration_ms,0)) FROM benchmark_task_results WHERE run_id=?),0) WHERE id=?`, runID, runID, runID, runID, runID, runID, runID, runID)
	return err
}

func (h *Handler) finishBenchmarkRunWithStatus(ctx context.Context, runID, status string) error {
	if err := h.refreshBenchmarkRunTotals(ctx, runID); err != nil {
		return err
	}
	completedAt := time.Now().UTC()
	durationMs := any(nil)
	var startedAt sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT started_at FROM benchmark_runs WHERE id=?`, runID).Scan(&startedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if startedAt.Valid {
		if parsed, err := time.Parse(time.RFC3339Nano, startedAt.String); err == nil {
			durationMs = completedAt.Sub(parsed).Milliseconds()
		}
	}
	_, err := h.DB.ExecContext(ctx, `UPDATE benchmark_runs SET status=?, completed_at=?, total_duration_ms=COALESCE(?, total_duration_ms) WHERE id=?`, status, completedAt.Format(time.RFC3339Nano), durationMs, runID)
	return err
}

func (h *Handler) benchmarkRunCancelled(ctx context.Context, runID string) bool {
	var status string
	if err := h.DB.QueryRowContext(ctx, `SELECT status FROM benchmark_runs WHERE id=?`, runID).Scan(&status); err != nil {
		return false
	}
	return status == "cancelled"
}

func (h *Handler) benchmarkRunRecord(ctx context.Context, runID string) (benchmarkRunRecord, error) {
	var run benchmarkRunRecord
	var configRaw sql.NullString
	err := h.DB.QueryRowContext(ctx, `SELECT id,suite_id,name,model,system_prompt,permission_mode,status,config,total_tasks,created_at,started_at FROM benchmark_runs WHERE id=?`, runID).Scan(&run.ID, &run.SuiteID, &run.Name, &run.Model, &run.SystemPrompt, &run.PermissionMode, &run.Status, &configRaw, &run.TotalTasks, &run.CreatedAt, &run.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return run, httpx.NotFound("Benchmark run")
	}
	if err != nil {
		return run, err
	}
	run.Config = map[string]any{}
	if configRaw.Valid && strings.TrimSpace(configRaw.String) != "" {
		_ = json.Unmarshal([]byte(configRaw.String), &run.Config)
	}
	return run, nil
}

func (h *Handler) benchmarkSuiteTasks(ctx context.Context, suiteID string) ([]benchmarkTask, error) {
	var raw sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT tasks_json FROM benchmark_suites WHERE id=?`, suiteID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NotFound("Benchmark suite")
		}
		return nil, err
	}
	taskMaps := benchmarkTasksFromJSON(raw)
	if len(taskMaps) > 1000 {
		return nil, httpx.BadRequest("benchmark suites may contain at most 1000 tasks")
	}
	tasks := make([]benchmarkTask, 0, len(taskMaps))
	for _, item := range taskMaps {
		meta, _ := item["metadata"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		task := benchmarkTask{
			ID:             stringMap(item, "id", ""),
			Name:           stringMap(item, "name", "Task"),
			Prompt:         stringMap(item, "prompt", ""),
			ExpectedOutput: stringMap(item, "expectedOutput", ""),
			TimeoutMs:      int64(numericMapOr(item, "timeoutMs", 0)),
			Metadata:       meta,
		}
		if strings.TrimSpace(task.Prompt) == "" {
			return nil, httpx.BadRequest("benchmark task prompt is required")
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (h *Handler) benchmarkTaskResultRefs(ctx context.Context, runID string) ([]benchmarkTaskResultRef, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT id,task_id,status FROM benchmark_task_results WHERE run_id=? ORDER BY created_at, task_id LIMIT 10000`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []benchmarkTaskResultRef{}
	for rows.Next() {
		var ref benchmarkTaskResultRef
		if err := rows.Scan(&ref.ID, &ref.TaskID, &ref.Status); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func evaluateBenchmarkTask(task benchmarkTask, output string, config map[string]any) benchmarkEvalResult {
	expected := strings.TrimSpace(task.ExpectedOutput)
	if expected == "" {
		expected = strings.TrimSpace(stringMap(task.Metadata, "expectedOutput", ""))
	}
	if expected == "" {
		passed := strings.TrimSpace(output) != ""
		details := "No output produced"
		if passed {
			details = "Output produced; no explicit evaluator configured"
		}
		return benchmarkEvalResult{Passed: passed, Score: boolScore(passed), MaxScore: 1, Details: details, Metadata: map[string]any{"evaluator": "go-fallback"}}
	}
	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(stringMap(task.Metadata, "match", ""), stringMap(config, "match", "contains"))))
	passed := false
	if mode == "exact" {
		passed = strings.TrimSpace(output) == expected
	} else {
		passed = strings.Contains(output, expected)
	}
	details := fmt.Sprintf("Go fallback evaluator expected %q with match=%s", truncateForBenchmark(expected, 200), mode)
	return benchmarkEvalResult{Passed: passed, Score: boolScore(passed), MaxScore: 1, Details: details, Metadata: map[string]any{"evaluator": "go-fallback", "match": mode}}
}

func benchmarkFakeEnabled(run benchmarkRunRecord, task benchmarkTask) bool {
	return boolMap(run.Config, "fake") || boolMap(task.Metadata, "fake") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(run.Model)), "fake")
}

func benchmarkFakeOutput(run benchmarkRunRecord, task benchmarkTask) string {
	for _, source := range []map[string]any{task.Metadata, run.Config} {
		if output := stringMap(source, "fakeOutput", ""); output != "" {
			return output
		}
	}
	if task.ExpectedOutput != "" {
		return task.ExpectedOutput
	}
	return "fake benchmark response for " + task.Name
}

func benchmarkFakeUsage(run benchmarkRunRecord, task benchmarkTask) narrators.BenchmarkUsage {
	return narrators.BenchmarkUsage{
		TokensIn:  int(numericMapOr(task.Metadata, "fakeTokensIn", numericMapOr(run.Config, "fakeTokensIn", 1))),
		TokensOut: int(numericMapOr(task.Metadata, "fakeTokensOut", numericMapOr(run.Config, "fakeTokensOut", 1))),
		CostUSD:   numericMapOr(task.Metadata, "fakeCostUsd", numericMapOr(run.Config, "fakeCostUsd", 0)),
		Model:     run.Model,
	}
}

func benchmarkTaskTimeout(run benchmarkRunRecord, task benchmarkTask) time.Duration {
	if task.TimeoutMs > 0 {
		return time.Duration(task.TimeoutMs) * time.Millisecond
	}
	if timeout := numericMapOr(run.Config, "taskTimeoutMs", 0); timeout > 0 {
		return time.Duration(timeout) * time.Millisecond
	}
	return defaultBenchmarkTaskTimeout
}

func copyDirLimited(ctx context.Context, sourcePath, targetPath string, maxBytes int64) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Benchmark sourcePath must be a directory: %s", sourcePath)
	}
	var copied int64
	return filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil || rel == "." {
			return err
		}
		dest := filepath.Join(targetPath, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if entry.Type()&os.ModeType != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		copied += info.Size()
		if copied > maxBytes {
			return fmt.Errorf("benchmark workspace copy exceeds %d bytes", maxBytes)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(in, info.Size()+1))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func nullStringPtrForBenchmark(value sql.NullString) *string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	return &value.String
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func truncateForBenchmark(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars]) + "\n[truncated]"
}

func safeBenchmarkPathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "task"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "\x00", "")
	value = replacer.Replace(value)
	if len(value) > 60 {
		value = value[:60]
	}
	return value
}
