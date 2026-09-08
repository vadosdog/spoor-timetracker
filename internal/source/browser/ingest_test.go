// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package browser

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIngestReadsBothFlavours(t *testing.T) {
	st := newStore(t)
	dir := t.TempDir()

	chrome := chromeProfile(t, filepath.Join(dir, "Profile 1"),
		hit{at("09:00"), "https://gitlab.example.com/team/service/-/merge_requests/12", "MR !12", 0},
		hit{at("09:05"), "http://localhost:3000/orders?token=secret", "Orders", 1},
	)
	// Firefox numbers its visit types differently from Chrome: 2 is "typed"
	// here, 1 is "typed" there. Each browser's own vocabulary is kept.
	firefox := firefoxProfile(t, filepath.Join(dir, "abc.default"),
		hit{at("09:10"), "https://github.com/owner/repo/pull/7", "Pull 7", 2},
	)

	rep, err := Ingest(st, []Profile{chrome, firefox}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Visits != 3 || rep.Events != 3 || rep.NewEvents != 3 {
		t.Fatalf("report = %+v, want 3 visits, 3 events, 3 new", rep)
	}

	for _, c := range []struct {
		column string
		want   []string
	}{
		{"host", []string{"gitlab.example.com", "localhost", "github.com"}},
		{"port", []string{"", "3000", ""}},
		{"path_head", []string{"team", "orders", "owner"}},
		{"title", []string{"MR !12", "Orders", "Pull 7"}},
		{"entrypoint", []string{"chrome", "chrome", "firefox"}},
		{"type", []string{"visit", "visit", "visit"}},
		{"subtype", []string{"link", "typed", "typed"}},
	} {
		if got := rowsOf(t, st, c.column); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %q, want %q", c.column, got, c.want)
		}
	}
}

// The port is the only thing that tells two dev servers apart. Without it
// every project on localhost collapses into one.
func TestPortSeparatesDevServers(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "http://localhost:3000/shop", "Shop", 0},
		hit{at("09:01"), "http://localhost:8080/admin", "Admin", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	if got, want := rowsOf(t, st, "port"), []string{"3000", "8080"}; !reflect.DeepEqual(got, want) {
		t.Errorf("port = %q, want %q", got, want)
	}
}

