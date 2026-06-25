package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

// insertTopLevelMessageAtSeq inserts a primary-narrator top-level message with an
// explicit ref seq, mirroring the TS test helper insertMessage (which always
// creates a ref). Used to seed deterministic seq ranges for boundary tests.
func insertTopLevelMessageAtSeq(t *testing.T, sqlDB *sql.DB, narratorID, messageID string, seq int, text string) {
	t.Helper()
	now := time.Now().UTC().Add(time.Duration(seq) * time.Millisecond).Format(time.RFC3339Nano)
	contentJSON, _ := json.Marshal([]map[string]any{{"type": "text", "text": text}})
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, 'assistant', ?, ?, ?)`, messageID, narratorID, string(contentJSON), text, now); err != nil {
		t.Fatalf("insert message %s: %v", messageID, err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, "ref_"+narratorID+"_"+messageID, narratorID, messageID, seq); err != nil {
		t.Fatalf("insert ref %s: %v", messageID, err)
	}
}

// insertParentMessageWithRef inserts a top-level assistant message that owns a
// tool_use, plus its ref and tool-call row, returning nothing. Mirrors the TS
// insertMessage + insertToolCall pair used by the message-location tests.
func insertParentMessageWithRef(t *testing.T, sqlDB *sql.DB, narratorID, messageID, toolUseID string, seq int) {
	t.Helper()
	now := time.Now().UTC().Add(time.Duration(seq) * time.Millisecond).Format(time.RFC3339Nano)
	contentJSON, _ := json.Marshal([]map[string]any{{"type": "tool_use", "id": toolUseID, "name": "Task", "input": map[string]any{}}})
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, 'assistant', ?, '', ?)`, messageID, narratorID, string(contentJSON), now); err != nil {
		t.Fatalf("insert parent message %s: %v", messageID, err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, "ref_"+narratorID+"_"+messageID, narratorID, messageID, seq); err != nil {
		t.Fatalf("insert parent ref %s: %v", messageID, err)
	}
	insertToolCallTestRow(t, sqlDB, narratorID, messageID, toolUseID)
}

// markNarratorSubagent flips a seeded narrator row to the subagent variant so the
// chunk queries treat its own child refs as top-level.
func markNarratorSubagent(t *testing.T, sqlDB *sql.DB, narratorID string) {
	t.Helper()
	if _, err := sqlDB.Exec(`UPDATE narrators SET type = 'subagent', variant = 'subagent:general' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark subagent: %v", err)
	}
}

func insertPrimaryNarratorWithID(t *testing.T, sqlDB *sql.DB, narratorID string) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', 'Second test narrator', 'fresh', 'openai:test', 'default', 'idle', '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, now, now)
	if err != nil {
		t.Fatalf("insert primary narrator %s: %v", narratorID, err)
	}
	return narratorID
}

func insertToolCallWithID(t *testing.T, sqlDB *sql.DB, id, narratorID, messageID, toolUseID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, ?, 'Task', '{}', '{}', 'success', ?)`, id, narratorID, messageID, toolUseID, now)
	if err != nil {
		t.Fatalf("insert tool call %s: %v", id, err)
	}
}

