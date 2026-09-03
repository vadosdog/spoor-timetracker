-- spoor schema. Readable on purpose: `sqlite3 spoor.db` and a SELECT must be
-- enough for a user to see everything the tool collected about them.
-- No blobs, no serialised structs, no opaque identifiers.

CREATE TABLE IF NOT EXISTS events (
    id             INTEGER PRIMARY KEY,

    -- Which plugin produced this. One source so far: 'claude-code'.
    source         TEXT    NOT NULL,
    -- The source's own identity for the event. For Claude Code it is the
    -- JSONL `uuid`. Together with source it is the dedup key: re-importing
    -- the same line can never create a second row.
    external_id    TEXT    NOT NULL,

    -- When it happened. RFC3339, UTC, millisecond precision, always ending
    -- in Z, so lexicographic order is chronological order.
    ts             TEXT    NOT NULL,
    -- How long it took, when the source actually reports it. NULL means
    -- "a point in time", which is the common case, not "zero".
    duration_ms    INTEGER,

    -- The source's own classification, kept verbatim.
    type           TEXT    NOT NULL,
    subtype        TEXT    NOT NULL DEFAULT '',

    -- Attribution guessed at ingest time; empty when unknown.
    project        TEXT    NOT NULL DEFAULT '',
    -- Short metadata label: model, tool names, attachment kind. Never the
    -- text of a conversation.
    raw_text       TEXT    NOT NULL DEFAULT '',

    -- Claude Code core fields. Present in 100% of core rows, all clients,
    -- all versions. Kept as columns rather than a JSON blob so the database
    -- stays readable.
    session_id     TEXT    NOT NULL DEFAULT '',
    cwd            TEXT    NOT NULL DEFAULT '',
    git_branch     TEXT    NOT NULL DEFAULT '',
    entrypoint     TEXT    NOT NULL DEFAULT '',
    is_sidechain   INTEGER NOT NULL DEFAULT 0,
    client_version TEXT    NOT NULL DEFAULT '',

    -- When spoor first saw it. The event outlives its source file; this is
    -- the only way to tell how long ago that import happened.
    ingested_at    TEXT    NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS events_identity ON events (source, external_id);
CREATE INDEX IF NOT EXISTS events_ts ON events (ts);
CREATE INDEX IF NOT EXISTS events_project ON events (project);

-- Import bookkeeping. Source files are read incrementally: JSONL is only ever
-- appended to, so a byte offset is enough to skip what was already imported.
--
-- Rows are never deleted when the file disappears. The whole point of the
-- accumulating database is that Claude Code erases its own history after
-- 30 days and the imported events must survive that.
CREATE TABLE IF NOT EXISTS source_files (
    source      TEXT    NOT NULL,
    path        TEXT    NOT NULL,
    size        INTEGER NOT NULL,
    mtime       TEXT    NOT NULL,
    read_offset INTEGER NOT NULL,
    -- Hash of the bytes immediately preceding read_offset. A path can end up
    -- holding different content — a restored backup, a copied session, a
    -- rollback followed by fresh writes — and then the offset points into
    -- bytes nobody has read. Appending never changes these bytes, so the hash
    -- is stable while the log grows and differs the moment it is not the same
    -- file any more.
    seam_hash   TEXT    NOT NULL DEFAULT '',
    seen_at     TEXT    NOT NULL,
    PRIMARY KEY (source, path)
);
