package narrators

import (
	"container/list"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"narrafork-go/internal/httpx"
)

// CHUNK_SIZE — number of top-level messages per chunk. Must stay in sync with
// the frontend and the TS server (server/services/narrator-messages.ts).
const chunkSize = 20

// manifestCacheLimit bounds the per-narrator manifest LRU. Mirrors the TS
// MANIFEST_CACHE_LIMIT.
const manifestCacheLimit = 64

// computedManifest is a cached manifest payload, validated by messageVersion.
// Because the manifest is a pure function of the ref table at a given
// messageVersion, a cached entry whose version still matches is exact — so even
// a first load (no client `since`) of a recently-viewed narrator skips the full
// ref scan.
type computedManifest struct {
	messageVersion int
	total          int
	chunks         []ManifestChunkTuple
}

// manifestLRUCache is a bounded, O(1) LRU keyed by narratorId. It is NOT safe
// for concurrent use on its own; the owning Service serializes access with
// manifestCacheMu (the entries it stores are treated as immutable once cached).
type manifestLRUCache struct {
	ll      *list.List               // front = most-recently-used
	entries map[string]*list.Element // narratorId -> element
}

type manifestCacheEntry struct {
	narratorID string
	manifest   computedManifest
}

func newManifestLRUCache() *manifestLRUCache {
	return &manifestLRUCache{ll: list.New(), entries: make(map[string]*list.Element)}
}

// get returns the cached manifest when present AND its messageVersion still
// matches the requested version, touching it as most-recently-used. A version
// mismatch is a miss (the entry is stale and will be overwritten on the next
// set).
func (c *manifestLRUCache) get(narratorID string, version int) (computedManifest, bool) {
	el, ok := c.entries[narratorID]
	if !ok {
		return computedManifest{}, false
	}
	entry := el.Value.(*manifestCacheEntry)
	if entry.manifest.messageVersion != version {
		return computedManifest{}, false
	}
	c.ll.MoveToFront(el)
	return entry.manifest, true
}

// set inserts/overwrites the manifest for a narrator and evicts the
// least-recently-used entries until the cache is within manifestCacheLimit.
func (c *manifestLRUCache) set(narratorID string, manifest computedManifest) {
	if el, ok := c.entries[narratorID]; ok {
		el.Value.(*manifestCacheEntry).manifest = manifest
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&manifestCacheEntry{narratorID: narratorID, manifest: manifest})
	c.entries[narratorID] = el
	for c.ll.Len() > manifestCacheLimit {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.entries, oldest.Value.(*manifestCacheEntry).narratorID)
	}
}

// getCachedManifest / setCachedManifest are the Service-level, mutex-guarded
// entry points to the lazily-initialized manifest LRU.
func (s *Service) getCachedManifest(narratorID string, version int) (computedManifest, bool) {
	s.manifestCacheMu.Lock()
	defer s.manifestCacheMu.Unlock()
	if s.manifestCache == nil {
		return computedManifest{}, false
	}
	return s.manifestCache.get(narratorID, version)
}

func (s *Service) setCachedManifest(narratorID string, manifest computedManifest) {
	s.manifestCacheMu.Lock()
	defer s.manifestCacheMu.Unlock()
	if s.manifestCache == nil {
		s.manifestCache = newManifestLRUCache()
	}
	s.manifestCache.set(narratorID, manifest)
}

// ManifestChunkTuple is the compact wire form of a manifest chunk:
// [id, firstSeq, lastSeq, count]. Tuples avoid repeating object keys across
// ~1.5k chunks (~60% smaller wire/parse than an array of objects), matching the
// TS getChunkManifest payload.
type ManifestChunkTuple struct {
	ID       string
	FirstSeq int
	LastSeq  int
	Count    int
}

// MarshalJSON renders the tuple as a JSON array [id, firstSeq, lastSeq, count]
// so the wire shape matches the TS `Array<[string, number, number, number]>`.
func (t ManifestChunkTuple) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{t.ID, t.FirstSeq, t.LastSeq, t.Count})
}

