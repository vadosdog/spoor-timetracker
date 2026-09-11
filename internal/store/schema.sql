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
    -- trace — 'cli', 'claude-desktop' and the other clients for Claude Code,
    -- 'chrome' or 'firefox'
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

-- What a person said about a stretch of a day, where a rule could not say it.
--
-- Every other answer becomes a line of the dictionary and lives in the config
-- file, because a rule is what stops the same question being asked twice.
-- These are the ones no rule could carry: a block whose only traces are on the
-- never list, one encounter with a trace that names a different subject every
-- time, or a correction its author meant for this day and no other.
--
-- Times are RFC3339 in UTC, like events.ts, so that this table can be read
-- beside that one. Durations in these tables are milliseconds instead, because
-- a length is a number and an instant is a time.
--
-- An instant that describes a piece of the day carries milliseconds, as
-- events.ts does, so that it compares to a trace. The ones that record when
-- spoor was told something — made_at here, confirmed_at below — are to the
-- second: nothing compares those to anything.
CREATE TABLE IF NOT EXISTS assignment (
    day       TEXT    NOT NULL,        -- local date, YYYY-MM-DD
    from_ts   TEXT    NOT NULL,
    to_ts     TEXT    NOT NULL,
    -- Empty project means "leave the project as it is": an answer about the
    -- subject alone is the commonest kind here.
    project   TEXT    NOT NULL DEFAULT '',
    subject   TEXT    NOT NULL DEFAULT '',
    -- Empty subject already means "unchanged", so "none of them" needs a flag
    -- of its own.
    clear_subject INTEGER NOT NULL DEFAULT 0,
    -- 1 when no rule could have been written for this. Counted and printed:
    -- hand marking nobody can see is hand marking nobody will revisit.
    one_off   INTEGER NOT NULL DEFAULT 0,
    -- Why no rule could be written, because the point of counting these is
    -- that somebody reads them months later. Spoor's own words, not yours:
    -- one of a handful of fixed sentences saying which answer put the row
    -- here ("answered for this block only"). Nothing typed reaches this
    -- column.
    reason    TEXT    NOT NULL DEFAULT '',
    made_at   TEXT    NOT NULL,
    PRIMARY KEY (day, from_ts, to_ts)
);

-- Whether a pause between two blocks was work.
--
-- No **measured** number moves because a row is in it. A pause is invisible in
-- the traces — a cigarette and a meeting look identical on disk — so nothing
-- here was measured by anything, and the attention, background, active and
-- coverage figures are exactly what they would be if this table were empty.
--
-- What a row does buy is a line of its own: "pauses you called work" in the
-- report, in --json, in an export, and against the project it was attached to
-- in confirmed_row.claimed_ms below.
--
-- It exists to settle one question with data instead of memory: whether the
-- gaps between blocks are worth storing as records of their own, which can
-- only be found out by asking on the evening of the day while somebody still
-- knows. If the answer turns out to be no, this table goes away with the
-- screen that fills it. That is the deal it was built under.
CREATE TABLE IF NOT EXISTS window_answer (
    day           TEXT    NOT NULL,        -- local date, YYYY-MM-DD
    from_ts       TEXT    NOT NULL,
    to_ts         TEXT    NOT NULL,
    worked        INTEGER NOT NULL,        -- 1 | 0, and there is no default
    -- What it was, when it was work. A pause you can only call "work" is a
    -- pause you cannot do anything with afterwards: the question people
    -- actually ask is how long something took, and an hour attached to nothing
    -- answers it for nothing.
    project       TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    -- And what was being worked on either side, as it stood when the question was
    -- answered. Kept with the answer rather than looked up later: the rules
    -- move, and the point of the exercise is whether "the same project on both
    -- sides" predicted the answer *at the time*.
    left_project  TEXT    NOT NULL DEFAULT '',
    right_project TEXT    NOT NULL DEFAULT '',
    answered_at   TEXT    NOT NULL,
    PRIMARY KEY (day, from_ts, to_ts)
);

