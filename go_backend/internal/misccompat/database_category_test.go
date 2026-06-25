package misccompat

import "testing"

// TestDatabaseTableCategoryMatchesTS 锁定 databaseTableCategory 逐字镜像 TS
// database-cleanup-service.ts:327-389 getDatabaseStorageCategory 的显式表名映射。
// 这些类别值是 user-visible（GET /api/storage/scan SSE category 事件 +
// 存储明细 categories[].key / topTables[].category）。
func TestDatabaseTableCategoryMatchesTS(t *testing.T) {
	cases := []struct {
		table string
		want  string
	}{
		// sessions：narrator 族（显式表名，非前缀）+ background_tasks
		{"narrators", "sessions"},
		{"narrator_messages", "sessions"},
		{"narrator_message_refs", "sessions"},
		{"narrator_tool_calls", "sessions"},
		{"narrator_file_snapshots", "sessions"},
		{"background_tasks", "sessions"},
		// apiRequests：独立类别（此前 Go 误并入 sessions）
		{"api_requests", "apiRequests"},
		// projects：含 merge_sessions/exploration_groups/review_conclusions（此前 Go 落 other）
		{"projects", "projects"},
		{"chapters", "projects"},
		{"chapter_edges", "projects"},
		{"chapter_commits", "projects"},
		{"exploration_groups", "projects"},
		{"merge_sessions", "projects"},
		{"review_conclusions", "projects"},
		// runtime：terminals/容器/卷快照族（此前 Go 无 runtime 类别，散落 sessions/other）
		{"terminals", "runtime"},
		{"terminal_tabs", "runtime"},
		{"terminal_view_state", "runtime"},
		{"container_instances", "runtime"},
		{"port_allocations", "runtime"},
		{"volume_snapshots", "runtime"},
		{"volume_snapshot_applications", "runtime"},
		// users：含 workspaces/hooks（此前 Go 落 other）
		{"users", "users"},
		{"user_preferences", "users"},
		{"user_favorite_directories", "users"},
		{"workspaces", "users"},
		{"hooks", "users"},
		// gateway：精确表名（此前 Go 用 gateway 前缀，恰好命中此表）
		{"gateway_session_mappings", "gateway"},
		// benchmarks：三张显式表（此前 Go 用 benchmark 前缀）
		{"benchmark_suites", "benchmarks"},
		{"benchmark_runs", "benchmarks"},
		{"benchmark_task_results", "benchmarks"},
		// search：FTS 表按 prefix==name 或 prefix_ 前缀（对齐 TS isSearchTableName，
		// 而非此前 Go 的 Contains("fts")）
		{"chapters_fts", "search"},
		{"narrator_messages_fts", "search"},
		{"narrators_fts", "search"},
		{"chapters_fts_data", "search"},
		// internal：sqlite_ 前缀（此前 Go 无此类别，会落 other）
		{"sqlite_sequence", "internal"},
		{"sqlite_stat1", "internal"},
		// other：未知表
		{"some_unknown_table", "other"},
		{"narrator", "other"}, // 非显式表名，不再被前缀误命中 sessions
	}
	for _, tc := range cases {
		if got := databaseTableCategory(tc.table); got != tc.want {
			t.Errorf("databaseTableCategory(%q) = %q, want %q", tc.table, got, tc.want)
		}
	}
}

// TestDatabaseTableCategoryKeysSubsetOfTS 确认 databaseTableCategory 产出的任何键
// 都在 TS DATABASE_STORAGE_CATEGORY_KEYS（database-cleanup-service.ts:25-37）的
// 11 个合法键集合内。
func TestDatabaseTableCategoryKeysSubsetOfTS(t *testing.T) {
	tsKeys := map[string]bool{
		"sessions": true, "apiRequests": true, "projects": true, "runtime": true,
		"users": true, "search": true, "gateway": true, "benchmarks": true,
		"internal": true, "free": true, "other": true,
	}
	samples := []string{
		"narrators", "api_requests", "projects", "terminals", "users",
		"chapters_fts", "gateway_session_mappings", "benchmark_runs",
		"sqlite_sequence", "background_tasks", "merge_sessions", "workspaces",
		"unknown_xyz",
	}
	for _, name := range samples {
		key := databaseTableCategory(name)
		if !tsKeys[key] {
			t.Errorf("databaseTableCategory(%q) returned %q which is not a TS DATABASE_STORAGE_CATEGORY_KEYS member", name, key)
		}
	}
}
