-- spoor schema. Readable on purpose: `sqlite3 spoor.db` and a SELECT must be
-- enough for a user to see everything the tool collected about them.
-- No blobs, no serialised structs, no opaque identifiers.

CREATE TABLE IF NOT EXISTS events (
    id             INTEGER PRIMARY KEY,

    -- Which plugin produced this: 'claude-code', 'browser' or 'calendar'.
    source         TEXT    NOT NULL,
    -- The source's own identity for the event. For Claude Code it is the
    -- JSONL `uuid`; for the browser it is
    -- <flavour>/<profile>/<visit time>/<visit id>; for the calendar it is
    -- <calendar>/<UID>/<occurrence>. Together with source it is the dedup key:
    -- re-importing the same line can never create a second row.
    --
    -- The calendar id is in there because an iCalendar UID is unique within
    -- one calendar and not across two: an invitation keeps the organiser's UID
    -- in every attendee's copy, so the same meeting subscribed to twice would
    -- otherwise collide on this index and the second one would be dropped with
    -- no error and no warning.
    external_id    TEXT    NOT NULL,

    -- When it happened. RFC3339, UTC, millisecond precision, always ending
    -- in Z, so lexicographic order is chronological order.
    ts             TEXT    NOT NULL,
    -- How long it took, when the source actually reports it. NULL means
    -- "a point in time", which is the common case, not "zero".
    --
    -- Two sources fill it and the report reads only one of them. A meeting's
    -- length is the sole evidence that hour existed, because nothing is
    -- written to disk during a call; Claude Code's system/turn_duration
    -- describes a turn that already has events at both of its ends, so
    -- counting it as a span would count the same seconds twice.
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
    -- stays readable. Empty on rows from any other source, with one
    -- exception: entrypoint names which instance of a source produced the
    -- trace — 'cli' or 'claude-desktop' for Claude Code, 'chrome' or 'firefox'
    -- for the browser, and the calendar's own id for a meeting.
    session_id     TEXT    NOT NULL DEFAULT '',
    cwd            TEXT    NOT NULL DEFAULT '',
    git_branch     TEXT    NOT NULL DEFAULT '',
    entrypoint     TEXT    NOT NULL DEFAULT '',
    is_sidechain   INTEGER NOT NULL DEFAULT 0,
    client_version TEXT    NOT NULL DEFAULT '',

    -- Browser fields: where a visit went, never what was on the page. The
    -- full URL is deliberately absent — a query string carries session
    -- tokens and search terms, and this project does not store either.
    --
    -- The bare domain does not separate projects: one host serves several of
    -- them, and a dev server is only told apart by its port. So host, port
    -- and the first path segment are kept, and nothing below that.
    host           TEXT    NOT NULL DEFAULT '',  -- 'gitlab.example.com'
    -- '3000'. Empty when the URL carried no port, or carried the scheme's own
    -- default: https://x:443/ and https://x/ are the same place, and storing
    -- them differently would split one host into two.
    port           TEXT    NOT NULL DEFAULT '',
    -- The first path segment and nothing below it, capped at 64 characters.
    -- A %2F counts as the separator it decodes to. What may stay in the
    -- segment is derived from RFC 3986 §3.3 — letters, digits, marks,
    -- "-._~!$'()*+:@" and a space — less ';', '=' and ',', which the section
    -- names itself as parameter delimiters, and less '&'. Anything else ends it,
    -- named or not: a query string, a jsessionid, a UNC path, a private-use
    -- character. An allow-list, because a list of delimiters to watch out
    -- for is only ever as good as the last one somebody thought of.
    path_head      TEXT    NOT NULL DEFAULT '',
    -- The page title as the browser recorded it, capped at 4096 characters,
    -- with anything that could make it display as something else — controls,
    -- line separators, direction overrides — replaced by a space. Metadata by
    -- the rules of this project, and the one browser column that can still be
    -- revealing: the title of a search result page is the search query.
    -- The calendar fills this one too, with the meeting's summary. Same
    -- column, same repair, same warning: it is chosen by whoever sent the
    -- invitation, and it is the only field of a meeting that is stored. The
    -- attendees are read in one place, to answer whether you declined, and
    -- never written. The description, the location, the organiser and the
    -- conference link are not read at all.
    title          TEXT    NOT NULL DEFAULT '',
    -- Every column carrying bytes somebody else wrote — host, path_head,
    -- title and external_id above for the browser, and title, external_id and
    -- entrypoint for the calendar — holds what came out of another program's
    -- database, off a directory name, or off the network, so anything in
    -- them that is not valid UTF-8, or that would make the value display as
    -- something it is not, is replaced. A path older than UTF-8 is encoded
    -- in the page's own charset, and a Latin-1 '/caf%E9/' therefore reads
    -- as 'caf�' here: a character the page never contained, standing in for
    -- a byte SQLite would otherwise refuse to hand back at all — which would
    -- fail the whole query, not just that value.

    -- When spoor first saw it. The event outlives its source file; this is
    -- the only way to tell how long ago that import happened.
    ingested_at    TEXT    NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS events_identity ON events (source, external_id);
CREATE INDEX IF NOT EXISTS events_ts ON events (ts);
CREATE INDEX IF NOT EXISTS events_project ON events (project);
-- The index on host is not here: this file runs before the columns added
-- after the first release exist. See addedIndexes in store.go.

-- Import bookkeeping. Source files are read incrementally, but not all of
-- them the same way, because not all of them are appended to:
--
--   * a JSONL session log only ever grows, so a byte offset (read_offset,
--     guarded by seam_hash) is enough to skip what was already imported;
--   * a browser history is a database, rewritten in place, and it also
--     forgets: Chrome keeps 90 days. There is no offset to keep, so the
--     watermark is the time of the newest visit already imported (cursor).
--
-- A source uses one or the other. The columns it does not use stay at their
-- defaults rather than pretending to mean something.
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
    -- How far a database-shaped source has read: the timestamp of the newest
    -- record it imported from this file, RFC3339 in UTC with microseconds.
    -- Kept as a readable time rather than in whatever epoch the browser
    -- counts in, so that the row explains itself.
    cursor      TEXT    NOT NULL DEFAULT '',
    seen_at     TEXT    NOT NULL,
    PRIMARY KEY (source, path)
);
