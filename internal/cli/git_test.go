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

// fakeRepo writes a repository whose whole existence is one reflog. Hand
// written, like every fixture here: a real reflog carries branch names and an
// address.
func fakeRepo(t *testing.T, lines ...string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".git", "logs", "refs", "heads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// reflogLine is one action, `ago` before now, so that it lands inside the
// default window whatever day the suite runs on.
func reflogLine(old, next string, ago time.Duration, msg string) string {
	return fmt.Sprintf("%s %s Dev <dev@example.invalid> %d +0000\t%s",
		old, next, time.Now().Add(-ago).Unix(), msg)
}

const (
	sha1s = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sha2s = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func gitConfig(t *testing.T, id, path string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("git:\n  repos:\n    - id: %s\n      path: %s\n", id, path)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestGitImportIsIdempotent is hard limit 3 for this source. The reflog is
// re-read whole every run, so if the identity of an entry were not stable the
// database would grow on every single import.
func TestGitImportIsIdempotent(t *testing.T) {
	repo := fakeRepo(t,
		reflogLine(sha1s, sha2s, 2*time.Hour, "commit: one"),
		reflogLine(sha2s, sha1s, time.Hour, "checkout: moving from main to spike"),
	)
	cfg := gitConfig(t, "fixture", repo)
	db := filepath.Join(t.TempDir(), "spoor.db")

	first := run(t, "ingest", "--db", db, "--config", cfg,
		"--no-claude-code", "--no-browser", "--no-calendar")
	if !strings.Contains(first, "2 found, 2 new") {
		t.Fatalf("want two new events on the first run, got:\n%s", first)
	}

	second := run(t, "ingest", "--db", db, "--config", cfg,
		"--no-claude-code", "--no-browser", "--no-calendar")
	if !strings.Contains(second, "2 found, 0 new") {
		t.Errorf("a second run over the same reflog was not free, got:\n%s", second)
	}
	if !strings.Contains(second, "database: 2 events total") {
		t.Errorf("the database grew on a second run:\n%s", second)
	}
}

// TestAnImportedEntryOutlivesItsReflog. Git truncates its own log and a rebase
// makes a commit unreachable; the database is the only copy after that. Same
// promise the Claude Code source makes about its 30-day files.
func TestAnImportedEntryOutlivesItsReflog(t *testing.T) {
	repo := fakeRepo(t,
		reflogLine(sha1s, sha2s, 2*time.Hour, "commit: one"),
		reflogLine(sha2s, sha1s, time.Hour, "commit: two"),
	)
	cfg := gitConfig(t, "fixture", repo)
	db := filepath.Join(t.TempDir(), "spoor.db")
	run(t, "ingest", "--db", db, "--config", cfg, "--no-claude-code", "--no-browser", "--no-calendar")

	// The log is pruned back to its last entry, as `git gc` would leave it.
	log := filepath.Join(repo, ".git", "logs", "refs", "heads", "main")
	kept := reflogLine(sha2s, sha1s, time.Hour, "commit: two")
	if err := os.WriteFile(log, []byte(kept+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := run(t, "ingest", "--db", db, "--config", cfg, "--no-claude-code", "--no-browser", "--no-calendar")
	if !strings.Contains(out, "database: 2 events total") {
		t.Errorf("pruning the reflog took an imported event with it:\n%s", out)
	}
}

// TestNoGitBeatsTheConfig. Every source has a flag that turns it off whatever
// is written down, and the measurement of this stage depends on this one.
func TestNoGitBeatsTheConfig(t *testing.T) {
	repo := fakeRepo(t, reflogLine(sha1s, sha2s, time.Hour, "commit: one"))
	cfg := gitConfig(t, "fixture", repo)
	db := filepath.Join(t.TempDir(), "spoor.db")

	out := run(t, "ingest", "--db", db, "--config", cfg,
		"--no-claude-code", "--no-browser", "--no-calendar", "--no-git")
	if strings.Contains(out, "git:") {
		t.Errorf("--no-git did not turn the source off:\n%s", out)
	}
	if !strings.Contains(out, "database: 0 events total") {
		t.Errorf("--no-git still imported something:\n%s", out)
	}
}

// TestNoRepositoryConfiguredSaysNothing. A machine with nothing written down
// is the default, and the default is silent — the same rule the browser
// follows on a machine with no browser.
func TestNoRepositoryConfiguredSaysNothing(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("report:\n  cluster_gap: 10m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "spoor.db")
	out := run(t, "ingest", "--db", db, "--config", cfg,
		"--no-claude-code", "--no-browser", "--no-calendar")
	if strings.Contains(out, "git") {
		t.Errorf("a machine with no repository configured was told about git:\n%s", out)
	}
}

// TestARepeatedRepositoryIdIsRefused. The id is part of every entry's
// identity, so two repositories sharing one would drop the second one's
// entries on the unique index — no error, no warning, an import that reports
// success and holds half of what it read. The exact failure a calendar id
// already had to be made fatal for.
func TestARepeatedRepositoryIdIsRefused(t *testing.T) {
	one := fakeRepo(t, reflogLine(sha1s, sha2s, time.Hour, "commit: one"))
	two := fakeRepo(t, reflogLine(sha2s, sha1s, time.Hour, "commit: two"))
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("git:\n  repos:\n    - id: same\n      path: %s\n    - id: same\n      path: %s\n", one, two)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// A Claude Code session on the same run, to prove the refusal costs the
	// git section and nothing else. A typo in one part of the config is no
	// reason for the rest of somebody's day to go unimported — the same
	// doctrine the calendar section already follows.
	projects := filepath.Join(t.TempDir(), "projects", "-home-u-src-widget")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	jsonl := fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":"%s","sessionId":"s-1",`+
		`"cwd":"/home/u/src/widget","gitBranch":"main","entrypoint":"cli",`+
		`"isSidechain":false,"userType":"external","version":"2.1.219",`+
		`"message":{"content":"x"}}`+"\n", ts)
	if err := os.WriteFile(filepath.Join(projects, "s.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(t.TempDir(), "spoor.db")
	var out, errOut bytes.Buffer
	if code := Run([]string{"ingest", "--db", db, "--config", cfg,
		"--claude-dir", filepath.Dir(projects), "--no-browser", "--no-calendar"},
		&out, &errOut); code == 0 {
		t.Fatalf("a repeated repository id was accepted:\n%s%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "no repository was read") {
		t.Errorf("the refusal did not say what it cost:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "database: 1 events total") {
		t.Errorf("a broken git section threw away another source's import:\n%s", out.String())
	}
}

// TestAMisspeltRepositoryPathIsSaidOutLoud. Nobody discovered this path,
// somebody typed it — and a typo means those hours are never imported and
// nothing ever says so.
func TestAMisspeltRepositoryPathIsSaidOutLoud(t *testing.T) {
	cfg := gitConfig(t, "gone", filepath.Join(t.TempDir(), "not-a-repo"))
	db := filepath.Join(t.TempDir(), "spoor.db")
	out := run(t, "ingest", "--db", db, "--config", cfg,
		"--no-claude-code", "--no-browser", "--no-calendar")
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "not a git repository") {
		t.Errorf("a configured path that is not a repository passed quietly:\n%s", out)
	}
}

// TestGitCarriesNoDurationIntoTheDatabase. The promise of the stage, checked
// where it would actually do damage: a duration in this column invites the
// reporting stage to turn a commit into an interval.
func TestGitCarriesNoDurationIntoTheDatabase(t *testing.T) {
	repo := fakeRepo(t, reflogLine(sha1s, sha2s, time.Hour, "commit: one"))
	cfg := gitConfig(t, "fixture", repo)
	db := filepath.Join(t.TempDir(), "spoor.db")
	run(t, "ingest", "--db", db, "--config", cfg, "--no-claude-code", "--no-browser", "--no-calendar")

	st, closeDB, err := openStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()

	var n int
	if err := st.DB().QueryRow(
		`SELECT count(*) FROM events WHERE source = 'git' AND duration_ms IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d git rows carry a duration", n)
	}
}

// TestAConfirmedDayDoesNotMoveWhenGitArrives is the decision of this stage,
// checked end to end.
//
// Git is the first source that lands events under days somebody has already
// gone through by hand, and it backfills years of them in one run. The rule
// from the stage before stands unchanged: the events land, the snapshot is
// read rather than recomputed, and the run says which days it was — so the
// person decides whether their afternoon is worth taking again.
func TestAConfirmedDayDoesNotMoveWhenGitArrives(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	before := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")

	// A commit inside the confirmed day, in the same checkout the day's other
	// traces are in.
	when := time.Date(date.Year(), date.Month(), date.Day(), 9, 7, 0, 0, time.Local)
	repo := fakeRepo(t, fmt.Sprintf("%s %s Dev <dev@example.invalid> %d +0000\tcommit: x",
		sha1s, sha2s, when.Unix()))
	gitCfg := filepath.Join(t.TempDir(), "git.yaml")
	if err := os.WriteFile(gitCfg, []byte(fmt.Sprintf(
		"attribution:\n  fallback: none\ngit:\n  repos:\n    - id: widget\n      path: %s\n", repo)),
		0o600); err != nil {
		t.Fatal(err)
	}

	out := run(t, "ingest", "--db", db, "--config", gitCfg,
		"--no-claude-code", "--no-browser", "--no-calendar")
	if !strings.Contains(out, "1 found, 1 new") {
		t.Fatalf("the commit did not land in the confirmed day:\n%s", out)
	}
	if !strings.Contains(out, day) || !strings.Contains(out, "reads exactly as it was filed") {
		t.Errorf("the run did not say the confirmed day had been disturbed:\n%s", out)
	}

	if after := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json"); after != before {
		t.Errorf("a confirmed day moved when git arrived:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestTheDisturbedNoteSurvivesManyDays guards the decision of this stage. Git
// is the first source that lands events under days somebody has already gone
// through by hand, and it lands years of them at once. The note that used to
// join a day or two with commas has to stay readable at fourteen, and it has
// to say the thing that actually matters: nothing moved.
func TestTheDisturbedNoteSurvivesManyDays(t *testing.T) {
	var days []string
	for i := 1; i <= 9; i++ {
		days = append(days, fmt.Sprintf("2026-09-%02d", i))
	}
	note := disturbedNote(days)
	if !strings.Contains(note, "9 confirmed days") {
		t.Errorf("the count is not the first thing said: %s", note)
	}
	if strings.Contains(note, "2026-09-09") {
		t.Errorf("every day was named instead of capped: %s", note)
	}
	if !strings.Contains(note, "and 4 more") {
		t.Errorf("the capped remainder was not counted: %s", note)
	}
	// The sentence people actually need: nothing moved.
	for _, want := range []string{"reads exactly as it was filed", "--reconfirm", "--recompute"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not mention %q: %s", want, note)
		}
	}
	if one := disturbedNote([]string{"2026-09-08"}); !strings.Contains(one, "1 confirmed day") {
		t.Errorf("one day is not counted as one: %s", one)
	}
}
