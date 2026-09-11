// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A feed does not append, it restates — and until this stage spoor read it as
// if it did. Every test here is a way for a day to come out bigger or smaller
// than it was without anything saying so, which is the failure class the whole
// project is built against.
//
// All of them go through `ingest` and then through `report`, because the bug
// they pin was invisible from every unit: the store did exactly what it was
// asked, the parser produced exactly the right meetings, and the day was still
// wrong.

// writeFeed writes a calendar holding the events given, verbatim.
func writeFeed(t *testing.T, path string, events ...string) {
	t.Helper()
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" + strings.Join(events, "") + "END:VCALENDAR\r\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// vevent is one meeting, at the given local time on the given day.
func vevent(uid string, day time.Time, hour, minutes int, extra ...string) string {
	start := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, time.Local)
	end := start.Add(time.Duration(minutes) * time.Minute)
	return fmt.Sprintf("BEGIN:VEVENT\r\nUID:%s\r\nSUMMARY:Planning\r\n"+
		"DTSTART:%s\r\nDTEND:%s\r\n%sEND:VEVENT\r\n",
		uid,
		start.UTC().Format("20060102T150405Z"),
		end.UTC().Format("20060102T150405Z"),
		strings.Join(extra, ""))
}

// meetingHours is how much of one day the calendar accounts for, read the way
// a person would: out of the report, not out of the database.
func meetingHours(t *testing.T, db, cfg string, day time.Time) (starts []string, active time.Duration) {
	t.Helper()
	out := run(t, "report", "--db", db, "--config", cfg,
		"--day="+day.Format(time.DateOnly), "--json", "--timeline")
	var doc struct {
		Days []struct {
			ActiveMS int64 `json:"active_ms"`
			Runs     []struct {
				From string `json:"from"`
				Gap  bool   `json:"gap"`
			} `json:"runs"`
		} `json:"days"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("report --json: %v\n%s", err, out)
	}
	if len(doc.Days) != 1 {
		t.Fatalf("want one day, got %d", len(doc.Days))
	}
	for _, r := range doc.Days[0].Runs {
		if r.Gap {
			continue
		}
		at, err := time.Parse(time.RFC3339, r.From)
		if err != nil {
			t.Fatal(err)
		}
		starts = append(starts, at.Local().Format("15:04"))
	}
	return starts, time.Duration(doc.Days[0].ActiveMS) * time.Millisecond
}

// calendarSetup writes a feed and a config pointing at it, and returns the
// paths and the day the meetings fall on. Yesterday, so the day is closed.
func calendarSetup(t *testing.T) (db, cfg, feed string, day time.Time) {
	t.Helper()
	dir := t.TempDir()
	db = filepath.Join(dir, "spoor.db")
	feed = filepath.Join(dir, "feed.ics")
	cfg = writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      file: "+feed+"\n")
	return db, cfg, feed, time.Now().AddDate(0, 0, -1)
}

func ingestFeed(t *testing.T, db, cfg string) string {
	t.Helper()
	return run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(), "--config", cfg, "--no-browser")
}

// The measured case: one meeting rescheduled. The UID does not change, and
// neither does the occurrence, so the identity is the same row — which used to
// mean the new time was dropped as a duplicate and the database kept the old
// hour for ever.
func TestAMovedMeetingIsMovedRatherThanKeptAtTheOldTime(t *testing.T) {
	db, cfg, feed, day := calendarSetup(t)

	writeFeed(t, feed, vevent("moved@example.invalid", day, 10, 60))
	ingestFeed(t, db, cfg)
	if starts, _ := meetingHours(t, db, cfg, day); len(starts) != 1 || starts[0] != "10:00" {
		t.Fatalf("the first import did not put the meeting at 10:00: %v", starts)
	}

	writeFeed(t, feed, vevent("moved@example.invalid", day, 14, 60))
	out := ingestFeed(t, db, cfg)

	starts, active := meetingHours(t, db, cfg, day)
	if len(starts) != 1 {
		t.Fatalf("the day holds %d meetings, want 1 — a moved meeting was counted twice: %v",
			len(starts), starts)
	}
	if starts[0] != "14:00" {
		t.Errorf("the meeting is still at %s, want 14:00", starts[0])
	}
	if active != time.Hour {
		t.Errorf("the day holds %s of active time, want 1h", active)
	}
	// And it says so out loud. A day silently changing under a report is the
	// thing being fixed here; a day changing without a line is the same fault
	// one level up.
	if !strings.Contains(out, "1 moved or renamed") {
		t.Errorf("nothing said a meeting had moved:\n%s", out)
	}
}

// A whole series moved changes every occurrence key, so the new ones are
// inserted and the old ones used to stay — the shape that quietly doubles a
// day rather than misplacing it.
func TestAMovedSeriesLeavesNothingBehind(t *testing.T) {
	db, cfg, feed, day := calendarSetup(t)
	rule := "RRULE:FREQ=DAILY;COUNT=3\r\n"
	// The series starts two days before the day under test, so the day holds
	// its second occurrence either way.
	base := day.AddDate(0, 0, -1)

	writeFeed(t, feed, vevent("series@example.invalid", base, 9, 30, rule))
	ingestFeed(t, db, cfg)
	if starts, _ := meetingHours(t, db, cfg, day); len(starts) != 1 || starts[0] != "09:00" {
		t.Fatalf("the series did not start at 09:00: %v", starts)
	}

	writeFeed(t, feed, vevent("series@example.invalid", base, 16, 30, rule))
	out := ingestFeed(t, db, cfg)

	starts, active := meetingHours(t, db, cfg, day)
	if len(starts) != 1 {
		t.Fatalf("the day holds %d occurrences, want 1 — the old series is still there: %v",
			len(starts), starts)
	}
	if starts[0] != "16:00" {
		t.Errorf("the occurrence is at %s, want 16:00", starts[0])
	}
	if active != 30*time.Minute {
		t.Errorf("the day holds %s, want 30m", active)
	}
	if !strings.Contains(out, "no longer in the feed") {
		t.Errorf("nothing said the old occurrences were removed:\n%s", out)
	}
}

// Cancelling is skipped at parse time, which used to mean the row outlived the
// meeting: the hour stayed in the day and nothing in the feed could ever take
// it out again.
func TestACancelledMeetingLosesItsHour(t *testing.T) {
	db, cfg, feed, day := calendarSetup(t)

	writeFeed(t, feed, vevent("cancelled@example.invalid", day, 11, 60))
	ingestFeed(t, db, cfg)
	if _, active := meetingHours(t, db, cfg, day); active != time.Hour {
		t.Fatalf("the meeting was not imported: %s", active)
	}

	writeFeed(t, feed, vevent("cancelled@example.invalid", day, 11, 60, "STATUS:CANCELLED\r\n"))
	ingestFeed(t, db, cfg)

	if n := countOf(t, db, "calendar"); n != 0 {
		t.Errorf("%d calendar events survive the cancellation, want 0", n)
	}
}

// The guard on the one operation in the program that can lose data. An empty
// answer is what a genuinely empty calendar looks like — and also what a
// login page, an expired address and a server having a bad morning look like.
// Only the first of those means "delete everything".
func TestAnEmptyFeedDoesNotEraseWhatIsStored(t *testing.T) {
	db, cfg, feed, day := calendarSetup(t)

	writeFeed(t, feed, vevent("kept@example.invalid", day, 12, 60))
	ingestFeed(t, db, cfg)

	writeFeed(t, feed)
	out := ingestFeed(t, db, cfg)

	if n := countOf(t, db, "calendar"); n != 1 {
		t.Errorf("%d calendar events after an empty feed, want 1", n)
	}
	if !strings.Contains(out, "nothing was removed") {
		t.Errorf("the refusal was silent:\n%s", out)
	}
}

// One calendar restating its window says nothing about another's. Getting this
// wrong would make a second subscription delete the first one's meetings on
// every run, which is the same silent loss one door along.
func TestOneCalendarDoesNotSweepAnother(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "spoor.db")
	work := filepath.Join(dir, "work.ics")
	team := filepath.Join(dir, "team.ics")
	cfg := writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      file: "+work+
		"\n    - id: team\n      file: "+team+"\n")
	day := time.Now().AddDate(0, 0, -1)

	writeFeed(t, work, vevent("w@example.invalid", day, 10, 60))
	writeFeed(t, team, vevent("t@example.invalid", day, 15, 60))
	ingestFeed(t, db, cfg)
	if n := countOf(t, db, "calendar"); n != 2 {
		t.Fatalf("%d calendar events after the first import, want 2", n)
	}

	// The work calendar moves its meeting. The team calendar is untouched and
	// must keep every hour it had.
	writeFeed(t, work, vevent("w@example.invalid", day, 11, 60))
	ingestFeed(t, db, cfg)

	starts, _ := meetingHours(t, db, cfg, day)
	want := []string{"11:00", "15:00"}
	if len(starts) != len(want) || starts[0] != want[0] || starts[1] != want[1] {
		t.Errorf("the day is %v, want %v", starts, want)
	}
}

// Replacing a window must not undo idempotence: a second run over an unchanged
// feed still writes nothing and still says nothing changed.
func TestRestatingAWindowIsStillIdempotent(t *testing.T) {
	db, cfg, feed, day := calendarSetup(t)
	writeFeed(t, feed,
		vevent("a@example.invalid", day, 9, 30),
		vevent("b@example.invalid", day, 14, 45))
	ingestFeed(t, db, cfg)

	out := ingestFeed(t, db, cfg)
	for _, unwanted := range []string{"moved or renamed", "no longer in the feed"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("an unchanged feed reported %q:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "0 new") {
		t.Errorf("a second run inserted rows:\n%s", out)
	}
	if n := countOf(t, db, "calendar"); n != 2 {
		t.Errorf("%d calendar events, want 2", n)
	}
}

// A meeting older than the window that was expanded is not in the feed's
// answer *and* not something the feed was asked about. Deleting it would turn
// a narrow --calendar-back into a way of quietly forgetting last year.
func TestASweepDoesNotReachOutsideTheWindowItAskedAbout(t *testing.T) {
	db, cfg, feed, day := calendarSetup(t)
	old := day.AddDate(0, 0, -20)

	writeFeed(t, feed, vevent("old@example.invalid", old, 10, 60))
	run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(), "--config", cfg,
		"--no-browser", "--calendar-back", "720h") // 30 days

	// Now a run that only looks a week back. The old meeting is outside it.
	writeFeed(t, feed, vevent("new@example.invalid", day, 10, 60))
	run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(), "--config", cfg,
		"--no-browser", "--calendar-back", "168h")

	if n := countOf(t, db, "calendar"); n != 2 {
		t.Errorf("%d calendar events, want 2: a sweep reached past its own window", n)
	}
}

// countOf reads a source's event count the way a person would.
func countOf(t *testing.T, db, source string) int {
	t.Helper()
	out := run(t, "count", "--db", db, "--source", source,
		"--from", time.Now().AddDate(0, 0, -400).Format(time.DateOnly),
		"--to", time.Now().AddDate(0, 0, 30).Format(time.DateOnly))
	var n int
	if _, err := fmt.Sscanf(out, "%d events", &n); err != nil {
		t.Fatalf("count said %q", strings.TrimSpace(out))
	}
	return n
}

// A meeting that begins before the window and runs into it is restated by the
// feed on every run — the parser widens its own window by the length of a
// meeting so the hour lands on the right day. Its row is outside the sweep, so
// it is not in the set the sweep compares against, and it was counted as an
// arrival every single time.
func TestAMeetingStraddlingTheWindowIsNotNewTwice(t *testing.T) {
	db, cfg, feed, date := calendarSetup(t)
	// Two hours, starting at 08:00. A window that opens at 09:00 holds the
	// second hour of it.
	writeFeed(t, feed, vevent("straddle@example.invalid", date, 8, 120))
	from := time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, time.Local)
	back := time.Since(from)

	first := run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(), "--config", cfg,
		"--no-browser", "--calendar-back", back.Truncate(time.Minute).String())
	if !strings.Contains(first, "1 new") {
		t.Fatalf("the meeting was not imported:\n%s", first)
	}
	again := run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(), "--config", cfg,
		"--no-browser", "--calendar-back", back.Truncate(time.Minute).String())
	if !strings.Contains(again, "0 new") {
		t.Errorf("a meeting that starts before the window was counted new again:\n%s", again)
	}
	if n := countOf(t, db, "calendar"); n != 1 {
		t.Errorf("%d calendar events, want 1", n)
	}
}

// One event this build cannot read stops the deleting half. Spoor failing to
// understand a feed is not the feed saying a meeting is gone, and this is the
// only operation in the program that loses data.
func TestOneUnreadableEventStopsTheSweep(t *testing.T) {
	db, cfg, feed, date := calendarSetup(t)
	writeFeed(t, feed,
		vevent("keep@example.invalid", date, 10, 60),
		vevent("gone@example.invalid", date, 14, 60))
	ingestFeed(t, db, cfg)
	if n := countOf(t, db, "calendar"); n != 2 {
		t.Fatalf("%d events after the first import, want 2", n)
	}

	// The second meeting disappears from the feed — and a *third* event, which
	// the feed still reads fine, grows a repetition rule this parser refuses
	// to guess at. So the feed yields a meeting: the case a first attempt at
	// this guard did not reach, because it only looked at feeds that yielded
	// nothing at all.
	writeFeed(t, feed,
		vevent("keep@example.invalid", date, 10, 60),
		vevent("series@example.invalid", date, 16, 30,
			"RRULE:FREQ=MONTHLY;BYSETPOS=2;BYDAY=MO\r\n"))
	out := ingestFeed(t, db, cfg)

	if n := countOf(t, db, "calendar"); n != 2 {
		t.Errorf("%d events, want 2: a feed spoor could not read in full deleted one", n)
	}
	if !strings.Contains(out, "could not be read in full") {
		t.Errorf("nothing said why nothing was removed:\n%s", out)
	}

	// And once the feed reads cleanly again, the removal happens.
	writeFeed(t, feed, vevent("keep@example.invalid", date, 10, 60))
	ingestFeed(t, db, cfg)
	if n := countOf(t, db, "calendar"); n != 1 {
		t.Errorf("%d events, want 1: a clean feed did not remove what it no longer holds", n)
	}
}