func TestChunkManifestBuildsTuplesAndShortCircuits(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	// 25 top-level messages → 2 chunks (20 + 5) at CHUNK_SIZE = 20.
	for i := 0; i < 25; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}

	result, err := service.ChunkManifest(ctx, narratorID, nil, nil)
	if err != nil {
		t.Fatalf("chunk manifest: %v", err)
	}
	if result["unchanged"] != false {
		t.Fatalf("expected unchanged=false on first load, got %#v", result["unchanged"])
	}
	if result["total"] != 25 {
		t.Fatalf("expected total=25, got %#v", result["total"])
	}
	chunks, ok := result["chunks"].([]ManifestChunkTuple)
	if !ok || len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %#v", result["chunks"])
	}
	if chunks[0].FirstSeq != 0 || chunks[0].LastSeq != 19 || chunks[0].Count != 20 {
		t.Fatalf("chunk 0 boundaries wrong: %#v", chunks[0])
	}
	if chunks[1].FirstSeq != 20 || chunks[1].LastSeq != 24 || chunks[1].Count != 5 {
		t.Fatalf("chunk 1 boundaries wrong: %#v", chunks[1])
	}
	if chunks[0].ID != "m0" || chunks[1].ID != "m20" {
		t.Fatalf("chunk ids should be first message of each chunk, got %q / %q", chunks[0].ID, chunks[1].ID)
	}

	// Tuple wire form must marshal to [id, firstSeq, lastSeq, count].
	encoded, err := json.Marshal(chunks[1])
	if err != nil {
		t.Fatalf("marshal tuple: %v", err)
	}
	if string(encoded) != `["m20",20,24,5]` {
		t.Fatalf("tuple wire form wrong, got %s", encoded)
	}

	// since == current messageVersion short-circuits to { unchanged: true }.
	version, err := service.NarratorMessageVersion(ctx, narratorID)
	if err != nil {
		t.Fatalf("message version: %v", err)
	}
	short, err := service.ChunkManifest(ctx, narratorID, &version, nil)
	if err != nil {
		t.Fatalf("short-circuit manifest: %v", err)
	}
	if short["unchanged"] != true {
		t.Fatalf("expected unchanged=true when since matches version, got %#v", short)
	}
	if _, hasChunks := short["chunks"]; hasChunks {
		t.Fatalf("short-circuit response must omit chunks, got %#v", short)
	}
}

// TestChunkManifestWindowing mirrors the TS getChunkManifest 窗口化 suite
// (tests/server/services/narrator-message-queries.test.ts). CHUNK_SIZE = 20; 50
// top-level messages (seq 0..49) → 3 chunks: [0..19], [20..39], [40..49].
func TestChunkManifestWindowing(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}

	intField := func(t *testing.T, m map[string]any, key string) int {
		t.Helper()
		v, ok := m[key].(int)
		if !ok {
			t.Fatalf("field %q not an int: %#v", key, m[key])
		}
		return v
	}
	seqRanges := func(chunks []ManifestChunkTuple) [][2]int {
		out := make([][2]int, len(chunks))
		for i, c := range chunks {
			out[i] = [2]int{c.FirstSeq, c.LastSeq}
		}
		return out
	}

	ptr := func(v int) *int { return &v }

	t.Run("limitChunks 只返回最新 N 个 chunk，total 仍为全量", func(t *testing.T) {
		res, err := service.ChunkManifest(ctx, narratorID, nil, &manifestWindow{limitChunks: ptr(2)})
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		chunks := res["chunks"].([]ManifestChunkTuple)
		got := seqRanges(chunks)
		want := [][2]int{{20, 39}, {40, 49}}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("expected newest 2 chunks %v, got %v", want, got)
		}
		if total := intField(t, res, "total"); total != 50 {
			t.Fatalf("expected total=50, got %d", total)
		}
		if wfi := intField(t, res, "windowFirstIndex"); wfi != 1 {
			t.Fatalf("expected windowFirstIndex=1, got %d", wfi)
		}
		if res["hasOlderChunks"] != true {
			t.Fatalf("expected hasOlderChunks=true, got %#v", res["hasOlderChunks"])
		}
	})

	t.Run("beforeSeq 向更早翻页，取紧邻的更旧 chunk", func(t *testing.T) {
		res, err := service.ChunkManifest(ctx, narratorID, nil, &manifestWindow{limitChunks: ptr(10), beforeSeq: ptr(20)})
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		chunks := res["chunks"].([]ManifestChunkTuple)
		got := seqRanges(chunks)
		if len(got) != 1 || got[0] != [2]int{0, 19} {
			t.Fatalf("expected [[0,19]], got %v", got)
		}
		if wfi := intField(t, res, "windowFirstIndex"); wfi != 0 {
			t.Fatalf("expected windowFirstIndex=0, got %d", wfi)
		}
		if res["hasOlderChunks"] != false {
			t.Fatalf("expected hasOlderChunks=false, got %#v", res["hasOlderChunks"])
		}
		if total := intField(t, res, "total"); total != 50 {
			t.Fatalf("expected total=50, got %d", total)
		}
	})

	t.Run("limitChunks 覆盖全部时返回完整 manifest 且 hasOlderChunks 为假", func(t *testing.T) {
		res, err := service.ChunkManifest(ctx, narratorID, nil, &manifestWindow{limitChunks: ptr(100)})
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		if chunks := res["chunks"].([]ManifestChunkTuple); len(chunks) != 3 {
			t.Fatalf("expected 3 chunks, got %d", len(chunks))
		}
		if wfi := intField(t, res, "windowFirstIndex"); wfi != 0 {
			t.Fatalf("expected windowFirstIndex=0, got %d", wfi)
		}
		if res["hasOlderChunks"] != false {
			t.Fatalf("expected hasOlderChunks=false, got %#v", res["hasOlderChunks"])
		}
	})

	t.Run("不传 window 时返回完整 manifest（向后兼容）", func(t *testing.T) {
		res, err := service.ChunkManifest(ctx, narratorID, nil, nil)
		if err != nil {
			t.Fatalf("manifest: %v", err)
		}
		if chunks := res["chunks"].([]ManifestChunkTuple); len(chunks) != 3 {
			t.Fatalf("expected 3 chunks, got %d", len(chunks))
		}
		if wfi := intField(t, res, "windowFirstIndex"); wfi != 0 {
			t.Fatalf("expected windowFirstIndex=0, got %d", wfi)
		}
		if res["hasOlderChunks"] != false {
			t.Fatalf("expected hasOlderChunks=false, got %#v", res["hasOlderChunks"])
		}
		if total := intField(t, res, "total"); total != 50 {
			t.Fatalf("expected total=50, got %d", total)
		}
	})
}