// The boundary of the whole project, as a test: what is stored is host, port
// and one path segment, and nothing that could carry a token or a search.
func TestNothingBelowTheFirstPathSegmentIsStored(t *testing.T) {
	st := newStore(t)
	const (
		token = "ya29-SECRET-TOKEN"
		query = "how+to+quit+my+job"
	)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/reset/" + token + "?next=/inbox#top", "Reset", 0},
		hit{at("09:01"), "https://search.example.com/search?q=" + query, "results", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	rows, err := st.DB().Query(`SELECT * FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}

	for rows.Next() {
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(any)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		for i, cell := range cells {
			s, ok := (*cell.(*any)).(string)
			if !ok {
				continue
			}
			for _, secret := range []string{token, query, "?", "#", "next="} {
				if strings.Contains(s, secret) {
					t.Errorf("column %s holds %q, which contains %q", cols[i], s, secret)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if got, want := rowsOf(t, st, "path_head"), []string{"reset", "search"}; !reflect.DeepEqual(got, want) {
		t.Errorf("path_head = %q, want %q", got, want)
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/a", "A", 0},
		hit{at("09:01"), "https://example.com/b", "B", 0},
	)

	first, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}

	if first.NewEvents != 2 {
		t.Fatalf("first run inserted %d, want 2", first.NewEvents)
	}
	if second.NewEvents != 0 {
		t.Errorf("second run inserted %d, want 0", second.NewEvents)
	}
	if second.ProfilesSkipped != 1 || second.ProfilesRead != 0 {
		t.Errorf("second run read %d profiles and skipped %d, want 0 and 1",
			second.ProfilesRead, second.ProfilesSkipped)
	}

	total, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("%d events after two runs, want 2", total)
	}
}

// The watermark: a history that grew must not cost a full re-read.
func TestIngestReadsOnlyWhatIsNew(t *testing.T) {
	st := newStore(t)
	old := at("09:00").Add(-30 * 24 * time.Hour)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{old, "https://example.com/old", "OLD", 0},
		hit{at("09:00"), "https://example.com/a", "A", 0},
		hit{at("09:30"), "https://example.com/b", "B", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	db := openFixture(t, p.Path)
	appendChrome(t, db, hit{at("10:00"), "https://example.com/c", "C", 0})
	touch(t, p.Path)

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	// Three rows out of four: the new one, plus the overlap window behind the
	// watermark. The month-old visit is not read again, which is the point of
	// being incremental at all.
	if rep.Visits != 3 || rep.NewEvents != 1 {
		t.Errorf("report = %+v, want 3 visits read and 1 new event", rep)
	}
	if got, want := rowsOf(t, st, "title"), []string{"OLD", "A", "B", "C"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %q, want %q", got, want)
	}
}

// A history does not only grow forwards. Browser sync writes another device's
// visits carrying their original timestamps, a corrected clock inserts rows
// behind the newest one, a restored profile does both. A watermark with no
// overlap would never read those again and would never say so — and the
// database is the only copy of anything past the browser's own retention.
func TestVisitsRecordedBehindTheWatermarkAreStillFound(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/a", "A", 0},
		hit{at("12:00"), "https://example.com/b", "B", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	// Written now, timestamped two days before the newest imported visit.
	db := openFixture(t, p.Path)
	appendChrome(t, db, hit{at("10:00").Add(-2 * 24 * time.Hour), "https://example.com/synced", "SYNCED", 0})
	touch(t, p.Path)

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewEvents != 1 {
		t.Fatalf("inserted %d, want 1 — a backdated visit was lost", rep.NewEvents)
	}
	if got, want := rowsOf(t, st, "title"), []string{"SYNCED", "A", "B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %q, want %q", got, want)
	}
}

// A visit cannot honestly be in the future, but a machine whose clock was
// wrong records one — a restored snapshot, a dual-boot RTC read as local
// time, sync from a device running fast. A watermark parked in 2030 is a
// profile that imports nothing for years and never says why, and deleting the
// bad row does not undo it. The overlap protects against skew backwards; this
// is the same protection forwards.
func TestAVisitFromTheFutureDoesNotFreezeTheProfile(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/now", "NOW", 0},
		hit{time.Now().AddDate(4, 0, 0), "https://example.com/2030", "FUTURE", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	var cursor string
	if err := st.DB().QueryRow(
		`SELECT cursor FROM source_files WHERE source = 'browser'`).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor > time.Now().UTC().Format(cursorLayout) {
		t.Fatalf("watermark = %s, which is in the future", cursor)
	}

	// Whatever is browsed after the clock is put right must still arrive.
	db := openFixture(t, p.Path)
	appendChrome(t, db, hit{at("09:00").Add(time.Hour), "https://example.com/after", "AFTER", 0})
	touch(t, p.Path)

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewEvents != 1 {
		t.Fatalf("inserted %d after the clock was corrected, want 1 — the profile is frozen", rep.NewEvents)
	}
}

// One unusable row must cost that row. Aborting would discard the rows
// already read, leave the watermark where it was, and fail again identically
// on every future run — so the profile would never be imported at all, and
// everything after the bad row would be lost once the browser expires it.
func TestOneUnusableRowDoesNotCostTheProfile(t *testing.T) {
	st := newStore(t)
	dir := filepath.Join(t.TempDir(), "Profile 1")
	p := chromeProfile(t, dir,
		hit{at("09:00"), "https://example.com/a", "A", 0},
		hit{at("09:01"), "https://example.com/b", "B", 0},
		hit{at("09:02"), "https://example.com/c", "C", 0},
	)

	// urls.url is nullable in Chrome's own schema, and a visit can outlive
	// the urls row it points at.
	db := openFixture(t, p.Path)
	if _, err := db.Exec(`UPDATE urls SET url = NULL WHERE title = 'B'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM urls WHERE title = 'C'`); err != nil {
		t.Fatal(err)
	}

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("warnings: %v", rep.Errors)
	}
	if rep.Visits != 3 || rep.Unparseable != 2 || rep.NewEvents != 1 {
		t.Errorf("report = %+v, want 3 visits read, 2 unusable, 1 new", rep)
	}
	if got, want := rowsOf(t, st, "title"), []string{"A"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %q, want %q", got, want)
	}
}

// Both browsers number visits with an ordinary rowid, and clearing the
// history starts that numbering again from one. If the id were the whole
// dedup key, everything browsed after a "clear history" would collide with
// what is already stored and vanish.
func TestVisitsAfterAClearedHistoryAreNotSwallowed(t *testing.T) {
	st := newStore(t)
	dir := filepath.Join(t.TempDir(), "Profile 1")
	p := chromeProfile(t, dir, hit{at("09:00"), "https://example.com/a", "A", 0})
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	// Clearing the history in the browser replaces the file: same path, same
	// profile, ids counting from one again.
	if err := os.Remove(p.Path); err != nil {
		t.Fatal(err)
	}
	p = chromeProfile(t, dir, hit{at("11:00"), "https://example.com/b", "B", 0})

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewEvents != 1 {
		t.Fatalf("inserted %d after the history was cleared, want 1", rep.NewEvents)
	}
	if got, want := rowsOf(t, st, "title"), []string{"A", "B"}; !reflect.DeepEqual(got, want) {
		t.Errorf("titles = %q, want %q — the imported visit did not outlive the clear", got, want)
	}
}

// Chrome keeps 90 days and then forgets. An imported visit has to outlive the
// file it came from, or the database is a cache rather than a record.
func TestEventsSurviveTheHistoryBeingDeleted(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/a", "A", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.Path); err != nil {
		t.Fatal(err)
	}

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatalf("a vanished profile must not fail the import: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Errorf("warnings for a vanished profile: %v", rep.Errors)
	}

	total, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("%d events after the source file was deleted, want 1", total)
	}
}

// The ignore list is a privacy control, so it has to run before the insert.
// A domain on it must leave no row at all — not a hidden one.
func TestIgnoredDomainsNeverReachTheDatabase(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://videos.example/watch", "clip", 0},
		hit{at("09:01"), "https://www.videos.example/watch", "clip", 0},
		hit{at("09:02"), "https://notvideos.example/watch", "other", 0},
		hit{at("09:03"), "https://work.example/board", "board", 0},
	)

	ignore, _ := NewIgnore([]string{"Videos.Example"})
	rep, err := Ingest(st, []Profile{p}, ignore)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ignored != 2 {
		t.Errorf("ignored %d, want 2 (the host and its subdomain)", rep.Ignored)
	}

	want := []string{"notvideos.example", "work.example"}
	if got := rowsOf(t, st, "host"); !reflect.DeepEqual(got, want) {
		t.Errorf("hosts = %q, want %q", got, want)
	}
}

// Every field this source stores describes a web address. A file:// URL has
// none of them.
func TestNonWebVisitsAreCountedAndDropped(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "file:///home/someone/notes/report.html", "Report", 0},
		hit{at("09:01"), "chrome-extension://abcdefghijklmnop/options.html", "Options", 0},
		hit{at("09:02"), "https://example.com/a", "A", 0},
	)

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.NotWeb != 2 || rep.Events != 1 {
		t.Errorf("report = %+v, want 2 non-web and 1 event", rep)
	}
	if got, want := rowsOf(t, st, "host"), []string{"example.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hosts = %q, want %q", got, want)
	}
}

// A browser holding its history open is the normal case, not a failure. The
// original is never opened and never written to.
func TestIngestWorksWhileTheDatabaseIsLocked(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/a", "A", 0},
	)

	before, err := os.Stat(p.Path)
	if err != nil {
		t.Fatal(err)
	}

	// A writer with an open transaction: exactly what a running browser is.
	db := openFixture(t, p.Path)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO urls (url, title, last_visit_time) VALUES ('https://x/','x',0)`); err != nil {
		t.Fatal(err)
	}

	rep, err := Ingest(st, []Profile{p}, Ignore{})
	if err != nil {
		t.Fatalf("import failed while the history was locked: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("warnings while the history was locked: %v", rep.Errors)
	}
	if rep.NewEvents != 1 {
		t.Errorf("inserted %d, want 1", rep.NewEvents)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(p.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Errorf("the original history was modified: %v/%d became %v/%d",
			before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}
}

// Two profiles number their visits separately, so the profile has to be part
// of the identity or one of them overwrites the other.
func TestTwoProfilesDoNotCollide(t *testing.T) {
	st := newStore(t)
	dir := t.TempDir()
	one := chromeProfile(t, filepath.Join(dir, "Profile 1"),
		hit{at("09:00"), "https://one.example/a", "one", 0})
	two := chromeProfile(t, filepath.Join(dir, "Profile 2"),
		hit{at("09:00"), "https://two.example/a", "two", 0})

	rep, err := Ingest(st, []Profile{one, two}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewEvents != 2 {
		t.Errorf("inserted %d for two profiles, want 2", rep.NewEvents)
	}
}

// A history with nothing spoor understands in it is a reason to warn about
// that one file, not to lose the rest of the import.
func TestOneUnreadableProfileDoesNotStopTheRest(t *testing.T) {
	st := newStore(t)
	dir := t.TempDir()

	broken := filepath.Join(dir, "Profile 0", chromeHistoryFile)
	if err := os.MkdirAll(filepath.Dir(broken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := chromeProfile(t, filepath.Join(dir, "Profile 1"),
		hit{at("09:00"), "https://example.com/a", "A", 0})

	rep, err := Ingest(st, []Profile{
		{Flavour: "chrome", Name: "Profile 0", Path: broken},
		good,
	}, Ignore{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 1 {
		t.Errorf("warnings = %v, want exactly one", rep.Errors)
	}
	if rep.NewEvents != 1 {
		t.Errorf("inserted %d, want 1 — the healthy profile was lost too", rep.NewEvents)
	}
}

// Timestamps are stored the same way for every source, so that string order
// in SQL is time order.
func TestTimestampsAreNormalised(t *testing.T) {
	st := newStore(t)
	moment := time.Date(2026, 8, 25, 6, 30, 0, 123456000, time.UTC)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{moment, "https://example.com/a", "A", 0})
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	if got, want := rowsOf(t, st, "ts"), []string{"2026-08-25T06:30:00.123Z"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ts = %q, want %q", got, want)
	}
	// The dedup key keeps the microseconds the browser recorded, so that two
	// visits inside one millisecond stay two visits.
	if got := rowsOf(t, st, "external_id"); len(got) != 1 ||
		!strings.Contains(got[0], "2026-08-25T06:30:00.123456Z") {
		t.Errorf("external_id = %q, want the microsecond time in it", got)
	}
}

// A browser reports a duration and spoor does not store it: it measures the
// time until the tab navigated away, so a tab left open overnight claims a
// visit lasting all night.
func TestNoDurationIsInvented(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Profile 1"),
		hit{at("09:00"), "https://example.com/a", "A", 0})
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	var nulls int
	if err := st.DB().QueryRow(
		`SELECT count(*) FROM events WHERE source='browser' AND duration_ms IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 {
		t.Errorf("%d browser events with no duration, want 1", nulls)
	}
}

// touch moves the mtime forward. Writing through a second handle can leave it
// unchanged within the filesystem's resolution, and then the "nothing has
// happened here" check would skip a file that did change.
func touch(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}