-- A confirmed day: the result frozen, not the events.
--
-- Rules run when a report is built, so one edit to the dictionary renames a
-- year of history — which is right for history nobody has looked at and wrong
-- for a day whose numbers have already been acted on. Confirming a day writes
-- the answer down; `report` reads it back instead of computing it again.
--
-- The settings are stored with it because they are part of the answer: a day
-- computed with a ten minute clustering threshold is a different day from the
-- same events at twenty, and in six months nobody remembers which was in force.
CREATE TABLE IF NOT EXISTS confirmed_day (
    day                 TEXT PRIMARY KEY,
    confirmed_at        TEXT    NOT NULL,
    -- The dictionary this was computed against. When it stops matching the
    -- config, the report says the rules have moved rather than quietly
    -- disagreeing with them for ever.
    config_hash         TEXT    NOT NULL,
    spoor_version       TEXT    NOT NULL,
    cluster_gap_ms      INTEGER NOT NULL,
    attention_window_ms INTEGER NOT NULL,
    head_ms             INTEGER NOT NULL,
    tail_ms             INTEGER NOT NULL,
    count_background    INTEGER NOT NULL,
    -- How much of the day rests on the weakest rule there is: a block with no
    -- name of its own and a named block on one side only. The report counts it
    -- out loud, so a confirmed day has to carry it or the line disappears the
    -- moment a day is frozen.
    one_neighbour_ms    INTEGER NOT NULL DEFAULT 0,
    -- Time between blocks that the person said was work. Never part of the
    -- day's active time: nothing measured anything there, and one number made
    -- of a measurement and an answer is a number nobody can check. Carried so
    -- that a frozen day keeps the line a live one prints.
    claimed_ms          INTEGER NOT NULL DEFAULT 0,
    questions_total     INTEGER NOT NULL,
    questions_answered  INTEGER NOT NULL,
    one_off_count       INTEGER NOT NULL
);

-- The partition of a confirmed day's active time: the timeline, and the only
-- thing it is safe to read one as.
--
-- ground is not kept for the program. It is what lets somebody ask, months
-- later, what this half hour rests on — a rule they wrote, a guess from a
-- directory name, the block next door, or their own hand.
CREATE TABLE IF NOT EXISTS confirmed_stretch (
    day      TEXT    NOT NULL,
    from_ts  TEXT    NOT NULL,
    to_ts    TEXT    NOT NULL,
    project  TEXT    NOT NULL DEFAULT '',   -- '' = no project
    subject  TEXT    NOT NULL DEFAULT '',   -- '' = the rest of the project
    kind     TEXT    NOT NULL,              -- attention | background | padding
    ground   TEXT    NOT NULL,              -- rule | fallback | inherited |
                                            -- neighbours | one-neighbour |
                                            -- manual | one-off | '' (none)
    PRIMARY KEY (day, from_ts)
);

-- The rows of a confirmed day.
--
-- agent and wall are here rather than derived from the stretches because they
-- are not a partition of anything: two chat windows can be busy in the same
-- second, so both can exceed the length of a day, on purpose.
CREATE TABLE IF NOT EXISTS confirmed_row (
    day           TEXT    NOT NULL,
    project       TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    work          INTEGER,                  -- 1 | 0 | NULL = not said
    -- How many traces carry this row. The evidence *behind* the count — which
    -- hosts, which directories — is deliberately not copied here: it is in the
    -- events table, where it always was, and a snapshot of the answer is not a
    -- snapshot of everything that led to it. `report --recompute` shows it.
    events        INTEGER NOT NULL DEFAULT 0,
    attention_ms  INTEGER NOT NULL,
    -- Time between blocks the person said belonged to this row, from the
    -- window_answer table above. Never part of attention or background:
    -- nothing measured it, and one number made of a measurement and somebody's
    -- recollection is a number nobody can check. Its own column for the same
    -- reason it is its own line in the report.
    claimed_ms    INTEGER NOT NULL DEFAULT 0,
    background_ms INTEGER NOT NULL,
    padding_ms    INTEGER NOT NULL,
    agent_ms      INTEGER NOT NULL,
    wall_ms       INTEGER NOT NULL,
    PRIMARY KEY (day, project, subject)
);

-- The assignments in a confirmed day that could not become rules, copied here
-- when the day was frozen.
--
-- A copy rather than a reference, because a snapshot that reads through to a
-- live table is not a snapshot. And a table of its own rather than a column,
-- because the number of them is worth printing: a row resting on a rule and a
-- row resting on somebody's hand look identical a month later.
CREATE TABLE IF NOT EXISTS confirmed_one_off (
    day      TEXT    NOT NULL,
    from_ts  TEXT    NOT NULL,
    to_ts    TEXT    NOT NULL,
    project  TEXT    NOT NULL DEFAULT '',
    subject  TEXT    NOT NULL DEFAULT '',
    reason   TEXT    NOT NULL,              -- why no rule could have been written
    -- Keyed like the assignment table it is copied from. Two answers can
    -- legitimately start at the same minute and end at different ones —
    -- somebody widening a correction — and a narrower key here would make the
    -- day impossible to confirm at all, with a bare UNIQUE error and a row to
    -- find by hand.
    PRIMARY KEY (day, from_ts, to_ts)
);

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