func TestChunksByRangeBoundaryFlags(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	// Seed 50 messages seq 0..49 so a count=1 window (rowLimit 20) leaves content
	// on both sides for paging. Mirrors the TS getChunksByRange boundary suite.
	for i := 0; i < 50; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}

	// Tail window: newest 20 messages, seq 30..49.
	tail, err := service.ChunksByRange(ctx, narratorID, nil, "older", intPtr(1))
	if err != nil {
		t.Fatalf("tail window: %v", err)
	}
	assertSeqBounds(t, "tail", tail, 30, 49)
	if !tail.HasOlder || tail.HasNewer {
		t.Fatalf("tail expected hasOlder=true hasNewer=false, got older=%v newer=%v", tail.HasOlder, tail.HasNewer)
	}

	// Page older from the tail's min seq (30): expect seq 10..29, both flags true.
	older, err := service.ChunksByRange(ctx, narratorID, tail.MinSeq, "older", intPtr(1))
	if err != nil {
		t.Fatalf("older window: %v", err)
	}
	assertSeqBounds(t, "older", older, 10, 29)
	if !older.HasOlder || !older.HasNewer {
		t.Fatalf("middle window expected both flags true, got older=%v newer=%v", older.HasOlder, older.HasNewer)
	}

	// Earliest page: page older starting just past the head → seq 0..9.
	head, err := service.ChunksByRange(ctx, narratorID, intPtr(10), "older", intPtr(1))
	if err != nil {
		t.Fatalf("head window: %v", err)
	}
	assertSeqBounds(t, "head", head, 0, 9)
	if head.HasOlder || !head.HasNewer {
		t.Fatalf("head expected hasOlder=false hasNewer=true, got older=%v newer=%v", head.HasOlder, head.HasNewer)
	}

	// Newer than seq 9 → seq 10..29, both flags true.
	newer, err := service.ChunksByRange(ctx, narratorID, intPtr(9), "newer", intPtr(1))
	if err != nil {
		t.Fatalf("newer window: %v", err)
	}
	assertSeqBounds(t, "newer", newer, 10, 29)
	if !newer.HasOlder || !newer.HasNewer {
		t.Fatalf("newer window expected both flags true, got older=%v newer=%v", newer.HasOlder, newer.HasNewer)
	}
}

func TestChunksByRangeFewerThanWindowHasNoNeighbors(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}
	res, err := service.ChunksByRange(ctx, narratorID, nil, "older", intPtr(1))
	if err != nil {
		t.Fatalf("small window: %v", err)
	}
	assertSeqBounds(t, "small", res, 0, 4)
	if res.HasOlder || res.HasNewer {
		t.Fatalf("under-one-window should report both flags false, got older=%v newer=%v", res.HasOlder, res.HasNewer)
	}
}

