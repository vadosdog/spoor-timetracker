// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package store is the SQLite database spoor accumulates events in.
//
// The database accumulates rather than being rebuilt on demand: the main
// source of time, Claude Code's JSONL, deletes itself after 30 days, so an
// event that was imported once has to survive the removal of the file it
// came from.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go driver: no cgo, ever

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

//go:embed schema.sql
var schemaSQL string

// Store is an open spoor database.
type Store struct {
	db *sql.DB
	// now is injectable so tests do not depend on the wall clock.
	now func() time.Time
}

// FileState is what we remember about a source file between runs.
type FileState struct {
	Size       int64
	MTime      time.Time
	ReadOffset int64
	// SeamHash identifies the content the offset was measured against: the
	// bytes immediately before ReadOffset.
	SeamHash string
	// Cursor is the watermark of a source that reads a database rather than
	// an appended log: the time of the newest record already imported from
	// this file. Empty means "nothing imported yet".
	Cursor string
}

// Open opens (creating if needed) the database at path and applies the schema.
//
// The file holds a timeline of the user's days. It is created 0600 and its
// directory 0700, and SQLite inherits those permissions for the -wal and -shm
// files it makes alongside it.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if dir := filepath.Dir(abs); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}
	if err := createPrivate(abs); err != nil {
		return nil, err
	}

	// Built through url.URL rather than by concatenation: SQLite parses a
	// file: DSN as a URI, so a path containing ? # or % would otherwise open
	// the wrong file or smuggle in pragmas of its own.
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     abs,
		RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One writer at a time keeps WAL happy and ingest is single threaded anyway.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}

// addedColumns are columns that arrived after a table first shipped.
// CREATE TABLE IF NOT EXISTS does nothing to a table that already exists, so
// without this an older database keeps its old shape and every query naming a
// new column fails — quietly, one warning per file, importing nothing.
var addedColumns = []struct{ table, column, decl string }{
	{"source_files", "seam_hash", "TEXT NOT NULL DEFAULT ''"},
	{"source_files", "cursor", "TEXT NOT NULL DEFAULT ''"},
	{"events", "host", "TEXT NOT NULL DEFAULT ''"},
	{"events", "port", "TEXT NOT NULL DEFAULT ''"},
	{"events", "path_head", "TEXT NOT NULL DEFAULT ''"},
	{"events", "title", "TEXT NOT NULL DEFAULT ''"},
	// Added to the confirmed-day tables after they first existed. Every one of
	// these is declared in schema.sql as well — that file is what somebody
	// reads with the database open, and a column that exists only here has no
	// comment and no explanation. Both places, always.
	{"confirmed_day", "one_neighbour_ms", "INTEGER NOT NULL DEFAULT 0"},
	{"confirmed_day", "claimed_ms", "INTEGER NOT NULL DEFAULT 0"},
	{"confirmed_row", "events", "INTEGER NOT NULL DEFAULT 0"},
	{"confirmed_row", "claimed_ms", "INTEGER NOT NULL DEFAULT 0"},
	{"window_answer", "project", "TEXT NOT NULL DEFAULT ''"},
	{"window_answer", "subject", "TEXT NOT NULL DEFAULT ''"},
}

// addedIndexes are indexes that arrived after the table first shipped. Unlike
// columns, CREATE INDEX IF NOT EXISTS is enough on its own — but only once the
// column it names exists, so it runs after addedColumns rather than in
// schema.sql.
var addedIndexes = []string{
	`CREATE INDEX IF NOT EXISTS events_host ON events (host)`,
}

