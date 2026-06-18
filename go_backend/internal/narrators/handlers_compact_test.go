package narrators

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestAddMessageReturnsSequenceQueryErrorWithoutInsert(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	prepareBrokenNarratorMessageRefsSeq(t, sqlDB, narratorID)
	service := &Service{DB: sqlDB}

	_, err := service.addMessage(context.Background(), narratorID, "user", "after sequence failure", nil)
	if err == nil || !strings.Contains(err.Error(), "query next narrator message sequence") {
		t.Fatalf("expected sequence query error, got %v", err)
	}
	assertNoMessageRefSeqFallbackInsert(t, sqlDB, narratorID, "after sequence failure")
}

func TestAddMessageWithCommandReturnsSequenceQueryErrorWithoutInsert(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	prepareBrokenNarratorMessageRefsSeq(t, sqlDB, narratorID)
	service := &Service{DB: sqlDB}

	_, err := service.addMessageWithBlocksWithCommandAndParentToolUse(context.Background(), narratorID, "user", "command after sequence failure", []map[string]any{{"type": "text", "text": "command after sequence failure"}}, nil, "run command", "")
	if err == nil || !strings.Contains(err.Error(), "query next narrator message sequence") {
		t.Fatalf("expected command sequence query error, got %v", err)
	}
	assertNoMessageRefSeqFallbackInsert(t, sqlDB, narratorID, "command after sequence failure")
}

func prepareBrokenNarratorMessageRefsSeq(t *testing.T, sqlDB *sql.DB, narratorID string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('existing-seq-message', ?, 'user', '[{"type":"text","text":"existing"}]', 'existing', 'now')`, narratorID); err != nil {
		t.Fatalf("insert existing message: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('existing-seq-ref', ?, 'existing-seq-message', 0, 0)`, narratorID); err != nil {
		t.Fatalf("insert existing ref: %v", err)
	}
	stmts := []string{
		`ALTER TABLE narrator_message_refs RENAME TO narrator_message_refs_real`,
		`CREATE VIEW narrator_message_refs AS SELECT id, narrator_id, message_id, json_extract('not-json', '$') AS seq, is_compact, segment_compact_id FROM narrator_message_refs_real`,
		`CREATE TRIGGER narrator_message_refs_insert INSTEAD OF INSERT ON narrator_message_refs BEGIN INSERT INTO narrator_message_refs_real (id, narrator_id, message_id, seq, is_compact) VALUES (NEW.id, NEW.narrator_id, NEW.message_id, NEW.seq, NEW.is_compact); END;`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken refs view: %v", err)
		}
	}
}

func assertNoMessageRefSeqFallbackInsert(t *testing.T, sqlDB *sql.DB, narratorID, text string) {
	t.Helper()
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND content_text = ?`, narratorID, text).Scan(&messageCount); err != nil {
		t.Fatalf("count new messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("sequence query failure should roll back new message, count=%d", messageCount)
	}
	var refCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs_real WHERE narrator_id = ?`, narratorID).Scan(&refCount); err != nil {
		t.Fatalf("count backing refs: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("sequence query failure should not insert fallback ref, count=%d", refCount)
	}
}

