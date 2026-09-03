// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// week writes a synthetic session log covering seven local days, two events
// per day, at local noon so that the local date of an event is unambiguous in
// any time zone the test might run in.
func week(t *testing.T, dir string, first time.Time, days int) string {
	t.Helper()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "session.jsonl")

	var b strings.Builder
	for d := 0; d < days; d++ {
		day := first.AddDate(0, 0, d)
		noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.Local)
		for i := 0; i < 2; i++ {
			ts := noon.Add(time.Duration(i) * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
			fmt.Fprintf(&b, `{"type":"user","uuid":"d%d-%d","timestamp":"%s","sessionId":"s-1",`+
				`"cwd":"/home/u/projects/widget","gitBranch":"main","entrypoint":"cli",`+
				`"isSidechain":false,"userType":"external","version":"2.1.219",`+
				`"message":{"content":"x"}}`+"\n", d, i, ts)
		}
		// Session state, skipped silently.
		fmt.Fprintf(&b, `{"type":"mode","mode":"default","sessionId":"s-1"}`+"\n")
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(args, &out, &errOut); code != 0 {
		t.Fatalf("spoor %s exited %d: %s%s", strings.Join(args, " "), code, out.String(), errOut.String())
	}
	return out.String()
}

// The acceptance criterion of this stage, end to end: two runs of ingest in a
// row report the same number of events for a week.
func TestIngestTwiceGivesTheSameWeek(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")

	first := time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local)
	week(t, src, first, 7)

	run(t, "ingest", "--db", db, "--claude-dir", src, "--quiet")
	before := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31")

	run(t, "ingest", "--db", db, "--claude-dir", src, "--quiet")
	after := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31")

	if before != after {
		t.Fatalf("the week changed between two runs:\n  %s  %s", before, after)
	}
	if want := "14 events from 2026-08-25 to 2026-08-31\n"; before != want {
		t.Errorf("got %q, want %q", before, want)
	}
}

// The accumulating database, at the CLI level: Claude Code erases its JSONL
// after 30 days, and the week must read the same afterwards as it did before.
func TestWeekSurvivesSourceFileDeletion(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")

	path := week(t, src, time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local), 7)

	run(t, "ingest", "--db", db, "--claude-dir", src, "--quiet")
	before := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	run(t, "ingest", "--db", db, "--claude-dir", src, "--quiet")
	after := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31")

	if before != after {
		t.Fatalf("deleting the source file changed the week:\n  before %s  after  %s", before, after)
	}
}

func TestCountRangeBounds(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")

	week(t, src, time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local), 7)
	run(t, "ingest", "--db", db, "--claude-dir", src, "--quiet")

	// A single day is two events, and the last day of the range is included.
	if got := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-25"); !strings.HasPrefix(got, "2 events") {
		t.Errorf("single day: %q", got)
	}
	if got := run(t, "count", "--db", db, "--from", "2026-08-31", "--to", "2026-08-31"); !strings.HasPrefix(got, "2 events") {
		t.Errorf("last day of the week: %q", got)
	}
	if got := run(t, "count", "--db", db, "--from", "2026-09-01", "--to", "2026-09-07"); !strings.HasPrefix(got, "0 events") {
		t.Errorf("empty week: %q", got)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"teleport"}, &out, &errOut); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestBadDateIsReportedNotGuessed(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run([]string{"count", "--db", filepath.Join(t.TempDir(), "spoor.db"), "--from", "25.08.2026"}, &out, &errOut)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "YYYY-MM-DD") {
		t.Errorf("stderr = %q", errOut.String())
	}
}
