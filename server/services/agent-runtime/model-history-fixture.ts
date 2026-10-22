/** Small isolated schema for SQLite/PG contract tests; never used by the application. */
export const modelHistoryFixtureSql = `
CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT);
CREATE TABLE narrators (id TEXT PRIMARY KEY, title TEXT, context_summary TEXT, model_history_revision INTEGER NOT NULL DEFAULT 0, message_version INTEGER DEFAULT 0);
CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, sdk_message_uuid TEXT, parent_tool_use_id TEXT, role TEXT, content_json TEXT, content_text TEXT, created_by TEXT, origin TEXT, origin_label TEXT, body_revision INTEGER NOT NULL DEFAULT 0, tokens_in INTEGER, body_format INTEGER NOT NULL DEFAULT 1, body_bytes INTEGER NOT NULL DEFAULT 0, state_revision INTEGER NOT NULL DEFAULT 0, author_narrator_id TEXT);
CREATE TABLE narrator_blocks (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, ordinal INTEGER NOT NULL, kind TEXT NOT NULL, subtype TEXT, phase TEXT NOT NULL DEFAULT 'done', revision INTEGER NOT NULL DEFAULT 0, model_text TEXT, payload TEXT NOT NULL, state TEXT, provider_meta TEXT);
CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0, segment_compact_id TEXT);
CREATE INDEX idx_fixture_refs_seq ON narrator_message_refs(narrator_id, seq);
CREATE INDEX idx_fixture_refs_message ON narrator_message_refs(message_id);
CREATE INDEX idx_fixture_refs_compact ON narrator_message_refs(narrator_id, is_compact, seq);
CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, tool_name TEXT, input_json TEXT, output_json TEXT, status TEXT, execution_attempt INTEGER DEFAULT 0, created_at TEXT, model_revision INTEGER NOT NULL DEFAULT 0, input_tokens INTEGER DEFAULT 0, block_id TEXT, is_internal BOOLEAN NOT NULL DEFAULT FALSE);
CREATE INDEX idx_fixture_tool_keyset ON narrator_tool_calls(message_id, id);
CREATE TABLE narrator_block_sync_queue (message_id TEXT PRIMARY KEY);
CREATE TABLE model_history_revision_contracts (id TEXT PRIMARY KEY, installed_version TEXT NOT NULL);
`;
