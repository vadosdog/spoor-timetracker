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
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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

// ingest runs an import that touches nothing real. Every source that looks
// somewhere by default has to be pointed elsewhere or switched off here: a
// test that quietly imports the machine's own browsing history would both
// pass for the wrong reason and read data no test has any business reading.
func ingest(t *testing.T, db, claudeDir string, extra ...string) string {
	t.Helper()
	args := append([]string{
		"ingest",
		"--db", db,
		"--claude-dir", claudeDir,
		"--config", filepath.Join(t.TempDir(), "no-config.yaml"),
		"--no-browser",
	}, extra...)
	return run(t, args...)
}

// The acceptance criterion of this stage, end to end: two runs of ingest in a
// row report the same number of events for a week.
func TestIngestTwiceGivesTheSameWeek(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")

	first := time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local)
	week(t, src, first, 7)

	ingest(t, db, src, "--quiet")
	before := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31")

	ingest(t, db, src, "--quiet")
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

	ingest(t, db, src, "--quiet")
	before := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31")

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	ingest(t, db, src, "--quiet")
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
	ingest(t, db, src, "--quiet")

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

// chromeHistory writes a history database in Chrome's shape. It is invented
// from the published schema; no test reads a real profile.
func chromeHistory(t *testing.T, dir string, visits ...[2]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "History")

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`
		CREATE TABLE urls (id INTEGER PRIMARY KEY AUTOINCREMENT, url LONGVARCHAR,
			title LONGVARCHAR, last_visit_time INTEGER NOT NULL);
		CREATE TABLE visits (id INTEGER PRIMARY KEY AUTOINCREMENT, url INTEGER NOT NULL,
			visit_time INTEGER NOT NULL, transition INTEGER DEFAULT 0 NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	const chromeEpoch = 11644473600000000
	for i, v := range visits {
		when, err := time.ParseInLocation(time.RFC3339, v[0], time.Local)
		if err != nil {
			t.Fatal(err)
		}
		micros := when.UnixMicro() + chromeEpoch
		if _, err := db.Exec(`INSERT INTO urls (id, url, title, last_visit_time) VALUES (?,?,?,?)`,
			i+1, v[1], "page", micros); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO visits (url, visit_time) VALUES (?,?)`,
			i+1, micros); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// Two sources in one import, counted together and apart.
func TestIngestReadsBothSources(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")

	week(t, src, time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local), 7)
	history := chromeHistory(t, filepath.Join(tmp, "Profile 1"),
		[2]string{"2026-08-26T12:00:00+00:00", "https://example.com/a"},
		[2]string{"2026-08-26T12:05:00+00:00", "https://example.com/b"},
	)

	out := run(t, "ingest", "--db", db, "--claude-dir", src,
		"--config", filepath.Join(tmp, "no-config.yaml"), "--browser-history", history)
	if !strings.Contains(out, "browser:") || !strings.Contains(out, "claude-code:") {
		t.Errorf("ingest reported only one source:\n%s", out)
	}

	for _, c := range []struct{ source, want string }{
		{"", "16 events"},
		{"claude-code", "14 events"},
		{"browser", "2 events"},
	} {
		got := run(t, "count", "--db", db, "--from", "2026-08-25", "--to", "2026-08-31", "--source", c.source)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("count --source %q: %q, want %s", c.source, got, c.want)
		}
	}
}

// The ignore list has to reach the source from the config file, and it has to
// keep the domain out of the database rather than out of the display.
func TestIgnoreListFromTheConfigFile(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "spoor.db")
	history := chromeHistory(t, filepath.Join(tmp, "Profile 1"),
		[2]string{"2026-08-26T12:00:00+00:00", "https://videos.example/watch"},
		[2]string{"2026-08-26T12:05:00+00:00", "https://work.example/board"},
	)

	cfg := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfg, []byte("browser:\n  ignore:\n    - videos.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run(t, "ingest", "--db", db, "--claude-dir", filepath.Join(tmp, "no-projects"),
		"--config", cfg, "--browser-history", history, "--quiet")

	if got := run(t, "count", "--db", db, "--from", "2026-08-26", "--to", "2026-08-26"); !strings.HasPrefix(got, "1 events") {
		t.Errorf("count = %q, want 1 event: the ignored domain reached the database", got)
	}
}

// A history file the user named and spoor could not use is a warning about
// that file, not a failed import — and not silence either. A typo in the
// config means that browser is never imported, and nothing else would say so.
func TestAHistoryFileTheUserNamedIsCheckedOutLoud(t *testing.T) {
	tmp := t.TempDir()
	out := ingest(t, filepath.Join(tmp, "spoor.db"), filepath.Join(tmp, "no-projects"))
	if strings.Contains(out, "warning") {
		t.Fatalf("a clean import warned:\n%s", out)
	}

	for _, c := range []struct{ path, want string }{
		{filepath.Join(tmp, "Bookmarks"), "not a browser history file"},
		{filepath.Join(tmp, "typo", "History"), "no such file"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{
			"ingest",
			"--db", filepath.Join(tmp, "spoor.db"),
			"--claude-dir", filepath.Join(tmp, "no-projects"),
			"--config", filepath.Join(tmp, "no-config.yaml"),
			"--browser-history", c.path,
			"--quiet",
		}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", c.path, code)
		}
		if !strings.Contains(stdout.String(), c.want) {
			t.Errorf("%s: stdout = %q, want it to contain %q", c.path, stdout.String(), c.want)
		}
	}
}

// An ignore entry that could never match is an ignore list that silently does
// nothing, which is the worst thing this config file could do.
func TestUselessIgnoreEntryIsAWarning(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(cfg, []byte("browser:\n  ignore:\n    - '*.videos.example'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	history := chromeHistory(t, filepath.Join(tmp, "Profile 1"),
		[2]string{"2026-08-26T12:00:00+00:00", "https://videos.example/watch"})

	out := run(t, "ingest", "--db", filepath.Join(tmp, "spoor.db"),
		"--claude-dir", filepath.Join(tmp, "no-projects"),
		"--config", cfg, "--browser-history", history, "--quiet")
	if !strings.Contains(out, "is not a domain name") {
		t.Errorf("stdout = %q, want a warning about the entry", out)
	}
}

// Hard limit 7: a source declares where it works and stays quietly out of the
// way elsewhere. On a machine with neither source, "spoor ingest" must not
// print two blocks of zeroes that read like a fault.
func TestAnAbsentSourceSaysNothing(t *testing.T) {
	tmp := t.TempDir()
	out := run(t, "ingest",
		"--db", filepath.Join(tmp, "spoor.db"),
		"--claude-dir", filepath.Join(tmp, "no-projects"),
		"--config", filepath.Join(tmp, "no-config.yaml"),
		"--browser-history", filepath.Join(tmp, "nothing", "History"))

	if strings.Contains(out, "claude-code:") {
		t.Errorf("an absent Claude Code announced itself:\n%s", out)
	}
	if strings.Contains(out, "browser:") {
		t.Errorf("an absent browser announced itself:\n%s", out)
	}
	if !strings.Contains(out, "database: 0 events total") {
		t.Errorf("stdout = %q, want the database line", out)
	}
}

// With two valid values, a silent "0 events" is a coin flip between "I have
// none of that" and "I typed it wrong".
func TestUnknownSourceIsRejected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"count", "--db", filepath.Join(t.TempDir(), "spoor.db"),
		"--source", "clode-code"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no such source") {
		t.Errorf("stderr = %q", stderr.String())
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
