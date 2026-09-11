// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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

	// A database in the shape that shipped before any of these columns did.
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// The indexes go first: SQLite will not drop a column an index names.
	if _, err := st.DB().Exec(`DROP INDEX IF EXISTS events_host`); err != nil {
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
		Size: 10, MTime: time.Now(), ReadOffset: 10, SeamHash: "abc", Cursor: "2026-08-25T09:00:00.000000Z",
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
	if got.Cursor != "2026-08-25T09:00:00.000000Z" {
		t.Errorf("Cursor = %q, want the saved watermark", got.Cursor)
	}

	browsing := ev("chrome/Profile 1/2026-08-25T09:00:00.000000Z/1", "2026-08-25T09:00:00.000Z")
	browsing.Source = "browser"
	browsing.Host = "example.com"
	browsing.Port = "3000"
	browsing.PathHead = "orders"
	browsing.Title = "Orders"
	if _, err := st.InsertEvents([]event.Event{browsing}); err != nil {
		t.Fatalf("InsertEvents after upgrade: %v", err)
	}
	var host string
	if err := st.DB().QueryRow(`SELECT host FROM events WHERE source = 'browser'`).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if host != "example.com" {
		t.Errorf("host = %q after upgrade, want example.com", host)
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

// The report is built by walking events in order, so the order has to be the
// same every time — including for two events that share a timestamp, which
// happens whenever two sessions are busy at once.
func TestEventsBetweenIsOrdered(t *testing.T) {
	st := open(t)

	same := "2026-05-04T09:00:00.000Z"
	if _, err := st.InsertEvents([]event.Event{
		{Source: "browser", ExternalID: "b2", TS: same, Type: "visit"},
		{Source: "claude-code", ExternalID: "c1", TS: "2026-05-04T08:00:00.000Z", Type: "user"},
		{Source: "browser", ExternalID: "b1", TS: same, Type: "visit"},
		{Source: "claude-code", ExternalID: "c2", TS: same, Type: "user"},
	}); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	got, err := st.EventsBetween(from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ExternalID)
	}
	// By time, then by source, then by the source's own id.
	if want := "[c1 b1 b2 c2]"; fmt.Sprint(ids) != want {
		t.Errorf("order %v, want %s", ids, want)
	}
}

// The range is the same half-open one count uses: the lower bound is in, the
// upper bound is not.
func TestEventsBetweenBounds(t *testing.T) {
	st := open(t)
	if _, err := st.InsertEvents([]event.Event{
		{Source: "claude-code", ExternalID: "before", TS: "2026-05-03T23:59:59.999Z", Type: "user"},
		{Source: "claude-code", ExternalID: "first", TS: "2026-05-04T00:00:00.000Z", Type: "user"},
		{Source: "claude-code", ExternalID: "last", TS: "2026-05-04T23:59:59.999Z", Type: "user"},
		{Source: "claude-code", ExternalID: "after", TS: "2026-05-05T00:00:00.000Z", Type: "user"},
	}); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	got, err := st.EventsBetween(from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ExternalID != "first" || got[1].ExternalID != "last" {
		t.Errorf("got %d events: %+v", len(got), got)
	}
}

// The title is loaded, and it is the one column that had to be argued into
// this query: it is what an attribution rule reads to find an issue key, and
// also the most revealing thing in the database. Loading it is deliberate, and
// what keeps it from being printed is a test in the report package rather than
// this absence — see TestNeitherRendererPrintsATitleOrALabel there, which
// checks every view in both formats.
func TestEventsBetweenLoadsTitles(t *testing.T) {
	st := open(t)
	const title = "SPOOR-1 make the report say what it means"
	if _, err := st.InsertEvents([]event.Event{{
		Source: "browser", ExternalID: "v1", TS: "2026-05-04T09:00:00.000Z",
		Type: "visit", Host: "tracker.test", PathHead: "browse", Title: title,
	}}); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	got, err := st.EventsBetween(from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Title != title {
		t.Errorf("Title = %q, want %q", got[0].Title, title)
	}
}

// A column added to a table that already exists reaches nobody: CREATE TABLE
// IF NOT EXISTS does nothing to a table that is there, and the failure is a
// query that says "no such column" — or, in the shape this project has already
// paid for once, an import that exits 0 and stores nothing.
//
// So every column in addedColumns is checked against a database opened twice:
// once by a build that did not have it, and once by this one.
func TestEveryAddedColumnReachesAnOlderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoor.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// Take the columns away again, which is what an older build's database
	// looks like. A column an index depends on cannot be dropped; those are
	// covered by the other tests in this file.
	dropped := 0
	for _, c := range addedColumns {
		if _, err := st.db.Exec(fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", c.table, c.column)); err == nil {
			dropped++
		}
	}
	if dropped == 0 {
		t.Fatal("no column could be taken away, so this test proves nothing")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("opening a database missing the added columns failed: %v", err)
	}
	defer func() { _ = st.Close() }()

	for _, c := range addedColumns {
		has, err := hasColumn(st.db, c.table, c.column)
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Errorf("%s.%s did not reach a database that predates it", c.table, c.column)
		}
	}
}

// A confirmed day goes into the database and comes back the same.
//
// Field by field, by reflection, because that is the only version of this test
// that keeps working. Three fields were added to these rows while this stage
// was being written and every one of them reached the struct and not the SQL:
// the value was carried all the way to the insert and dropped there, so the
// day looked right until it was frozen and then quietly held zeros. Nothing
// failed; a number simply stopped existing.
//
// So this walks every field of every row rather than the ones somebody thought
// of, and a new column that is not in both statements fails here rather than
// in six months in somebody's report.
func TestAConfirmedDayRoundTripsEveryField(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "spoor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	work := true
	at := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	want := ConfirmedDay{
		Day:               "2026-05-04",
		ConfirmedAt:       at,
		ConfigHash:        "abc123",
		Version:           "test",
		ClusterGap:        11 * time.Minute,
		AttentionWindow:   6 * time.Minute,
		Head:              3 * time.Minute,
		Tail:              4 * time.Minute,
		CountBackground:   true,
		OneNeighbour:      7 * time.Minute,
		Claimed:           8 * time.Minute,
		QuestionsTotal:    12,
		QuestionsAnswered: 9,
		Rows: []ConfirmedRow{{
			Project: "Alpha", Subject: "beta", Work: &work, Events: 42,
			Attention: 1 * time.Minute, Claimed: 2 * time.Minute,
			Background: 3 * time.Minute, Padding: 4 * time.Minute,
			Agent: 5 * time.Minute, Wall: 6 * time.Minute,
		}},
		Stretches: []ConfirmedStretch{{
			From: at, To: at.Add(time.Minute), Project: "Alpha", Subject: "beta",
			Kind: "attention", Ground: "rule",
		}},
		OneOffs: []ConfirmedOneOff{{
			From: at, To: at.Add(time.Minute), Project: "Alpha", Subject: "beta",
			Reason: "nothing here could carry a rule",
		}},
	}
	want.OneOffCount = len(want.OneOffs)

	if err := st.Confirm(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Confirmed(want.Day)
	if err != nil || !ok {
		t.Fatalf("reading it back: %v, found=%v", err, ok)
	}

	// Every scalar field of the day, and of one row of each kind. Reflection
	// rather than a list, so that a field added tomorrow is covered today.
	compare(t, "day", reflect.ValueOf(want), reflect.ValueOf(got))
	compare(t, "row", reflect.ValueOf(want.Rows[0]), reflect.ValueOf(got.Rows[0]))
	compare(t, "stretch", reflect.ValueOf(want.Stretches[0]), reflect.ValueOf(got.Stretches[0]))
	compare(t, "one-off", reflect.ValueOf(want.OneOffs[0]), reflect.ValueOf(got.OneOffs[0]))
}

// compare checks every field that is not a slice — the slices are compared
// element by element by the caller.
func compare(t *testing.T, what string, want, got reflect.Value) {
	t.Helper()
	for i := 0; i < want.NumField(); i++ {
		field := want.Type().Field(i)
		if field.Type.Kind() == reflect.Slice {
			continue
		}
		a, b := want.Field(i).Interface(), got.Field(i).Interface()
		if at, ok := a.(time.Time); ok {
			if !at.Equal(b.(time.Time)) {
				t.Errorf("%s.%s: wrote %v, read back %v", what, field.Name, a, b)
			}
			continue
		}
		if field.Type.Kind() == reflect.Pointer {
			if want.Field(i).IsNil() != got.Field(i).IsNil() {
				t.Errorf("%s.%s: wrote %v, read back %v", what, field.Name, a, b)
				continue
			}
			if !want.Field(i).IsNil() &&
				want.Field(i).Elem().Interface() != got.Field(i).Elem().Interface() {
				t.Errorf("%s.%s: wrote %v, read back %v",
					what, field.Name, want.Field(i).Elem(), got.Field(i).Elem())
			}
			continue
		}
		if a != b {
			t.Errorf("%s.%s: wrote %v, read back %v", what, field.Name, a, b)
		}
	}
}

// The other two tables a person's answers live in, round-tripped the same way
// and for the same reason: every one of them has gained a column since it was
// written, and a column that reaches the struct and not the SQL fails
// silently.
func TestAnswersRoundTripEveryField(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "spoor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	at := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	assignment := Assignment{
		Day: "2026-05-04", From: at, To: at.Add(time.Hour),
		Project: "Alpha", Subject: "beta", ClearSubject: true,
		OneOff: true, Reason: "nothing here could carry a rule",
	}
	if err := st.SaveAssignment(assignment); err != nil {
		t.Fatal(err)
	}
	got, err := st.Assignments(assignment.Day)
	if err != nil || len(got) != 1 {
		t.Fatalf("reading it back: %v, %d rows", err, len(got))
	}
	compare(t, "assignment", reflect.ValueOf(assignment), reflect.ValueOf(got[0]))

	answer := WindowAnswer{
		Day: "2026-05-04", From: at, To: at.Add(time.Hour),
		Worked: true, Project: "Alpha", Subject: "beta",
		Left: "Alpha", Right: "Gamma",
	}
	if err := st.SaveWindowAnswer(answer); err != nil {
		t.Fatal(err)
	}
	answers, err := st.WindowAnswers(answer.Day)
	if err != nil || len(answers) != 1 {
		t.Fatalf("reading it back: %v, %d rows", err, len(answers))
	}
	compare(t, "window answer", reflect.ValueOf(answer), reflect.ValueOf(answers[0]))
}

// schema.sql and addedColumns have to agree.
//
// A column that exists only in the migration list reaches the database and no
// comment reaches the reader: `schema.sql` is the file CLAUDE.md singles out as
// the one somebody opens beside `sqlite3`, and the README promises that reading
// it shows every column there is with the comment saying what it holds. Two
// columns went in through the migration alone and were reported as fixed twice
// before this test existed.
func TestEveryMigratedColumnIsAlsoDeclared(t *testing.T) {
	for _, c := range addedColumns {
		// The declaration, not merely the word: "events" appears in prose all
		// over this file.
		decl := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(c.column) + `\s+\w`)
		table := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` +
			regexp.QuoteMeta(c.table) + ` \((.*?)\n\);`).FindStringSubmatch(schemaSQL)
		if table == nil {
			t.Errorf("%s is in addedColumns and has no CREATE TABLE in schema.sql", c.table)
			continue
		}
		if !decl.MatchString(table[1]) {
			t.Errorf("%s.%s reaches the database through addedColumns and is not declared "+
				"in schema.sql, so it has no comment and nobody reading the database can "+
				"find out what it holds", c.table, c.column)
		}
	}
}