// ChunkRange is the result of ChunksByRange — a contiguous window of top-level
// messages (with full child trees) plus exact boundary flags. Mirrors the TS
// getChunksByRange return shape.
type ChunkRange struct {
	Messages       []Message `json:"messages"`
	MinSeq         *int      `json:"minSeq"`
	MaxSeq         *int      `json:"maxSeq"`
	HasOlder       bool      `json:"hasOlder"`
	HasNewer       bool      `json:"hasNewer"`
	MessageVersion int       `json:"messageVersion"`
}

// MessageLocation resolves a message id to the top-level seq coordinate used by
// the virtualized chunk list. Mirrors the TS getMessageLocation return shape.
type MessageLocation struct {
	MessageID         string `json:"messageId"`
	TopLevelMessageID string `json:"topLevelMessageId"`
	Seq               int    `json:"seq"`
}

// narrowSeqRef is a minimal {messageId, seq} row read directly from
// narrator_message_refs without loading any large content columns.
type narrowSeqRef struct {
	MessageID string
	Seq       int
}

// manifestWindow mirrors the TS getChunkManifest `window` argument. limitChunks
// caps the number of (newest) chunks returned; beforeSeq requests the band of
// chunks immediately older than that seq (reverse infinite scroll). A nil
// pointer field means "unset" (matching the TS `undefined`).
type manifestWindow struct {
	limitChunks *int
	beforeSeq   *int
}