func TestChunksByRangeAttachesChildTree(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	childNarratorID := insertChildNarratorTestRow(t, sqlDB, "chunk_child")
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	assistant, err := service.addMessageWithBlocks(ctx, narratorID, "assistant", "launch child", []map[string]any{{"type": "tool_use", "id": "chunk_tool", "name": "Task", "input": map[string]any{"prompt": "go"}}}, nil)
	if err != nil {
		t.Fatalf("add assistant: %v", err)
	}
	insertToolCallTestRow(t, sqlDB, narratorID, assistant.ID, "chunk_tool")
	childID := insertChildMessageTestRow(t, sqlDB, childNarratorID, "chunk_tool", 1, "chunk child")

	result, err := service.ChunksByRange(ctx, narratorID, nil, "older", intPtr(1))
	if err != nil {
		t.Fatalf("chunk range: %v", err)
	}
	if len(result.Messages) != 1 || len(result.Messages[0].Children) != 1 || result.Messages[0].Children[0].ID != childID {
		t.Fatalf("expected child tree attached to top-level ref, got %#v", result.Messages)
	}
	if len(result.Messages[0].ToolCalls) != 1 || result.Messages[0].ToolCalls[0].ToolUseID != "chunk_tool" {
		t.Fatalf("expected tool call attached to top-level assistant message, got %#v", result.Messages[0].ToolCalls)
	}
}

func TestMessageLocationResolvesTopLevelSeq(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	childNarratorID := insertChildNarratorTestRow(t, sqlDB, "loc_child")
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	// Primary parent message owning a tool_use at seq 2, plus a child message.
	insertParentMessageWithRef(t, sqlDB, narratorID, "m-parent", "tu-parent", 2)
	childID := insertChildMessageTestRow(t, sqlDB, childNarratorID, "tu-parent", 3, "child target")

	// A primary-narrator child resolves to its parent's top-level seq.
	childLoc, err := service.MessageLocation(ctx, narratorID, childID)
	if err != nil {
		t.Fatalf("child location: %v", err)
	}
	if childLoc.MessageID != childID || childLoc.TopLevelMessageID != "m-parent" || childLoc.Seq != 2 {
		t.Fatalf("expected child→parent seq 2, got %#v", childLoc)
	}

	// A top-level message resolves to itself.
	parentLoc, err := service.MessageLocation(ctx, narratorID, "m-parent")
	if err != nil {
		t.Fatalf("parent location: %v", err)
	}
	if parentLoc.MessageID != "m-parent" || parentLoc.TopLevelMessageID != "m-parent" || parentLoc.Seq != 2 {
		t.Fatalf("expected parent→itself seq 2, got %#v", parentLoc)
	}

	// Unknown message id is a 404 (mirrors TS NotFoundError).
	if _, err := service.MessageLocation(ctx, narratorID, "does-not-exist"); err == nil {
		t.Fatalf("expected not-found error for unknown message id")
	}
}

func TestMessageLocationIgnoresDuplicateToolUseOutsideNarrator(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorOne := insertNarratorHandlerTestRow(t, sqlDB)
	narratorTwo := insertPrimaryNarratorWithID(t, sqlDB, "nar_two")
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	insertTopLevelMessageAtSeq(t, sqlDB, narratorOne, "wrong-parent", 0, "wrong parent")
	insertToolCallWithID(t, sqlDB, "tc_wrong_dup", narratorOne, "wrong-parent", "tu-dup")

	insertTopLevelMessageAtSeq(t, sqlDB, narratorTwo, "right-parent", 4, "right parent")
	insertToolCallWithID(t, sqlDB, "tc_right_dup", narratorTwo, "right-parent", "tu-dup")
	childID := insertChildMessageTestRow(t, sqlDB, narratorTwo, "tu-dup", 5, "child in narrator two")

	loc, err := service.MessageLocation(ctx, narratorTwo, childID)
	if err != nil {
		t.Fatalf("duplicate tool-use location: %v", err)
	}
	if loc.MessageID != childID || loc.TopLevelMessageID != "right-parent" || loc.Seq != 4 {
		t.Fatalf("expected narrator-two child to resolve to visible right-parent seq 4, got %#v", loc)
	}
}

