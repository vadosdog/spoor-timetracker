// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "spoor.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func ev(id, ts string) event.Event {
	return event.Event{
		Source:     "claude-code",
		ExternalID: id,
		TS:         ts,
		Type:       "user",
		Project:    "widget",
	}
}

func TestInsertEventsIgnoresDuplicates(t *testing.T) {
	st := open(t)

	n, err := st.InsertEvents([]event.Event{
		ev("a", "2026-08-25T09:00:00.000Z"),
		ev("b", "2026-08-25T09:01:00.000Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("inserted %d, want 2", n)
	}

	n, err = st.InsertEvents([]event.Event{
		ev("a", "2026-08-25T09:00:00.000Z"),
		ev("c", "2026-08-25T09:02:00.000Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("inserted %d on the second batch, want 1", n)
	}

	total, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

// The same external id from a different source is a different event.
func TestExternalIDIsScopedToSource(t *testing.T) {
	st := open(t)

	a := ev("same-id", "2026-08-25T09:00:00.000Z")
	b := a
	b.Source = "browser"

	n, err := st.InsertEvents([]event.Event{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("inserted %d, want 2", n)
	}
}

func TestDurationIsNullForPointEvents(t *testing.T) {
	st := open(t)

	d := int64(42000)
	withDuration := ev("with", "2026-08-25T09:00:00.000Z")
	withDuration.DurationMS = &d

	if _, err := st.InsertEvents([]event.Event{ev("without", "2026-08-25T09:00:00.000Z"), withDuration}); err != nil {
		t.Fatal(err)
	}

	var nulls int
	if err := st.DB().QueryRow(`SELECT count(*) FROM events WHERE duration_ms IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 {
		t.Errorf("%d rows with NULL duration, want 1", nulls)
	}

	var got int64
	if err := st.DB().QueryRow(`SELECT duration_ms FROM events WHERE external_id = 'with'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 42000 {
		t.Errorf("duration_ms = %d, want 42000", got)
	}
}

func TestCountEventsRange(t *testing.T) {
	st := open(t)

	if _, err := st.InsertEvents([]event.Event{
		ev("before", "2026-08-24T23:59:59.999Z"),
		ev("first", "2026-08-25T00:00:00.000Z"),
		ev("middle", "2026-08-27T12:00:00.000Z"),
		ev("last", "2026-08-31T23:59:59.999Z"),
		ev("after", "2026-09-01T00:00:00.000Z"),
	}); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	n, err := st.CountEvents("", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}

	n, err = st.CountEvents("browser", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count for another source = %d, want 0", n)
	}
}

func TestFileStateRoundTrip(t *testing.T) {
	st := open(t)

	if _, ok, err := st.FileState("claude-code", "/nowhere"); err != nil || ok {
		t.Fatalf("unknown file: ok=%v err=%v", ok, err)
	}

	mtime := time.Date(2026, 8, 25, 9, 0, 0, 123456789, time.UTC)
	want := FileState{Size: 4096, MTime: mtime, ReadOffset: 2048}
	if err := st.SaveFileState("claude-code", "/some/session.jsonl", want); err != nil {
		t.Fatal(err)
	}

	got, ok, err := st.FileState("claude-code", "/some/session.jsonl")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.Size != want.Size || got.ReadOffset != want.ReadOffset || !got.MTime.Equal(want.MTime) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	want.ReadOffset = 8192
	want.Size = 8192
	if err := st.SaveFileState("claude-code", "/some/session.jsonl", want); err != nil {
		t.Fatal(err)
	}
	got, _, err = st.FileState("claude-code", "/some/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadOffset != 8192 {
		t.Errorf("ReadOffset = %d after update, want 8192", got.ReadOffset)
	}
}

// The database is a timeline of the user's days. Nobody else on the machine
// gets to read it.
func TestDatabaseIsNotReadableByOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data", "spoor.db")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.InsertEvents([]event.Event{ev("a", "2026-08-25T09:00:00.000Z")}); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue // -wal and -shm need not exist at this moment
		}
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s has mode %04o, want no group or other access", filepath.Base(p), mode)
		}
	}

	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("data directory has mode %04o, want 0700", mode)
	}
}

// A path is a file name, not a URI. One containing a question mark must not
// turn into pragmas or open something else.
func TestOpenHandlesAwkwardPaths(t *testing.T) {
	for _, name := range []string{"who?.db", "100% mine.db", "hash#tag.db"} {
		path := filepath.Join(t.TempDir(), name)

		st, err := Open(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := st.InsertEvents([]event.Event{ev("a", "2026-08-25T09:00:00.000Z")}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}

		// Stat alone would prove nothing: Open creates the file at that path
		// before SQLite sees it. Reopening and finding the row back is what
		// shows SQLite wrote to the same place.
		st, err = Open(path)
		if err != nil {
			t.Fatalf("%s: reopen: %v", name, err)
		}
		total, err := st.TotalEvents()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total != 1 {
			t.Errorf("%s: %d events after reopening, want 1 — SQLite wrote somewhere else", name, total)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// CREATE TABLE IF NOT EXISTS does nothing to a table that is already there, so
// a column added later never reaches a database made by an earlier build. The
// failure is quiet — ingest keeps exiting 0 and importing nothing — and the
// whole point of an accumulating database is that it never quietly stops.
func TestOpenUpgradesAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoor.db")

	// A database in the shape that shipped before seam_hash existed.
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range addedColumns {
		if _, err := st.DB().Exec("ALTER TABLE " + c.table + " DROP COLUMN " + c.column); err != nil {
			t.Fatalf("undo %s.%s: %v", c.table, c.column, err)
		}
		has, err := hasColumn(st.DB(), c.table, c.column)
		if err != nil || has {
			t.Fatalf("%s.%s still present, the test proves nothing", c.table, c.column)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopening an older database: %v", err)
	}
	defer st.Close()

	// The queries that name the new columns must work, not warn.
	if err := st.SaveFileState("claude-code", "/some/session.jsonl", FileState{
		Size: 10, MTime: time.Now(), ReadOffset: 10, SeamHash: "abc",
	}); err != nil {
		t.Fatalf("SaveFileState after upgrade: %v", err)
	}
	got, ok, err := st.FileState("claude-code", "/some/session.jsonl")
	if err != nil || !ok {
		t.Fatalf("FileState after upgrade: ok=%v err=%v", ok, err)
	}
	if got.SeamHash != "abc" {
		t.Errorf("SeamHash = %q, want abc", got.SeamHash)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "spoor.db")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertEvents([]event.Event{ev("a", "2026-08-25T09:00:00.000Z")}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()

	total, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d after reopening, want 1", total)
	}
}