// ChunkManifest builds the lightweight chunk manifest for the virtualized
// message list. It reads ONLY the narrow narrator_message_refs join (indexed by
// seq), never the large content_json/output_json payloads, and returns one
// tuple per chunkSize top-level messages so the client can detect which chunks
// changed without refetching content.
//
// When `since` matches the current messageVersion it short-circuits with
// {unchanged: true}.
//
// Windowing: the client opens with only the newest `limitChunks` chunks and
// walks older bands via `beforeSeq` (reverse infinite scroll). The full chunk
// array is still computed/cached server-side (cheap), but only the requested
// window is sent on the wire. `total` is always the full top-level count so
// callers know the true history size; `windowFirstIndex`/`hasOlderChunks`
// locate the window inside the full history. This mirrors the TS
// getChunkManifest window contract (server/services/narrator-messages.ts).
//
// A per-narrator LRU (keyed by narratorId, validated by messageVersion) caches
// the computed FULL manifest. Because the manifest is a pure function of the ref
// table at a given messageVersion — and messageVersion is bumped on every
// structural write — a cached entry whose version still matches is exact, so
// even a first load (no client `since`) of a recently-viewed narrator skips the
// full ref scan. The cache lives on the Service instance, not a package global,
// for test isolation.
func (s *Service) ChunkManifest(ctx context.Context, narratorID string, since *int, window *manifestWindow) (map[string]any, error) {
	messageVersion, err := s.NarratorMessageVersion(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	if since != nil && *since == messageVersion {
		return map[string]any{"unchanged": true, "messageVersion": messageVersion}, nil
	}

	full, err := s.computeFullManifest(ctx, narratorID, messageVersion)
	if err != nil {
		return nil, err
	}
	allChunks := full.chunks

	// Resolve the window [windowFirstIndex, windowEnd) over the full chunk array.
	// `beforeSeq` walks older: take the chunks whose firstSeq < beforeSeq, keeping
	// the newest `limit` of them. No `beforeSeq` -> newest `limit`. Mirrors the TS
	// windowing math exactly (clamp limit to [1,200], trunc).
	limit := len(allChunks)
	if window != nil && window.limitChunks != nil {
		limit = *window.limitChunks
		if limit < 1 {
			limit = 1
		}
		if limit > 200 {
			limit = 200
		}
	}
	windowEnd := len(allChunks) // exclusive
	if window != nil && window.beforeSeq != nil {
		// First chunk index whose firstSeq >= beforeSeq; everything before it is older.
		idx := len(allChunks)
		for i := range allChunks {
			if allChunks[i].FirstSeq >= *window.beforeSeq {
				idx = i
				break
			}
		}
		windowEnd = idx
	}
	windowFirstIndex := windowEnd - limit
	if windowFirstIndex < 0 {
		windowFirstIndex = 0
	}
	chunks := jsSliceRange(allChunks, windowFirstIndex, windowEnd)

	return map[string]any{
		"unchanged":        false,
		"messageVersion":   messageVersion,
		"total":            full.total,
		"windowFirstIndex": windowFirstIndex,
		"hasOlderChunks":   windowFirstIndex > 0,
		"chunks":           chunks,
	}, nil
}

// computeFullManifest computes (or reads from cache) the full manifest for a
// narrator at the given messageVersion. The full chunk array is the cheap part
// (one indexed ref scan + slicing) and stays cached so windowed reads never
// rescan. Mirrors the TS computeFullManifest helper.
func (s *Service) computeFullManifest(ctx context.Context, narratorID string, messageVersion int) (computedManifest, error) {
	if cached, ok := s.getCachedManifest(narratorID, messageVersion); ok {
		return cached, nil
	}

	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return computedManifest{}, err
	}

	// Narrow query: seq + message_id only, ordered by seq. No large columns, no
	// COUNT(*). Primary narrators never have refs pointing to child
	// (parent_tool_use_id IS NOT NULL) messages — children are not referenced in
	// the junction table — so the join to narrator_messages would filter nothing
	// and is pure overhead. Skip it for primaries; subagent narrators legitimately
	// hold child refs as top-level, so they keep the (cheap, small) join path.
	var query string
	if isSubagent {
		query = `SELECT r.message_id, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.segment_compact_id IS NULL ORDER BY r.seq`
	} else {
		query = `SELECT message_id, seq FROM narrator_message_refs WHERE narrator_id = ? AND segment_compact_id IS NULL ORDER BY seq`
	}
	rows, err := s.DB.QueryContext(ctx, query, narratorID)
	if err != nil {
		return computedManifest{}, err
	}
	defer rows.Close()
	refs := []narrowSeqRef{}
	for rows.Next() {
		var ref narrowSeqRef
		if err := rows.Scan(&ref.MessageID, &ref.Seq); err != nil {
			return computedManifest{}, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return computedManifest{}, err
	}

	// Compact tuple payload: [id, firstSeq, lastSeq, count]. The structural
	// fingerprint (hash) the TS code once carried is intentionally omitted — it is
	// only needed by an incremental reconciliation path that has no consumer yet.
	chunks := []ManifestChunkTuple{}
	for i := 0; i < len(refs); i += chunkSize {
		end := i + chunkSize
		if end > len(refs) {
			end = len(refs)
		}
		slice := refs[i:end]
		first := slice[0]
		last := slice[len(slice)-1]
		chunks = append(chunks, ManifestChunkTuple{
			ID:       first.MessageID,
			FirstSeq: first.Seq,
			LastSeq:  last.Seq,
			Count:    len(slice),
		})
	}

	entry := computedManifest{
		messageVersion: messageVersion,
		total:          len(refs),
		chunks:         chunks,
	}
	s.setCachedManifest(narratorID, entry)
	return entry, nil
}

// ChunksByRange fetches a contiguous range of top-level messages (with full
// child trees) for the virtualized list. Replaces the legacy cursor/around
// triple-path.
//
//   - direction "older": messages with seq < fromSeq (descending then reversed)
//   - direction "newer": messages with seq > fromSeq (ascending)
//   - fromSeq nil: the latest tail
//
// `count` is expressed in chunks; the row limit is count*chunkSize. Uses
// LIMIT n+1 for the queried direction and a separate indexed LIMIT 1 existence
// probe for the opposite edge, so hasOlder/hasNewer are exact without a COUNT(*).
//
// `count` is a pointer so a nil (omitted) value defaults to 7 chunks, exactly
// matching the TS `opts.count ?? 7` before the [1,20] clamp. A present 0/negative
// value still clamps up to 1 rather than the 7 default.
func (s *Service) ChunksByRange(ctx context.Context, narratorID string, fromSeq *int, direction string, count *int) (ChunkRange, error) {
	if direction != "newer" {
		direction = "older"
	}
	// Math.min(Math.max(count ?? 7, 1), 20).
	chunkCount := 7
	if count != nil {
		chunkCount = *count
	}
	if chunkCount < 1 {
		chunkCount = 1
	}
	if chunkCount > 20 {
		chunkCount = 20
	}
	rowLimit := chunkCount * chunkSize

	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return ChunkRange{}, err
	}

	// Base predicate shared by the window query and the opposite-edge existence
	// probe. Excludes the seq cursor so it can be reused for either side.
	baseConditions := []string{"r.narrator_id = ?", "r.segment_compact_id IS NULL"}
	if !isSubagent {
		baseConditions = append(baseConditions, "m.parent_tool_use_id IS NULL")
	}

	conditions := append([]string{}, baseConditions...)
	args := []any{narratorID}
	if fromSeq != nil {
		if direction == "newer" {
			conditions = append(conditions, "r.seq > ?")
		} else {
			conditions = append(conditions, "r.seq < ?")
		}
		args = append(args, *fromSeq)
	}
	order := "r.seq DESC"
	if direction == "newer" {
		order = "r.seq ASC"
	}
	args = append(args, rowLimit+1)
	refs, err := s.queryMessageRefs(ctx, strings.Join(conditions, " AND "), order, args...)
	if err != nil {
		return ChunkRange{}, err
	}

	hasMoreInDirection := len(refs) > rowLimit
	if hasMoreInDirection {
		refs = jsSliceStart0(refs, rowLimit)
	}
	if direction == "older" {
		reverseMessageRefs(refs)
	}

	messageVersion, err := s.NarratorMessageVersion(ctx, narratorID)
	if err != nil {
		return ChunkRange{}, err
	}

	if len(refs) == 0 {
		return ChunkRange{
			Messages:       []Message{},
			MinSeq:         nil,
			MaxSeq:         nil,
			HasOlder:       false,
			HasNewer:       false,
			MessageVersion: messageVersion,
		}, nil
	}

	messages, err := s.loadMessagesForRefs(ctx, narratorID, refs)
	if err != nil {
		return ChunkRange{}, err
	}
	minSeq := refs[0].Seq
	maxSeq := refs[len(refs)-1].Seq

	// hasOlder/hasNewer relative to the returned window. For the direction we
	// queried, hasMoreInDirection answers it directly. For the opposite side,
	// probe for the existence of a single row beyond the window edge.
	hasOlder := hasMoreInDirection
	hasNewer := hasMoreInDirection
	if direction == "older" {
		hasOlder = hasMoreInDirection
		exists, err := s.existsBeyondSeq(ctx, baseConditions, narratorID, "newer", maxSeq)
		if err != nil {
			return ChunkRange{}, err
		}
		hasNewer = exists
	} else {
		hasNewer = hasMoreInDirection
		exists, err := s.existsBeyondSeq(ctx, baseConditions, narratorID, "older", minSeq)
		if err != nil {
			return ChunkRange{}, err
		}
		hasOlder = exists
	}

	min := minSeq
	max := maxSeq
	return ChunkRange{
		Messages:       messages,
		MinSeq:         &min,
		MaxSeq:         &max,
		HasOlder:       hasOlder,
		HasNewer:       hasNewer,
		MessageVersion: messageVersion,
	}, nil
}

