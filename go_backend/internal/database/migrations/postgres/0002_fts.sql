-- 0002_fts.sql (PostgreSQL)
-- Full-text search using tsvector + GIN indexes.
-- Unlike SQLite FTS5 (separate virtual tables + triggers), PostgreSQL
-- adds a search_vector column directly to each searchable table.
--
-- NOTE: NarraFork uses the 'simple' text search configuration for
-- language-neutral matching (works for CJK via the Go application layer
-- passing pre-tokenized input, or can be upgraded to a custom config
-- with pg_jieba/zhparser for proper Chinese segmentation later).
-- The Go PostgresStore.IndexXxx methods populate these columns explicitly.

-- === chapters ===
ALTER TABLE chapters ADD COLUMN IF NOT EXISTS search_vector tsvector;
CREATE INDEX IF NOT EXISTS idx_chapters_search_vector
	ON chapters USING gin(search_vector);

-- Auto-maintain search_vector via trigger (mirrors SQLite FTS5 triggers)
CREATE OR REPLACE FUNCTION chapters_search_update() RETURNS trigger AS $$
BEGIN
	NEW.search_vector := to_tsvector('simple',
		COALESCE(NEW.title, '') || ' ' || COALESCE(NEW.description, ''));
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS chapters_search_trigger ON chapters;
CREATE TRIGGER chapters_search_trigger
	BEFORE INSERT OR UPDATE OF title, description ON chapters
	FOR EACH ROW EXECUTE FUNCTION chapters_search_update();

-- === narrators ===
ALTER TABLE narrators ADD COLUMN IF NOT EXISTS search_vector tsvector;
CREATE INDEX IF NOT EXISTS idx_narrators_search_vector
	ON narrators USING gin(search_vector);

CREATE OR REPLACE FUNCTION narrators_search_update() RETURNS trigger AS $$
BEGIN
	NEW.search_vector := to_tsvector('simple', COALESCE(NEW.title, ''));
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS narrators_search_trigger ON narrators;
CREATE TRIGGER narrators_search_trigger
	BEFORE INSERT OR UPDATE OF title ON narrators
	FOR EACH ROW EXECUTE FUNCTION narrators_search_update();

-- === narrator_messages ===
ALTER TABLE narrator_messages ADD COLUMN IF NOT EXISTS search_vector tsvector;
CREATE INDEX IF NOT EXISTS idx_narrator_messages_search_vector
	ON narrator_messages USING gin(search_vector);

CREATE OR REPLACE FUNCTION narrator_messages_search_update() RETURNS trigger AS $$
BEGIN
	NEW.search_vector := to_tsvector('simple', COALESCE(NEW.content_text, ''));
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS narrator_messages_search_trigger ON narrator_messages;
CREATE TRIGGER narrator_messages_search_trigger
	BEFORE INSERT OR UPDATE OF content_text ON narrator_messages
	FOR EACH ROW EXECUTE FUNCTION narrator_messages_search_update();