func TestCompactCreatesMarkerAndClearsPruneFields(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	first, err := service.addMessage(context.Background(), narratorID, "user", "first context message", nil)
	if err != nil {
		t.Fatalf("insert first message: %v", err)
	}
	if _, err := service.addMessage(context.Background(), narratorID, "assistant", "second context message", nil); err != nil {
		t.Fatalf("insert second message: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET api_conversation_id = 'conv_1', context_summary = 'old summary', prune_boundary_message_id = ?, pruned_percent = 42 WHERE id = ?`, first.ID, narratorID); err != nil {
		t.Fatalf("seed prune fields: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/compact", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode compact response: %v", err)
	}
	if body["fallbackSummary"] != true {
		t.Fatalf("expected explicit fallbackSummary flag, got %#v", body)
	}
	if body["fallbackReason"] != compactFallbackSummaryReason {
		t.Fatalf("expected compact fallback reason, got %#v", body)
	}
	if body["summarySource"] != "deterministic_fallback" || body["summaryQuality"] != "deterministic_preview" || body["modelSummaryGenerated"] != false {
		t.Fatalf("expected compact deterministic fallback summary metadata, got %#v", body)
	}
	summary, _ := body["summary"].(string)
	if !strings.Contains(summary, "fallback summary generated by Go backend") {
		t.Fatalf("summary should explicitly identify fallback, got %q", summary)
	}
	message := body["message"].(map[string]any)
	messageID := message["id"].(string)

	var isCompact int
	var contentJSON string
	if err := sqlDB.QueryRow(`SELECT r.is_compact, m.content_json FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.message_id = ?`, narratorID, messageID).Scan(&isCompact, &contentJSON); err != nil {
		t.Fatalf("load compact marker: %v", err)
	}
	if isCompact != 1 {
		t.Fatalf("compact marker ref should be is_compact=1, got %d", isCompact)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &blocks); err != nil {
		t.Fatalf("decode compact content: %v", err)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "compact" || blocks[0]["status"] != "compacted" || blocks[0]["summary"] != summary {
		t.Fatalf("unexpected compact content blocks: %#v", blocks)
	}
	var apiConversationID, pruneBoundary sql.NullString
	var prunedPercent sql.NullInt64
	var contextSummary sql.NullString
	if err := sqlDB.QueryRow(`SELECT api_conversation_id, context_summary, prune_boundary_message_id, pruned_percent FROM narrators WHERE id = ?`, narratorID).Scan(&apiConversationID, &contextSummary, &pruneBoundary, &prunedPercent); err != nil {
		t.Fatalf("load narrator fields: %v", err)
	}
	if apiConversationID.Valid || pruneBoundary.Valid || prunedPercent.Valid {
		t.Fatalf("compact should clear api/prune fields, got api=%#v boundary=%#v percent=%#v", apiConversationID, pruneBoundary, prunedPercent)
	}
	if !contextSummary.Valid || contextSummary.String != summary {
		t.Fatalf("compact should store context summary, got %#v", contextSummary)
	}
}

func TestCompactReturnsErrorWhenNarratorSummaryUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	if _, err := service.addMessage(context.Background(), narratorID, "user", "context message", nil); err != nil {
		t.Fatalf("insert context message: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_context_summary BEFORE UPDATE OF context_summary ON narrators BEGIN SELECT RAISE(ABORT, 'context summary update blocked'); END;`); err != nil {
		t.Fatalf("create context summary trigger: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/compact", `{"summary":"summary"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected compact update failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPersistPlanCompactReturnsErrorWhenNarratorSummaryUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_plan_context_summary BEFORE UPDATE OF context_summary ON narrators BEGIN SELECT RAISE(ABORT, 'plan context summary update blocked'); END;`); err != nil {
		t.Fatalf("create context summary trigger: %v", err)
	}

	_, err := service.persistPlanCompact(context.Background(), narratorID, "Implement the plan by updating the service, adding regression tests, and validating target packages.")
	if err == nil || !strings.Contains(err.Error(), "plan context summary update blocked") {
		t.Fatalf("expected plan context summary update error, got %v", err)
	}
}

// TestPersistPlanCompactContentTextMatchesTS pins the stored content_text to the TS
// persistPlanMessage shape (server/services/narrator-persistence.ts):
// `[Plan] ${content.slice(0, 200)}...` — the plan body is sliced to 200 UTF-16
// code units, wrapped with the "[Plan] " prefix and an always-present trailing
// "...". Go previously truncated the whole "[Plan] " + plan to 220 bytes with a
// conditional "...", diverging on slice target, metric, boundary, and the suffix.
func TestPersistPlanCompactContentTextMatchesTS(t *testing.T) {
	readContentText := func(t *testing.T, sqlDB *sql.DB, msgID string) string {
		t.Helper()
		var ct string
		if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE id = ?`, msgID).Scan(&ct); err != nil {
			t.Fatalf("read content_text: %v", err)
		}
		return ct
	}

	// Short plan: always-on "..." even though no truncation happens.
	shortDB := openNarratorHandlerTestDB(t)
	defer shortDB.Close()
	shortService := &Service{DB: shortDB}
	short := insertNarratorHandlerTestRow(t, shortDB)
	msg, err := shortService.persistPlanCompact(context.Background(), short, "do the thing")
	if err != nil {
		t.Fatalf("persist short plan: %v", err)
	}
	if got := readContentText(t, shortDB, msg.ID); got != "[Plan] do the thing..." {
		t.Fatalf("short plan content_text = %q, want %q", got, "[Plan] do the thing...")
	}

	// Long CJK plan: body sliced to 200 UTF-16 units (valid UTF-8), prefix + "...".
	cjkDB := openNarratorHandlerTestDB(t)
	defer cjkDB.Close()
	cjkService := &Service{DB: cjkDB}
	cjk := insertNarratorHandlerTestRow(t, cjkDB)
	plan := strings.Repeat("章", 250)
	msg2, err := cjkService.persistPlanCompact(context.Background(), cjk, plan)
	if err != nil {
		t.Fatalf("persist cjk plan: %v", err)
	}
	got := readContentText(t, cjkDB, msg2.ID)
	want := "[Plan] " + strings.Repeat("章", 200) + "..."
	if got != want {
		t.Fatalf("cjk plan content_text mismatch:\n got %q\nwant %q", got, want)
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatalf("cjk plan content_text has invalid UTF-8 (U+FFFD): %q", got)
		}
	}
}

// TestUpdateMessageSummaryContentTextMatchesTS pins the PATCH /compact/{messageId}
// content_text against TS updateCompactSummary (server/services/narrator-messages.ts):
// `${prefix} ${summary.slice(0, 200)}...` where prefix is "[Plan]" for a plan-subtype
// block else "[Compact]", with the summary sliced to 200 UTF-16 units and a trailing
// "..." always appended. The segment-compact branch keeps `[Segment Compact]\n${summary}`
// (full, no slice). Go previously used `[Compact] ` + full summary with no slice/"..."
// and ignored the plan subtype.
func TestUpdateMessageSummaryContentTextMatchesTS(t *testing.T) {
	readContentText := func(t *testing.T, sqlDB *sql.DB, msgID string) string {
		t.Helper()
		var ct string
		if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE id = ?`, msgID).Scan(&ct); err != nil {
			t.Fatalf("read content_text: %v", err)
		}
		return ct
	}
	insertCompactMsg := func(t *testing.T, sqlDB *sql.DB, narratorID, msgID, contentJSON string) {
		t.Helper()
		if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, 'system', ?, 'old', 'now')`, msgID, narratorID, contentJSON); err != nil {
			t.Fatalf("insert compact message: %v", err)
		}
	}

	cases := []struct {
		name        string
		contentJSON string
		summary     string
		want        string
	}{
		{"compact short", `[{"type":"compact","status":"compacted","summary":"old"}]`, "a new summary", "[Compact] a new summary..."},
		{"plan subtype", `[{"type":"compact","status":"compacted","subtype":"plan","summary":"old"}]`, "the plan body", "[Plan] the plan body..."},
		{"segment full", `[{"type":"segment_compact","status":"compacted","summary":"old"}]`, "seg summary", "[Segment Compact]\nseg summary"},
		{"compact cjk slice", `[{"type":"compact","status":"compacted","summary":"old"}]`, strings.Repeat("章", 250), "[Compact] " + strings.Repeat("章", 200) + "..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB := openNarratorHandlerTestDB(t)
			defer sqlDB.Close()
			narratorID := insertNarratorHandlerTestRow(t, sqlDB)
			insertCompactMsg(t, sqlDB, narratorID, "msg1", tc.contentJSON)
			h := NewHandler(&Service{DB: sqlDB})
			if err := h.updateMessageSummary(context.Background(), narratorID, "msg1", tc.summary); err != nil {
				t.Fatalf("updateMessageSummary: %v", err)
			}
			if got := readContentText(t, sqlDB, "msg1"); got != tc.want {
				t.Fatalf("%s content_text = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestSegmentCompactReturnsErrorWhenNarratorUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	first, err := service.addMessage(context.Background(), narratorID, "user", "hide one", nil)
	if err != nil {
		t.Fatalf("insert first message: %v", err)
	}
	second, err := service.addMessage(context.Background(), narratorID, "assistant", "hide two", nil)
	if err != nil {
		t.Fatalf("insert second message: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_api_conversation_clear BEFORE UPDATE OF api_conversation_id ON narrators BEGIN SELECT RAISE(ABORT, 'api conversation update blocked'); END;`); err != nil {
		t.Fatalf("create api conversation trigger: %v", err)
	}

	payload := `{"messageIds":["` + first.ID + `","` + second.ID + `"],"summary":"segment summary"}`
	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/segment-compact", payload)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected segment compact update failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSegmentCompactInsertsMarkerAndHidesRefs(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	first, err := service.addMessage(context.Background(), narratorID, "user", "keep visible", nil)
	if err != nil {
		t.Fatalf("insert first message: %v", err)
	}
	second, err := service.addMessage(context.Background(), narratorID, "assistant", "hide me", nil)
	if err != nil {
		t.Fatalf("insert second message: %v", err)
	}
	third, err := service.addMessage(context.Background(), narratorID, "user", "hide me too", nil)
	if err != nil {
		t.Fatalf("insert third message: %v", err)
	}

	payload := `{"messageIds":["` + second.ID + `","` + third.ID + `"]}`
	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/segment-compact", payload)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode segment response: %v", err)
	}
	if got := int(body["messageCount"].(float64)); got != 2 {
		t.Fatalf("expected two hidden messages, got %d", got)
	}
	if body["fallbackSummary"] != true || body["fallbackReason"] != compactFallbackSummaryReason {
		t.Fatalf("expected segment compact fallback diagnostics, got %#v", body)
	}
	if body["summarySource"] != "deterministic_fallback" || body["summaryQuality"] != "deterministic_preview" || body["modelSummaryGenerated"] != false {
		t.Fatalf("expected segment compact deterministic fallback summary metadata, got %#v", body)
	}
	message := body["message"].(map[string]any)
	markerID := message["id"].(string)
	summary := body["summary"].(string)
	if !strings.Contains(summary, "Segment compact fallback summary generated by Go backend") {
		t.Fatalf("segment compact summary should identify fallback, got %q", summary)
	}

	rows, err := sqlDB.Query(`SELECT message_id, seq, COALESCE(segment_compact_id, '') FROM narrator_message_refs WHERE narrator_id = ? ORDER BY seq ASC`, narratorID)
	if err != nil {
		t.Fatalf("query refs: %v", err)
	}
	defer rows.Close()
	refs := []struct {
		messageID string
		seq       int
		segmentID string
	}{}
	for rows.Next() {
		var ref struct {
			messageID string
			seq       int
			segmentID string
		}
		if err := rows.Scan(&ref.messageID, &ref.seq, &ref.segmentID); err != nil {
			t.Fatalf("scan ref: %v", err)
		}
		refs = append(refs, ref)
	}
	if len(refs) != 4 || refs[0].messageID != first.ID || refs[1].messageID != markerID {
		t.Fatalf("marker should be inserted at first hidden seq; refs=%#v", refs)
	}
	for _, ref := range refs {
		if ref.messageID == second.ID || ref.messageID == third.ID {
			if ref.segmentID != markerID {
				t.Fatalf("hidden ref %s should point at marker %s, got %q", ref.messageID, markerID, ref.segmentID)
			}
		}
	}
	var role, contentJSON string
	var isCompact int
	if err := sqlDB.QueryRow(`SELECT m.role, r.is_compact, m.content_json FROM narrator_messages m JOIN narrator_message_refs r ON r.message_id = m.id WHERE m.id = ?`, markerID).Scan(&role, &isCompact, &contentJSON); err != nil {
		t.Fatalf("load segment marker: %v", err)
	}
	if role != "user" || isCompact != 0 {
		t.Fatalf("segment marker should match TS role/isCompact, got role=%s isCompact=%d", role, isCompact)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &blocks); err != nil {
		t.Fatalf("decode segment content: %v", err)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "segment_compact" || blocks[0]["status"] != "compacted" || blocks[0]["summary"] != summary {
		t.Fatalf("unexpected segment compact content blocks: %#v", blocks)
	}

	summaryResp := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/segment-compact/"+markerID)
	if summaryResp.Code != http.StatusOK {
		t.Fatalf("segment compact summary status=%d body=%s", summaryResp.Code, summaryResp.Body.String())
	}
	summaryBody := decodeNarratorHandlerBody(t, summaryResp)
	summaryText := stringFromAny(summaryBody["summary"])
	if summaryBody["messageId"] != markerID || !strings.Contains(summaryText, summary) {
		t.Fatalf("segment summary route should expose marker summary, got %#v", summaryBody)
	}
	messagesResp := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/segment-compact/"+markerID+"/messages")
	if messagesResp.Code != http.StatusOK {
		t.Fatalf("segment compact messages status=%d body=%s", messagesResp.Code, messagesResp.Body.String())
	}
	messagesBody := decodeNarratorHandlerBody(t, messagesResp)
	hidden, ok := messagesBody["messages"].([]any)
	if !ok || len(hidden) != 2 {
		t.Fatalf("segment messages route should expose two hidden refs, got %#v", messagesBody)
	}

	page, err := service.MessagesCursor(context.Background(), narratorID, 10, "", "older")
	if err != nil {
		t.Fatalf("load visible messages: %v", err)
	}
	visibleIDs := make([]string, 0, len(page.Messages))
	for _, msg := range page.Messages {
		visibleIDs = append(visibleIDs, msg.ID)
	}
	if len(visibleIDs) != 2 || visibleIDs[0] != first.ID || visibleIDs[1] != markerID {
		t.Fatalf("segment-hidden refs should be hidden from message pages, got %v", visibleIDs)
	}
}

func TestDeleteMessageDeletesTargetAndFollowingMessages(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	first, err := service.addMessage(context.Background(), narratorID, "user", "first", nil)
	if err != nil {
		t.Fatalf("insert first message: %v", err)
	}
	second, err := service.addMessage(context.Background(), narratorID, "assistant", "second", nil)
	if err != nil {
		t.Fatalf("insert second message: %v", err)
	}
	third, err := service.addMessage(context.Background(), narratorID, "user", "third", nil)
	if err != nil {
		t.Fatalf("insert third message: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET api_conversation_id = 'conv_1', prune_boundary_message_id = ?, pruned_percent = 50 WHERE id = ?`, second.ID, narratorID); err != nil {
		t.Fatalf("seed narrator prune fields: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/messages/"+second.ID, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected delete message to succeed, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeNarratorHandlerBody(t, w)
	if body["deletedCount"] != float64(2) {
		t.Fatalf("expected deletedCount=2 for target and following messages, got %#v", body)
	}
	for _, tc := range []struct {
		id       string
		wantRefs int
		wantMsgs int
		label    string
	}{
		{id: first.ID, wantRefs: 1, wantMsgs: 1, label: "first"},
		{id: second.ID, wantRefs: 0, wantMsgs: 0, label: "second"},
		{id: third.ID, wantRefs: 0, wantMsgs: 0, label: "third"},
	} {
		var refCount, msgCount int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, tc.id).Scan(&refCount); err != nil {
			t.Fatalf("count %s refs: %v", tc.label, err)
		}
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND id = ?`, narratorID, tc.id).Scan(&msgCount); err != nil {
			t.Fatalf("count %s messages: %v", tc.label, err)
		}
		if refCount != tc.wantRefs || msgCount != tc.wantMsgs {
			t.Fatalf("%s counts refs/messages = %d/%d, want %d/%d", tc.label, refCount, msgCount, tc.wantRefs, tc.wantMsgs)
		}
	}
	var apiConversationID, pruneBoundary sql.NullString
	var prunedPercent sql.NullInt64
	var messageCount int
	if err := sqlDB.QueryRow(`SELECT api_conversation_id, prune_boundary_message_id, pruned_percent, message_count FROM narrators WHERE id = ?`, narratorID).Scan(&apiConversationID, &pruneBoundary, &prunedPercent, &messageCount); err != nil {
		t.Fatalf("load narrator after delete: %v", err)
	}
	if apiConversationID.Valid || pruneBoundary.Valid || prunedPercent.Valid || messageCount != 1 {
		t.Fatalf("delete should clear api/prune fields and reduce message_count, got api=%#v boundary=%#v percent=%#v count=%d", apiConversationID, pruneBoundary, prunedPercent, messageCount)
	}
}

func TestDeleteCompactRejectsNonCompactMessage(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	msg, err := service.addMessage(context.Background(), narratorID, "user", "plain user message", nil)
	if err != nil {
		t.Fatalf("insert user message: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/compact/"+msg.ID, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected non-compact delete to return 400, got %d body=%s", w.Code, w.Body.String())
	}
	var refCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, msg.ID).Scan(&refCount); err != nil {
		t.Fatalf("count message ref: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("non-compact message ref should remain, count=%d", refCount)
	}
}

func TestDismissErrorRejectsNonErrorMessage(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	msg, err := service.addMessageWithBlocks(context.Background(), narratorID, "system", "ordinary system message", []map[string]any{{"type": "text", "text": "ordinary"}}, nil)
	if err != nil {
		t.Fatalf("insert system message: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/error-messages/"+msg.ID, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected non-error dismiss to return 400, got %d body=%s", w.Code, w.Body.String())
	}
	var refCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, msg.ID).Scan(&refCount); err != nil {
		t.Fatalf("count message ref: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("non-error message ref should remain, count=%d", refCount)
	}
}

func TestDeleteSegmentCompactRejectsNonSegmentMessage(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	msg, err := service.addMessage(context.Background(), narratorID, "user", "plain user message", nil)
	if err != nil {
		t.Fatalf("insert user message: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/segment-compact/"+msg.ID, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected non-segment delete to return 400, got %d body=%s", w.Code, w.Body.String())
	}
	var refCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, msg.ID).Scan(&refCount); err != nil {
		t.Fatalf("count message ref: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("non-segment message ref should remain, count=%d", refCount)
	}
}

func TestDeleteCompactRollsBackWhenNarratorUpdateFails(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}
	if _, err := service.addMessage(context.Background(), narratorID, "user", "context message", nil); err != nil {
		t.Fatalf("insert context message: %v", err)
	}

	compact := serveNarratorHandlerRawJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/compact", `{"summary":"manual summary"}`)
	if compact.Code != http.StatusOK {
		t.Fatalf("expected compact setup to succeed, got %d body=%s", compact.Code, compact.Body.String())
	}
	compactBody := decodeNarratorHandlerBody(t, compact)
	compactID := compactBody["message"].(map[string]any)["id"].(string)
	if _, err := sqlDB.Exec(`CREATE TRIGGER narrator_update_fail BEFORE UPDATE ON narrators BEGIN SELECT RAISE(ABORT, 'update blocked'); END;`); err != nil {
		t.Fatalf("create failing update trigger: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodDelete, "/narrators/"+narratorID+"/compact/"+compactID, `{}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected compact delete update failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
	var refCount, messageCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, compactID).Scan(&refCount); err != nil {
		t.Fatalf("count compact ref: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND id = ?`, narratorID, compactID).Scan(&messageCount); err != nil {
		t.Fatalf("count compact message: %v", err)
	}
	if refCount != 1 || messageCount != 1 {
		t.Fatalf("failed compact delete should roll back ref/message, ref=%d message=%d", refCount, messageCount)
	}
}

func TestClearContextCreatesEmptyCompactMarker(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET api_conversation_id = 'conv_1', context_summary = 'old', prune_boundary_message_id = 'old_msg', pruned_percent = 88 WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("seed narrator fields: %v", err)
	}

	w := serveNarratorHandlerRawJSON(sqlDB, http.MethodPost, "/narrators/"+narratorID+"/clear-context", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode clear-context response: %v", err)
	}
	message := body["message"].(map[string]any)
	messageID := message["id"].(string)
	var text, contentJSON string
	var isCompact int
	if err := sqlDB.QueryRow(`SELECT m.content_text, m.content_json, r.is_compact FROM narrator_messages m JOIN narrator_message_refs r ON r.message_id = m.id WHERE m.id = ?`, messageID).Scan(&text, &contentJSON, &isCompact); err != nil {
		t.Fatalf("load clear context marker: %v", err)
	}
	if text != "[Context cleared]" || isCompact != 1 {
		t.Fatalf("unexpected clear-context marker text/isCompact: %q %d", text, isCompact)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &blocks); err != nil {
		t.Fatalf("decode clear content: %v", err)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "compact" || blocks[0]["status"] != "compacted" || blocks[0]["summary"] != "" {
		t.Fatalf("unexpected clear-context compact block: %#v", blocks)
	}
	var apiConversationID, contextSummary, pruneBoundary sql.NullString
	var prunedPercent sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT api_conversation_id, context_summary, prune_boundary_message_id, pruned_percent FROM narrators WHERE id = ?`, narratorID).Scan(&apiConversationID, &contextSummary, &pruneBoundary, &prunedPercent); err != nil {
		t.Fatalf("load narrator fields: %v", err)
	}
	if apiConversationID.Valid || contextSummary.Valid || pruneBoundary.Valid || prunedPercent.Valid {
		t.Fatalf("clear-context should clear context/api/prune fields, got api=%#v context=%#v boundary=%#v percent=%#v", apiConversationID, contextSummary, pruneBoundary, prunedPercent)
	}
}

func decodeNarratorHandlerBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode narrator handler response: %v; raw=%s", err, w.Body.String())
	}
	return body
}

func serveNarratorHandlerRawJSON(sqlDB *sql.DB, method string, path string, body string) *httptest.ResponseRecorder {
	h := NewHandler(&Service{DB: sqlDB})
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestCreateCompactMessageContentTextMatchesTS pins createCompactMessage (manual
// compact endpoint) content_text against the TS finalizeCompactingMessage format
// (narrator-persistence.ts): regular compact -> `[Compact] ${summary.slice(0, 200)}...`
// (200 UTF-16 units, always-on "..."), segment compact -> `[Segment Compact]\n${summary}`
// (full). Go previously used `[Compact]\n` + full summary for the regular compact.
func TestCreateCompactMessageContentTextMatchesTS(t *testing.T) {
	readContentText := func(t *testing.T, sqlDB *sql.DB, msgID string) string {
		t.Helper()
		var ct string
		if err := sqlDB.QueryRow(`SELECT content_text FROM narrator_messages WHERE id = ?`, msgID).Scan(&ct); err != nil {
			t.Fatalf("read content_text: %v", err)
		}
		return ct
	}

	regDB := openNarratorHandlerTestDB(t)
	defer regDB.Close()
	regNar := insertNarratorHandlerTestRow(t, regDB)
	h := NewHandler(&Service{DB: regDB})
	msg, err := h.createCompactMessage(context.Background(), regNar, "a compact summary", "compact", nil)
	if err != nil {
		t.Fatalf("createCompactMessage compact: %v", err)
	}
	if got := readContentText(t, regDB, msg.ID); got != "[Compact] a compact summary..." {
		t.Fatalf("compact content_text = %q, want %q", got, "[Compact] a compact summary...")
	}

	segDB := openNarratorHandlerTestDB(t)
	defer segDB.Close()
	segNar := insertNarratorHandlerTestRow(t, segDB)
	hSeg := NewHandler(&Service{DB: segDB})
	segMsg, err := hSeg.createCompactMessage(context.Background(), segNar, "seg summary", "segment_compact", nil)
	if err != nil {
		t.Fatalf("createCompactMessage segment: %v", err)
	}
	if got := readContentText(t, segDB, segMsg.ID); got != "[Segment Compact]\nseg summary" {
		t.Fatalf("segment content_text = %q, want %q", got, "[Segment Compact]\nseg summary")
	}

	cjkDB := openNarratorHandlerTestDB(t)
	defer cjkDB.Close()
	cjkNar := insertNarratorHandlerTestRow(t, cjkDB)
	hCJK := NewHandler(&Service{DB: cjkDB})
	cjkMsg, err := hCJK.createCompactMessage(context.Background(), cjkNar, strings.Repeat("章", 250), "compact", nil)
	if err != nil {
		t.Fatalf("createCompactMessage cjk: %v", err)
	}
	want := "[Compact] " + strings.Repeat("章", 200) + "..."
	if got := readContentText(t, cjkDB, cjkMsg.ID); got != want {
		t.Fatalf("compact cjk content_text mismatch:\n got %q\nwant %q", got, want)
	}
}