func migrate(db *sql.DB) error {
	for _, c := range addedColumns {
		has, err := hasColumn(db, c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.decl)
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s.%s: %w", c.table, c.column, err)
		}
	}
	for _, stmt := range addedIndexes {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	// PRAGMA takes no placeholders; both names are compile-time constants
	// from addedColumns, never anything a user supplied.
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			cid           int
			name, colType string
			notNull, pk   int
			defaultValue  any
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for read-only queries in tests.
func (s *Store) DB() *sql.DB { return s.db }

// InsertEvents writes events, ignoring any whose (source, external_id) is
// already there. It returns how many rows were actually new, which is what
// makes a second run over unchanged data a no-op.
func (s *Store) InsertEvents(events []event.Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	// Rolling back a committed transaction is a no-op; the error is expected.
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO events (
			source, external_id, ts, duration_ms, type, subtype,
			project, raw_text, session_id, cwd, git_branch,
			entrypoint, is_sidechain, client_version,
			host, port, path_head, title, ingested_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()

	ingestedAt := s.now().UTC().Format(time.RFC3339)
	inserted := 0
	for _, e := range events {
		var duration any
		if e.DurationMS != nil {
			duration = *e.DurationMS
		}
		res, err := stmt.Exec(
			e.Source, e.ExternalID, e.TS, duration, e.Type, e.Subtype,
			e.Project, e.RawText, e.SessionID, e.CWD, e.GitBranch,
			e.Entrypoint, e.IsSidechain, e.ClientVersion,
			e.Host, e.Port, e.PathHead, e.Title, ingestedAt,
		)
		if err != nil {
			return 0, fmt.Errorf("insert event %s: %w", e.ExternalID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		inserted += int(n)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return inserted, nil
}

// Window is one source instance's whole statement about one stretch of time.
type Window struct {
	// Source and Entrypoint name the instance whose rows may be touched.
	// Nothing another source, or another calendar, wrote is in scope.
	Source, Entrypoint string
	// From and To are the stretch the source was asked about, To exclusive.
	// Rows outside it are never touched: the source said nothing about them.
	From, To time.Time
	// Events is everything the source now says is in there.
	Events []event.Event
	// Whole says the source understood everything it was given, so what it
	// does not restate really is gone.
	//
	// False stops the deleting half and nothing else: what is stated is still
	// inserted and updated. It covers both shapes of "I did not understand" —
	// a feed that produced nothing at all, which is also what a login page and
	// a revoked address look like, and a feed that produced plenty while one
	// series of it went unread. The second is the likelier of the two and was
	// the one a first attempt at this guard did not reach, because it was only
	// consulted when the statement was empty.
	Whole bool
}

// WindowSync is what one call to ReplaceWindow did.
type WindowSync struct {
	// New is rows that were not there, Moved is rows whose time, length or
	// title changed, Removed is rows the source no longer states.
	New, Moved, Removed int
	// RefusedEmpty is set when the source did not understand the whole of what
	// it was given and the database holds rows the statement does not mention.
	// Nothing is deleted in that case and the caller is expected to say so out
	// loud.
	RefusedEmpty bool
}

// ReplaceWindow makes the database say about [from, to) exactly what the
// source now says, for one instance of one source.
//
// Every other import path here is append-only, and for a log that only grows
// that is right. A calendar is the other kind of source: it does not append,
// it restates. A meeting moved from 10:00 to 14:00 keeps its UID and therefore
// its identity, so an insert that ignores conflicts keeps the old hour; a
// series moved wholesale changes every occurrence key, so the new occurrences
// arrive, the old ones stay, and the day is counted twice. Both failures are
// silent and both are of the "quietly more than there was" kind.
//
// So: what the source states is inserted or updated, and what it no longer
// states is deleted — within the window it was asked about, and for that
// instance only. Rows outside the window are not touched, because the source
// said nothing about them.
//
// ingested_at survives an update. It is when spoor first saw the meeting, and
// rescheduling it does not make that a different fact.
func (s *Store) ReplaceWindow(w Window) (WindowSync, error) {
	var sync WindowSync

	tx, err := s.db.Begin()
	if err != nil {
		return sync, err
	}
	defer func() { _ = tx.Rollback() }()

	type stored struct {
		ts       string
		duration sql.NullInt64
		title    string
	}
	existing := map[string]stored{}
	rows, err := tx.Query(`
		SELECT external_id, ts, duration_ms, title FROM events
		WHERE source = ? AND entrypoint = ? AND ts >= ? AND ts < ?`,
		w.Source, w.Entrypoint, formatTS(w.From), formatTS(w.To))
	if err != nil {
		return sync, err
	}
	for rows.Next() {
		var id string
		var got stored
		if err := rows.Scan(&id, &got.ts, &got.duration, &got.title); err != nil {
			_ = rows.Close()
			return sync, err
		}
		existing[id] = got
	}
	if err := rows.Close(); err != nil {
		return sync, err
	}
	if err := rows.Err(); err != nil {
		return sync, err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO events (
			source, external_id, ts, duration_ms, type, subtype,
			project, raw_text, session_id, cwd, git_branch,
			entrypoint, is_sidechain, client_version,
			host, port, path_head, title, ingested_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (source, external_id) DO UPDATE SET
			ts = excluded.ts,
			duration_ms = excluded.duration_ms,
			type = excluded.type,
			subtype = excluded.subtype,
			title = excluded.title`)
	if err != nil {
		return sync, err
	}
	defer func() { _ = stmt.Close() }()

	ingestedAt := s.now().UTC().Format(time.RFC3339)
	for _, e := range w.Events {
		var duration any
		if e.DurationMS != nil {
			duration = *e.DurationMS
		}
		was, there := existing[e.ExternalID]
		delete(existing, e.ExternalID)
		if !there {
			// A source may legitimately restate something that begins before
			// the window it was asked about — a meeting that starts at 08:00
			// and runs into a window opening at 09:00. Its row is outside the
			// sweep, so it is not in `existing`, and counting it as new would
			// report one more arrival on every single run for ever.
			var got stored
			err := tx.QueryRow(`SELECT ts, duration_ms, title FROM events WHERE source = ? AND external_id = ?`,
				w.Source, e.ExternalID).Scan(&got.ts, &got.duration, &got.title)
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return sync, err
			default:
				was, there = got, true
			}
		}
		switch {
		case !there:
			sync.New++
		case was.ts != e.TS || !sameDuration(was.duration, e.DurationMS) || was.title != e.Title:
			sync.Moved++
		default:
			continue // unchanged: writing it again would only churn the file
		}
		if _, err := stmt.Exec(
			e.Source, e.ExternalID, e.TS, duration, e.Type, e.Subtype,
			e.Project, e.RawText, e.SessionID, e.CWD, e.GitBranch,
			e.Entrypoint, e.IsSidechain, e.ClientVersion,
			e.Host, e.Port, e.PathHead, e.Title, ingestedAt,
		); err != nil {
			return sync, fmt.Errorf("store event %s: %w", e.ExternalID, err)
		}
	}

	// Nothing is removed unless the source understood the whole of what it was
	// given. This is the one operation in the program that can lose data, and
	// the guard is on the operation rather than on a call site.
	if !w.Whole && len(existing) > 0 {
		sync.RefusedEmpty = true
		return sync, tx.Commit()
	}

	del, err := tx.Prepare(`DELETE FROM events WHERE source = ? AND external_id = ?`)
	if err != nil {
		return sync, err
	}
	defer func() { _ = del.Close() }()
	// Ranged over a map, so the order is Go's. Deletes by primary identity
	// commute, so the result does not depend on it.
	for id := range existing {
		if _, err := del.Exec(w.Source, id); err != nil {
			return sync, fmt.Errorf("remove event %s: %w", id, err)
		}
		sync.Removed++
	}

	return sync, tx.Commit()
}

func sameDuration(stored sql.NullInt64, incoming *int64) bool {
	if incoming == nil {
		return !stored.Valid
	}
	return stored.Valid && stored.Int64 == *incoming
}

// CountEvents counts events with from <= ts < to. Both bounds are instants;
// turning a local day into an instant is the caller's job.
func (s *Store) CountEvents(source string, from, to time.Time) (int, error) {
	query := `SELECT count(*) FROM events WHERE ts >= ? AND ts < ?`
	args := []any{formatTS(from), formatTS(to)}
	if source != "" {
		query += ` AND source = ?`
		args = append(args, source)
	}

	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// EventsBetween returns every event with from <= ts < to, oldest first.
//
// Every column, `title` included, and that one was deliberately left out until
// the attribution rules needed it. It holds the page title, which on a search
// results page is the query somebody typed — the most revealing thing in the
// database — and what reads it is the dictionary: an issue key lives in a path
// segment spoor does not store and in the title, which it does. It is matched
// against and never carried through: no renderer prints it, and a test asserts
// that a title cannot reach either the table or the JSON.
//
// The order is total and does not depend on the order the rows were inserted
// in: two databases holding the same events hand them back the same way, and
// so does the same database after a re-import. Everything downstream — which
// block an event lands in, which project owns a second of the day — is decided
// by walking this slice, so an unstable order here would be an unstable
// report.
func (s *Store) EventsBetween(from, to time.Time) ([]event.Event, error) {
	rows, err := s.db.Query(`
		SELECT source, external_id, ts, duration_ms, type, subtype,
		       project, raw_text, session_id, cwd, git_branch,
		       entrypoint, is_sidechain, client_version,
		       host, port, path_head, title
		FROM events
		WHERE ts >= ? AND ts < ?
		ORDER BY ts, source, external_id`,
		formatTS(from), formatTS(to))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []event.Event
	for rows.Next() {
		var (
			e        event.Event
			duration sql.NullInt64
		)
		if err := rows.Scan(
			&e.Source, &e.ExternalID, &e.TS, &duration, &e.Type, &e.Subtype,
			&e.Project, &e.RawText, &e.SessionID, &e.CWD, &e.GitBranch,
			&e.Entrypoint, &e.IsSidechain, &e.ClientVersion,
			&e.Host, &e.Port, &e.PathHead, &e.Title,
		); err != nil {
			return nil, err
		}
		if duration.Valid {
			ms := duration.Int64
			e.DurationMS = &ms
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// Range is the first and the last event in the database, in UTC. The third
// value is false when there are none at all.
//
// It exists for the question the accumulating subject asks — how long has this
// one thing taken, over however long it has been going — which has no date
// range of its own. Asking about "all of it" needs to know where all of it
// starts, and a report that guessed would either miss the beginning or walk
// through centuries of empty days.
func (s *Store) Range() (first, last time.Time, ok bool, err error) {
	var lo, hi sql.NullString
	if err := s.db.QueryRow(`SELECT min(ts), max(ts) FROM events`).Scan(&lo, &hi); err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if !lo.Valid || !hi.Valid {
		return time.Time{}, time.Time{}, false, nil
	}
	first, err = time.Parse(time.RFC3339, lo.String)
	if err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("unreadable first timestamp %q: %w", lo.String, err)
	}
	last, err = time.Parse(time.RFC3339, hi.String)
	if err != nil {
		return time.Time{}, time.Time{}, false, fmt.Errorf("unreadable last timestamp %q: %w", hi.String, err)
	}
	return first, last, true, nil
}

// TracesBefore is every working directory and browser key the database held
// before a day, as the strings a question is keyed by.
//
// It answers one thing on the question screen — "first time today" — and it is
// a query rather than a report on purpose: the answer is a set of strings, and
// building a year of days to find out whether a host is new would be seconds
// of work for a note.
func (s *Store) TracesBefore(day time.Time) (map[string]bool, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT cwd, host, port, path_head FROM events WHERE ts < ?`,
		formatTS(day))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]bool{}
	for rows.Next() {
		var cwd, host, port, head string
		if err := rows.Scan(&cwd, &host, &port, &head); err != nil {
			return nil, err
		}
		if cwd != "" {
			out["path:"+strings.TrimRight(cwd, "/")] = true
		}
		if host == "" {
			continue
		}
		key := host
		if strings.Contains(key, ":") {
			key = "[" + key + "]"
		}
		if port != "" {
			key += ":" + port
		}
		// Both the key with its segment and the bare host: a question folded
		// by host is asked about the host, and it is not new just because
		// today's segment is.
		out["key:"+key] = true
		if head != "" {
			out["key:"+key+"/"+head] = true
		}
	}
	return out, rows.Err()
}

// TotalEvents counts everything in the database.
func (s *Store) TotalEvents() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&n)
	return n, err
}

// FileState returns what was remembered about path, if anything.
func (s *Store) FileState(source, path string) (FileState, bool, error) {
	var (
		st    FileState
		mtime string
	)
	err := s.db.QueryRow(
		`SELECT size, mtime, read_offset, seam_hash, cursor FROM source_files WHERE source = ? AND path = ?`,
		source, path,
	).Scan(&st.Size, &mtime, &st.ReadOffset, &st.SeamHash, &st.Cursor)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return FileState{}, false, nil
	case err != nil:
		return FileState{}, false, err
	}

	parsed, err := time.Parse(time.RFC3339Nano, mtime)
	if err != nil {
		// Unreadable bookkeeping is not a reason to lose data: re-read the file.
		return FileState{}, false, nil
	}
	st.MTime = parsed
	return st, true, nil
}

// SaveFileState records how far into path we have read.
func (s *Store) SaveFileState(source, path string, st FileState) error {
	_, err := s.db.Exec(`
		INSERT INTO source_files (source, path, size, mtime, read_offset, seam_hash, cursor, seen_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT (source, path) DO UPDATE SET
			size = excluded.size,
			mtime = excluded.mtime,
			read_offset = excluded.read_offset,
			seam_hash = excluded.seam_hash,
			cursor = excluded.cursor,
			seen_at = excluded.seen_at`,
		source, path, st.Size, st.MTime.UTC().Format(time.RFC3339Nano),
		st.ReadOffset, st.SeamHash, st.Cursor, s.now().UTC().Format(time.RFC3339),
	)
	return err
}

// createPrivate makes sure the database file exists and is readable only by
// its owner before SQLite gets a chance to create it 0644.
func createPrivate(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	// The file may predate this rule, or have been created by an older build.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict database permissions: %w", err)
	}
	return nil
}

func formatTS(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