// existsBeyondSeq is the indexed LIMIT 1 probe used to compute the boundary flag
// for the edge ChunksByRange did not query directly.
func (s *Service) existsBeyondSeq(ctx context.Context, baseConditions []string, narratorID, op string, edgeSeq int) (bool, error) {
	conditions := append([]string{}, baseConditions...)
	if op == "newer" {
		conditions = append(conditions, "r.seq > ?")
	} else {
		conditions = append(conditions, "r.seq < ?")
	}
	query := `SELECT r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE ` + strings.Join(conditions, " AND ") + ` LIMIT 1`
	var seq int
	err := s.DB.QueryRowContext(ctx, query, narratorID, edgeSeq).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MessageLocation resolves a message id to the top-level seq used by the chunk
// manifest. This is the lightweight replacement for the old around-window query:
// it only reads narrow indexed columns and never returns content/message trees.
//
// For primary narrators, child messages are resolved by walking parent tool-use
// ownership until a referenced top-level message is found. For a subagent
// narrator, its own refs are already top-level from that page's point of view,
// even when messages carry parentToolUseId pointing to the parent narrator.
func (s *Service) MessageLocation(ctx context.Context, narratorID, messageID string) (MessageLocation, error) {
	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return MessageLocation{}, err
	}

	var targetParent sql.NullString
	err = s.DB.QueryRowContext(ctx, `SELECT parent_tool_use_id FROM narrator_messages WHERE id = ?`, messageID).Scan(&targetParent)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageLocation{}, httpx.NotFound("Message")
	}
	if err != nil {
		return MessageLocation{}, err
	}

	directSeq, directOK, err := s.visibleRefSeq(ctx, narratorID, messageID)
	if err != nil {
		return MessageLocation{}, err
	}
	targetParentToolUse := strings.TrimSpace(sqlNullString(targetParent))
	if directOK && (isSubagent || targetParentToolUse == "") {
		return MessageLocation{MessageID: messageID, TopLevelMessageID: messageID, Seq: directSeq}, nil
	}

	parentToolUseID := targetParentToolUse
	for depth := 0; parentToolUseID != "" && depth < 32; depth++ {
		rows, err := s.DB.QueryContext(ctx, `
			SELECT tc.message_id, m.parent_tool_use_id, r.seq
			FROM narrator_tool_calls tc
			JOIN narrator_messages m ON m.id = tc.message_id
			JOIN narrator_message_refs r ON r.message_id = tc.message_id AND r.narrator_id = ? AND r.segment_compact_id IS NULL
			WHERE tc.tool_use_id = ?
			ORDER BY r.seq
			LIMIT 10`, narratorID, parentToolUseID)
		if err != nil {
			return MessageLocation{}, err
		}

		nextParentToolUseID := ""
		foundCandidate := false
		for rows.Next() {
			foundCandidate = true
			var parentMessageID string
			var candidateParent sql.NullString
			var parentSeq int
			if err := rows.Scan(&parentMessageID, &candidateParent, &parentSeq); err != nil {
				rows.Close()
				return MessageLocation{}, err
			}
			candidateParentToolUseID := strings.TrimSpace(sqlNullString(candidateParent))
			if candidateParentToolUseID != "" {
				nextParentToolUseID = candidateParentToolUseID
				continue
			}
			rows.Close()
			return MessageLocation{MessageID: messageID, TopLevelMessageID: parentMessageID, Seq: parentSeq}, nil
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return MessageLocation{}, err
		}
		rows.Close()
		if !foundCandidate {
			break
		}
		parentToolUseID = nextParentToolUseID
	}

	if directOK {
		return MessageLocation{MessageID: messageID, TopLevelMessageID: messageID, Seq: directSeq}, nil
	}
	return MessageLocation{}, httpx.NotFound("Message")
}

// visibleRefSeq returns the seq of a narrator's visible (non segment-compacted)
// ref for the given message id, mirroring the TS findVisibleRef helper.
func (s *Service) visibleRefSeq(ctx context.Context, narratorID, messageID string) (int, bool, error) {
	var seq int
	err := s.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ? AND segment_compact_id IS NULL LIMIT 1`, narratorID, messageID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}