func TestMessageLocationSubagentUsesOwnRef(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	markNarratorSubagent(t, sqlDB, narratorID)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	// A subagent's own child-shaped message (carries parentToolUseId, but is a
	// top-level ref on its own page) resolves to its own seq.
	childID := insertChildMessageTestRow(t, sqlDB, narratorID, "tu-parent", 7, "subagent child as top-level")

	loc, err := service.MessageLocation(ctx, narratorID, childID)
	if err != nil {
		t.Fatalf("subagent location: %v", err)
	}
	if loc.MessageID != childID || loc.TopLevelMessageID != childID || loc.Seq != 7 {
		t.Fatalf("expected subagent self-location seq 7, got %#v", loc)
	}
}

func TestChunkAndLocationRoutesAreServed(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	for i := 0; i < 3; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}

	manifest := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/chunk-manifest")
	if manifest.Code != http.StatusOK {
		t.Fatalf("chunk-manifest status=%d body=%s", manifest.Code, manifest.Body.String())
	}
	var manifestBody map[string]any
	if err := json.Unmarshal(manifest.Body.Bytes(), &manifestBody); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if _, ok := manifestBody["chunks"]; !ok {
		t.Fatalf("manifest body missing chunks: %#v", manifestBody)
	}

	chunks := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/chunks?direction=older&count=1")
	if chunks.Code != http.StatusOK {
		t.Fatalf("chunks status=%d body=%s", chunks.Code, chunks.Body.String())
	}
	var chunksBody map[string]any
	if err := json.Unmarshal(chunks.Body.Bytes(), &chunksBody); err != nil {
		t.Fatalf("decode chunks: %v", err)
	}
	for _, key := range []string{"messages", "minSeq", "maxSeq", "hasOlder", "hasNewer", "messageVersion", "pruneBoundaryMessageId", "prunedPercent"} {
		if _, ok := chunksBody[key]; !ok {
			t.Fatalf("chunks body missing %q: %#v", key, chunksBody)
		}
	}

	loc := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/message-location/m1")
	if loc.Code != http.StatusOK {
		t.Fatalf("message-location status=%d body=%s", loc.Code, loc.Body.String())
	}
	var locBody map[string]any
	if err := json.Unmarshal(loc.Body.Bytes(), &locBody); err != nil {
		t.Fatalf("decode location: %v", err)
	}
	if locBody["topLevelMessageId"] != "m1" || locBody["seq"] != float64(1) {
		t.Fatalf("expected m1 self-location seq 1, got %#v", locBody)
	}
}

