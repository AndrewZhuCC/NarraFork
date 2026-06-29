-- 0002_fts.sql
-- FTS5 virtual tables and sync triggers for full-text search.
-- Uses trigram tokenizer for CJK support.

-- === Virtual Tables ===
CREATE VIRTUAL TABLE IF NOT EXISTS chapters_fts USING fts5(
    title, description, content='chapters', content_rowid=rowid, tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS narrator_messages_fts USING fts5(
    content_text, content='narrator_messages', content_rowid=rowid, tokenize='trigram'
);

CREATE VIRTUAL TABLE IF NOT EXISTS narrators_fts USING fts5(
    title, content='narrators', content_rowid=rowid, tokenize='trigram'
);

-- === Sync triggers: chapters ===
CREATE TRIGGER IF NOT EXISTS chapters_fts_insert AFTER INSERT ON chapters BEGIN
    INSERT INTO chapters_fts(rowid, title, description)
    VALUES (NEW.rowid, NEW.title, NEW.description);
END;

CREATE TRIGGER IF NOT EXISTS chapters_fts_update AFTER UPDATE ON chapters BEGIN
    INSERT INTO chapters_fts(chapters_fts, rowid, title, description)
    VALUES ('delete', OLD.rowid, OLD.title, OLD.description);
    INSERT INTO chapters_fts(rowid, title, description)
    VALUES (NEW.rowid, NEW.title, NEW.description);
END;

CREATE TRIGGER IF NOT EXISTS chapters_fts_delete AFTER DELETE ON chapters BEGIN
    INSERT INTO chapters_fts(chapters_fts, rowid, title, description)
    VALUES ('delete', OLD.rowid, OLD.title, OLD.description);
END;

-- === Sync triggers: narrator_messages ===
CREATE TRIGGER IF NOT EXISTS narrator_messages_fts_insert AFTER INSERT ON narrator_messages BEGIN
    INSERT INTO narrator_messages_fts(rowid, content_text)
    VALUES (NEW.rowid, NEW.content_text);
END;

CREATE TRIGGER IF NOT EXISTS narrator_messages_fts_update AFTER UPDATE ON narrator_messages BEGIN
    INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
    VALUES ('delete', OLD.rowid, OLD.content_text);
    INSERT INTO narrator_messages_fts(rowid, content_text)
    VALUES (NEW.rowid, NEW.content_text);
END;

CREATE TRIGGER IF NOT EXISTS narrator_messages_fts_delete AFTER DELETE ON narrator_messages BEGIN
    INSERT INTO narrator_messages_fts(narrator_messages_fts, rowid, content_text)
    VALUES ('delete', OLD.rowid, OLD.content_text);
END;

-- === Sync triggers: narrators ===
CREATE TRIGGER IF NOT EXISTS narrators_fts_insert AFTER INSERT ON narrators
WHEN NEW.title IS NOT NULL BEGIN
    INSERT INTO narrators_fts(rowid, title) VALUES (NEW.rowid, NEW.title);
END;

-- narrators_fts_update uses DROP+CREATE for complex WHEN clause
DROP TRIGGER IF EXISTS narrators_fts_update;
CREATE TRIGGER narrators_fts_update AFTER UPDATE OF title ON narrators
WHEN OLD.title IS NOT NULL OR NEW.title IS NOT NULL BEGIN
    INSERT INTO narrators_fts(narrators_fts, rowid, title)
    SELECT 'delete', OLD.rowid, OLD.title WHERE OLD.title IS NOT NULL;
    INSERT OR IGNORE INTO narrators_fts(rowid, title)
    SELECT NEW.rowid, NEW.title WHERE NEW.title IS NOT NULL;
END;

CREATE TRIGGER IF NOT EXISTS narrators_fts_delete AFTER DELETE ON narrators
WHEN OLD.title IS NOT NULL BEGIN
    INSERT INTO narrators_fts(narrators_fts, rowid, title)
    VALUES ('delete', OLD.rowid, OLD.title);
END;