func TestChunkManifestCacheHitServesStaleStructureUntilVersionBumps(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	for i := 0; i < 25; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}

	first, err := service.ChunkManifest(ctx, narratorID, nil, nil)
	if err != nil {
		t.Fatalf("first manifest: %v", err)
	}
	if first["total"] != 25 {
		t.Fatalf("expected total=25 on first load, got %#v", first["total"])
	}

	// Mutate the ref table WITHOUT bumping message_version. A cache hit must
	// return the previously-computed (now structurally stale) manifest, proving
	// the second call did not re-scan refs.
	if _, err := sqlDB.Exec(`DELETE FROM narrator_message_refs WHERE narrator_id = ?`, narratorID); err != nil {
		t.Fatalf("delete refs: %v", err)
	}
	cached, err := service.ChunkManifest(ctx, narratorID, nil, nil)
	if err != nil {
		t.Fatalf("cached manifest: %v", err)
	}
	if cached["total"] != 25 {
		t.Fatalf("expected cache hit to still report total=25, got %#v", cached["total"])
	}
	if chunks, ok := cached["chunks"].([]ManifestChunkTuple); !ok || len(chunks) != 2 {
		t.Fatalf("expected cache hit to return 2 cached chunks, got %#v", cached["chunks"])
	}

	// Bumping message_version invalidates the entry; the next call re-scans and
	// observes the now-empty ref table.
	if _, err := sqlDB.Exec(`UPDATE narrators SET message_version = message_version + 1 WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	fresh, err := service.ChunkManifest(ctx, narratorID, nil, nil)
	if err != nil {
		t.Fatalf("fresh manifest: %v", err)
	}
	if fresh["total"] != 0 {
		t.Fatalf("expected re-scan after version bump to report total=0, got %#v", fresh["total"])
	}
	if chunks, ok := fresh["chunks"].([]ManifestChunkTuple); !ok || len(chunks) != 0 {
		t.Fatalf("expected re-scan to return 0 chunks, got %#v", fresh["chunks"])
	}
}

func TestChunkManifestCacheIsolatedPerServiceInstance(t *testing.T) {
	// Two independent in-memory DBs that share the same narrator id + initial
	// messageVersion must NOT see each other's cached manifest. This is the
	// regression guard for why the cache is a Service field rather than a package
	// global (TS keeps a module-level map; Go test DBs all reuse "nar_test").
	sqlDBA := openNarratorHandlerTestDB(t)
	defer sqlDBA.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDBA)
	serviceA := &Service{DB: sqlDBA}
	for i := 0; i < 3; i++ {
		insertTopLevelMessageAtSeq(t, sqlDBA, narratorID, "m"+itoa(i), i, "msg")
	}
	ctx := context.Background()
	if _, err := serviceA.ChunkManifest(ctx, narratorID, nil, nil); err != nil {
		t.Fatalf("warm serviceA cache: %v", err)
	}

	sqlDBB := openNarratorHandlerTestDB(t)
	defer sqlDBB.Close()
	if got := insertNarratorHandlerTestRow(t, sqlDBB); got != narratorID {
		t.Fatalf("expected both DBs to reuse the same narrator id, got %q", got)
	}
	serviceB := &Service{DB: sqlDBB}
	for i := 0; i < 7; i++ {
		insertTopLevelMessageAtSeq(t, sqlDBB, narratorID, "m"+itoa(i), i, "msg")
	}
	resB, err := serviceB.ChunkManifest(ctx, narratorID, nil, nil)
	if err != nil {
		t.Fatalf("serviceB manifest: %v", err)
	}
	if resB["total"] != 7 {
		t.Fatalf("serviceB must compute its own total=7, not serviceA's cached 3, got %#v", resB["total"])
	}
}

func TestManifestLRUEvictsLeastRecentlyUsed(t *testing.T) {
	cache := newManifestLRUCache()
	mk := func(version int) computedManifest {
		return computedManifest{messageVersion: version, total: version}
	}

	// Fill to the limit, then touch key "0" so it is most-recently-used.
	for i := 0; i < manifestCacheLimit; i++ {
		cache.set("n"+itoa(i), mk(1))
	}
	if _, ok := cache.get("n0", 1); !ok {
		t.Fatalf("n0 should still be cached before overflow")
	}

	// Inserting one more must evict the LRU entry. "n1" is now least-recently
	// used (n0 was just touched), so it should be the eviction victim.
	cache.set("n_overflow", mk(1))
	if cache.ll.Len() != manifestCacheLimit {
		t.Fatalf("cache should stay bounded at %d, got %d", manifestCacheLimit, cache.ll.Len())
	}
	if _, ok := cache.get("n1", 1); ok {
		t.Fatalf("n1 should have been evicted as least-recently-used")
	}
	if _, ok := cache.get("n0", 1); !ok {
		t.Fatalf("n0 should survive eviction because it was touched")
	}
	if _, ok := cache.get("n_overflow", 1); !ok {
		t.Fatalf("freshly inserted n_overflow should be present")
	}
}

func TestManifestLRUVersionMismatchIsMiss(t *testing.T) {
	cache := newManifestLRUCache()
	cache.set("n", computedManifest{messageVersion: 5, total: 10})
	if _, ok := cache.get("n", 5); !ok {
		t.Fatalf("matching version should hit")
	}
	if _, ok := cache.get("n", 6); ok {
		t.Fatalf("mismatched version must miss")
	}
}

// TestChunkManifestConcurrentAccessIsRaceFree drives ChunkManifest from many
// goroutines while another set bumps message_version, exercising the
// manifestCacheMu-guarded read/write/evict paths under -race. Each narrator id
// is distinct so the cache also crosses the LRU eviction threshold.
//
// The shared :memory: DB keeps its single-connection pool (set by
// openNarratorHandlerTestDB) so every goroutine sees the same schema/rows;
// SQLite serializes the DB access while the concurrent goroutines still contend
// on manifestCacheMu, which is what -race verifies here.
func TestChunkManifestConcurrentAccessIsRaceFree(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	const narratorCount = 80 // > manifestCacheLimit (64) to force evictions
	for n := 0; n < narratorCount; n++ {
		id := "nar_" + itoa(n)
		insertPrimaryNarratorWithID(t, sqlDB, id)
		for i := 0; i < 5; i++ {
			insertTopLevelMessageAtSeq(t, sqlDB, id, id+"_m"+itoa(i), i, "msg")
		}
	}

	var wg sync.WaitGroup
	for n := 0; n < narratorCount; n++ {
		id := "nar_" + itoa(n)
		wg.Add(2)
		go func() {
			defer wg.Done()
			for r := 0; r < 5; r++ {
				if _, err := service.ChunkManifest(ctx, id, nil, nil); err != nil {
					t.Errorf("concurrent manifest %s: %v", id, err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := sqlDB.Exec(`UPDATE narrators SET message_version = message_version + 1 WHERE id = ?`, id); err != nil {
				t.Errorf("concurrent version bump %s: %v", id, err)
			}
		}()
	}
	wg.Wait()
}

func TestChunksByRangeCountParity(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	ctx := context.Background()

	// 200 messages so a 7-chunk default window (140 rows) is distinguishable from
	// both a clamped 1-chunk (20) and a clamped 20-chunk (200/all) window.
	for i := 0; i < 200; i++ {
		insertTopLevelMessageAtSeq(t, sqlDB, narratorID, "m"+itoa(i), i, "msg")
	}

	// nil count → default 7 chunks (140 rows): seq 60..199.
	def, err := service.ChunksByRange(ctx, narratorID, nil, "older", nil)
	if err != nil {
		t.Fatalf("default count: %v", err)
	}
	if len(def.Messages) != 140 {
		t.Fatalf("nil count should default to 7 chunks (140 rows), got %d", len(def.Messages))
	}

	// Explicit 0/negative clamps UP to 1 chunk (20 rows) — matching the TS
	// Math.max(count, 1) — rather than falling back to the nil default of 7.
	zero, err := service.ChunksByRange(ctx, narratorID, nil, "older", intPtr(0))
	if err != nil {
		t.Fatalf("zero count: %v", err)
	}
	if len(zero.Messages) != 20 {
		t.Fatalf("explicit count=0 should clamp to 1 chunk (20 rows), got %d", len(zero.Messages))
	}
	neg, err := service.ChunksByRange(ctx, narratorID, nil, "older", intPtr(-5))
	if err != nil {
		t.Fatalf("negative count: %v", err)
	}
	if len(neg.Messages) != 20 {
		t.Fatalf("negative count should clamp to 1 chunk (20 rows), got %d", len(neg.Messages))
	}

	// Over-cap clamps DOWN to 20 chunks (400 rows → all 200 here).
	over, err := service.ChunksByRange(ctx, narratorID, nil, "older", intPtr(999))
	if err != nil {
		t.Fatalf("over-cap count: %v", err)
	}
	if len(over.Messages) != 200 {
		t.Fatalf("over-cap count should clamp to 20 chunks (all 200 rows), got %d", len(over.Messages))
	}
}

func assertSeqBounds(t *testing.T, label string, r ChunkRange, wantMin, wantMax int) {
	t.Helper()
	if r.MinSeq == nil || r.MaxSeq == nil {
		t.Fatalf("%s window expected non-nil min/max, got %#v", label, r)
	}
	if *r.MinSeq != wantMin || *r.MaxSeq != wantMax {
		t.Fatalf("%s window expected seq [%d..%d], got [%d..%d]", label, wantMin, wantMax, *r.MinSeq, *r.MaxSeq)
	}
}

func intPtr(v int) *int { return &v }
