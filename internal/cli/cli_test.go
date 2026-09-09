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
	"encoding/json"
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

// runBoth is run when the point of the test is what went to stderr. The two
// streams are kept apart on purpose: `report --json` writes a document to
// stdout, so a warning printed there would make it unparseable.
func runBoth(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(args, &out, &errOut); code != 0 {
		t.Fatalf("spoor %s exited %d: %s%s", strings.Join(args, " "), code, out.String(), errOut.String())
	}
	return out.String(), errOut.String()
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

// fails runs a command that is expected not to work, and returns everything
// it said. A flag that is wrong has to say so: the alternative is a report
// about the wrong day that looks exactly like a report about the right one.
func fails(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut)
	if code == 0 {
		t.Fatalf("spoor %s was expected to fail; it printed %s", strings.Join(args, " "), out.String())
	}
	return out.String() + errOut.String()
}

// reportArgs points every default somewhere harmless. Without --config the
// command would read the config of whoever is running the tests.
func reportArgs(t *testing.T, db string, extra ...string) []string {
	t.Helper()
	return append([]string{
		"report", "--db", db, "--config", filepath.Join(t.TempDir(), "no-config.yaml"),
	}, extra...)
}

func reportFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")
	week(t, src, time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local), 7)
	ingest(t, db, src, "--quiet")
	return db
}

// reportJSON runs the report and reads the numbers out of it. The table pads
// its columns with spaces, so matching a duration in it matches whichever
// column happens to hold that string — which is how three tests here once
// passed while asserting the wall clock and calling it attention.
func reportJSON(t *testing.T, db string, extra ...string) struct {
	Options struct {
		ClusterGap      string `json:"cluster_gap"`
		AttentionWindow string `json:"attention_window"`
		Head            string `json:"head"`
		Tail            string `json:"tail"`
		CountBackground bool   `json:"count_background"`
	} `json:"options"`
	Range struct {
		From string `json:"from"`
		To   string `json:"to"`
		Days int    `json:"days"`
	} `json:"range"`
	Total struct {
		AttentionMS int64 `json:"attention_ms"`
		ActiveMS    int64 `json:"active_ms"`
		PaddingMS   int64 `json:"padding_ms"`
		Projects    []struct {
			Project     string `json:"project"`
			AttentionMS int64  `json:"attention_ms"`
			WallMS      int64  `json:"wall_ms"`
		} `json:"projects"`
	} `json:"total"`
} {
	t.Helper()
	var out struct {
		Options struct {
			ClusterGap      string `json:"cluster_gap"`
			AttentionWindow string `json:"attention_window"`
			Head            string `json:"head"`
			Tail            string `json:"tail"`
			CountBackground bool   `json:"count_background"`
		} `json:"options"`
		Range struct {
			From string `json:"from"`
			To   string `json:"to"`
			Days int    `json:"days"`
		} `json:"range"`
		Total struct {
			AttentionMS int64 `json:"attention_ms"`
			ActiveMS    int64 `json:"active_ms"`
			PaddingMS   int64 `json:"padding_ms"`
			Projects    []struct {
				Project     string `json:"project"`
				AttentionMS int64  `json:"attention_ms"`
				WallMS      int64  `json:"wall_ms"`
			} `json:"projects"`
		} `json:"total"`
	}
	args := append(reportArgs(t, db, "--json"), extra...)
	if err := json.Unmarshal([]byte(run(t, args...)), &out); err != nil {
		t.Fatalf("report --json did not parse: %v", err)
	}
	return out
}

// A day of the synthetic week is two prompts a minute apart. With the default
// head and tail that is five minutes of attention — two before the first
// prompt, one between them, two after the last — all of it on one project.
func TestReportDay(t *testing.T) {
	db := reportFixture(t)
	got := reportJSON(t, db, "--day=2026-08-26")

	if len(got.Total.Projects) != 1 || got.Total.Projects[0].Project != "widget" {
		t.Fatalf("projects %+v, want one called widget", got.Total.Projects)
	}
	if want := int64(5 * 60 * 1000); got.Total.Projects[0].AttentionMS != want {
		t.Errorf("widget attention %d ms, want %d", got.Total.Projects[0].AttentionMS, want)
	}
	// The wall clock is the minute between the two events and nothing else,
	// which is exactly the number the old version of this test was reading
	// out of the table and calling attention.
	if want := int64(60 * 1000); got.Total.Projects[0].WallMS != want {
		t.Errorf("widget wall %d ms, want %d", got.Total.Projects[0].WallMS, want)
	}
	if got.Total.PaddingMS != int64(4*60*1000) {
		t.Errorf("padding %d ms, want four minutes", got.Total.PaddingMS)
	}

	// And the table names the project and says what it is based on.
	out := run(t, reportArgs(t, db, "--day=2026-08-26")...)
	for _, want := range []string{"day 2026-08-26", "widget", "claude-code 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the day report does not mention %q:\n%s", want, out)
		}
	}
}

// A week is the sum of its days and nothing else. The fixture starts on the
// Tuesday, so the Monday of that week is empty and six days of five minutes
// each are thirty minutes.
func TestReportWeek(t *testing.T) {
	db := reportFixture(t)
	got := reportJSON(t, db, "--week=2026-08-26")

	if got.Range.From != "2026-08-24" || got.Range.To != "2026-08-30" || got.Range.Days != 7 {
		t.Errorf("range %+v, want Monday 2026-08-24 to Sunday 2026-08-30", got.Range)
	}
	if want := int64(6 * 5 * 60 * 1000); got.Total.AttentionMS != want {
		t.Errorf("week attention %d ms, want %d", got.Total.AttentionMS, want)
	}

	out := run(t, reportArgs(t, db, "--week=2026-08-26")...)
	if !strings.Contains(out, "Mon 2026-08-24  attention -") {
		t.Errorf("the empty Monday is missing from the by-day list:\n%s", out)
	}
	if strings.Contains(out, "2026-08-31") {
		t.Errorf("the week ran past its Sunday:\n%s", out)
	}
}

// With no flags at all it reports on today, which for a database of last
// August means saying so rather than printing nothing.
func TestReportDefaultsToToday(t *testing.T) {
	db := reportFixture(t)
	out := run(t, reportArgs(t, db)...)
	if !strings.Contains(out, time.Now().Format(time.DateOnly)) {
		t.Errorf("the default report is not about today:\n%s", out)
	}
}

// The date goes with an equals sign, because --day also works on its own.
// Written with a space it is not a value, and reporting on today instead
// would be the one answer nobody could spot.
func TestADateWrittenWithASpaceIsRefused(t *testing.T) {
	db := reportFixture(t)
	out := fails(t, reportArgs(t, db, "--day", "2026-08-26")...)
	if !strings.Contains(out, "equals sign") {
		t.Errorf("the message does not say how to write it:\n%s", out)
	}
}

func TestDayAndWeekTogetherAreRefused(t *testing.T) {
	db := reportFixture(t)
	if out := fails(t, reportArgs(t, db, "--day", "--week")...); !strings.Contains(out, "pick one") {
		t.Errorf("unhelpful message:\n%s", out)
	}
	if out := fails(t, reportArgs(t, db, "--json", "--table")...); !strings.Contains(out, "pick one") {
		t.Errorf("unhelpful message:\n%s", out)
	}
}

// The invariant, end to end: two runs over the same database produce the same
// JSON byte for byte.
func TestReportJSONIsByteIdentical(t *testing.T) {
	db := reportFixture(t)
	first := run(t, reportArgs(t, db, "--week=2026-08-26", "--json")...)
	for i := 0; i < 5; i++ {
		if again := run(t, reportArgs(t, db, "--week=2026-08-26", "--json")...); again != first {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, first, again)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(first), &parsed); err != nil {
		t.Fatalf("the JSON does not parse: %v", err)
	}
}

// The threshold is reachable without rebuilding. Two events a minute apart
// stop being one block as soon as the gap is smaller than the pause — and
// with nothing added at the ends, that leaves no time at all.
func TestReportThresholdIsAFlag(t *testing.T) {
	db := reportFixture(t)
	bare := []string{"--day=2026-08-26", "--head=0", "--tail=0"}

	whole := reportJSON(t, db, bare...)
	if want := int64(60 * 1000); whole.Total.ActiveMS != want {
		t.Fatalf("active %d ms, want the one minute between the two events", whole.Total.ActiveMS)
	}
	split := reportJSON(t, db, append(bare, "--gap=30s")...)
	if split.Total.ActiveMS != 0 {
		t.Errorf("at a thirty second gap the minute was still counted: %d ms", split.Total.ActiveMS)
	}
}

// The attention window follows the threshold rather than a constant of its
// own, all the way through the command rather than only inside Build. This is
// the path a person actually takes, and it is where the two came apart.
func TestTheWindowFollowsTheGapThroughTheCommand(t *testing.T) {
	db := reportFixture(t)

	if got := reportJSON(t, db, "--day=2026-08-26").Options; got.AttentionWindow != "5m0s" {
		t.Errorf("attention window %q at the default gap, want 5m0s", got.AttentionWindow)
	}
	if got := reportJSON(t, db, "--day=2026-08-26", "--gap=20m").Options; got.AttentionWindow != "10m0s" {
		t.Errorf("attention window %q at a twenty minute gap, want half of it", got.AttentionWindow)
	}
	// Through the config file as well, which is the other way in.
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("report:\n  cluster_gap: 30m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := run(t, "report", "--db", db, "--config", cfg, "--day=2026-08-26", "--json")
	if !strings.Contains(out, `"attention_window": "15m0s"`) {
		t.Errorf("a gap from the config did not move the window:\n%s", out)
	}
	// And an explicit window still wins over both.
	if got := reportJSON(t, db, "--day=2026-08-26", "--gap=20m", "--attention-window=1m").Options; got.AttentionWindow != "1m0s" {
		t.Errorf("attention window %q, want the one that was asked for", got.AttentionWindow)
	}
}

// A flag beats the config, including when it turns something off.
func TestAFlagCanTurnOffAConfigSetting(t *testing.T) {
	db := reportFixture(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("report:\n  count_background: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	on := run(t, "report", "--db", db, "--config", cfg, "--day=2026-08-26", "--json")
	if !strings.Contains(on, `"count_background": true`) {
		t.Errorf("the config setting was not read:\n%s", on)
	}
	off := run(t, "report", "--db", db, "--config", cfg, "--day=2026-08-26", "--json", "--count-background=false")
	if !strings.Contains(off, `"count_background": false`) {
		t.Errorf("--count-background=false could not turn the config setting off:\n%s", off)
	}
}

// Reporting is reading. Pointed at a path with no database, it says so instead
// of leaving an empty one behind and cheerfully finding nothing in it.
func TestReportDoesNotCreateADatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nothing-here.db")
	out := fails(t, "report", "--db", missing,
		"--config", filepath.Join(t.TempDir(), "no-config.yaml"), "--day=2026-08-26")
	if !strings.Contains(out, "spoor ingest") {
		t.Errorf("the message does not say what to do:\n%s", out)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("a report created a database")
	}
}

// --day=false is not "no day": it would report on today, which is the answer
// nobody could tell from the right one.
func TestDayFalseIsRefused(t *testing.T) {
	db := reportFixture(t)
	if out := fails(t, reportArgs(t, db, "--day=false")...); !strings.Contains(out, "YYYY-MM-DD") {
		t.Errorf("unhelpful message:\n%s", out)
	}
}

// A negative duration is a typo, not a way of asking for the default — for
// every one of them, not just the one somebody remembered to test. The config
// refuses the same thing, so the same mistake means the same thing wherever it
// is typed.
func TestNegativeDurationsAreRefused(t *testing.T) {
	db := reportFixture(t)
	for _, flag := range []string{"--gap", "--attention-window", "--head", "--tail", "--min"} {
		out := fails(t, reportArgs(t, db, "--day=2026-08-26", flag+"=-5m")...)
		if !strings.Contains(out, "zero or more") {
			t.Errorf("%s: unhelpful message:\n%s", flag, out)
		}
		if !strings.Contains(out, flag) {
			t.Errorf("%s: the message does not name the flag:\n%s", flag, out)
		}
	}
}

// Reporting reads and never writes. Collecting and reporting being separate
// commands is only worth anything if the second one cannot damage the first.
func TestReportDoesNotChangeTheDatabase(t *testing.T) {
	db := reportFixture(t)
	before, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	run(t, reportArgs(t, db, "--week=2026-08-26")...)
	run(t, reportArgs(t, db, "--week=2026-08-26", "--json")...)
	after, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("the database changed: %d bytes at %s, then %d at %s",
			before.Size(), before.ModTime(), after.Size(), after.ModTime())
	}
}

// withConfig is reportArgs with a config file of the caller's own, which is
// what every attribution test needs: the rules live nowhere else.
func withConfig(t *testing.T, db, body string, extra ...string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return append([]string{"report", "--db", db, "--config", path}, extra...)
}

// The dictionary is read when the report is built, not when events are
// imported. That is what lets a rule written today name what was collected
// last year — and it is the reason collecting and reporting are two commands.
func TestARuleNamesEventsThatWereImportedBeforeIt(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "projects")
	db := filepath.Join(tmp, "spoor.db")
	week(t, src, time.Date(2026, 8, 25, 0, 0, 0, 0, time.Local), 7)
	ingest(t, db, src, "--quiet")

	before := run(t, reportArgs(t, db, "--week=2026-08-26")...)
	if !strings.Contains(before, "widget") {
		t.Fatalf("the import guess is not there to begin with:\n%s", before)
	}

	after := run(t, withConfig(t, db, `
attribution:
  projects:
    - name: renamed-by-a-rule
      paths: /home/u/projects
`, "--week=2026-08-26")...)
	if !strings.Contains(after, "renamed-by-a-rule") {
		t.Errorf("the rule did not reach events imported before it existed:\n%s", after)
	}
	if strings.Contains(after, "widget") {
		t.Errorf("the import guess survived a rule that covers it:\n%s", after)
	}
}

// A line that can never match anything is a warning rather than a failure: one
// unusable rule is not a reason to refuse to report at all, and a rule that
// silently does nothing is the failure this is here to prevent.
func TestAnUnusableRuleIsAWarning(t *testing.T) {
	db := reportFixture(t)
	const broken = `
attribution:
  projects:
    - name: broken
      keys: https://example.com/a
`
	out, errOut := runBoth(t, withConfig(t, db, broken, "--week=2026-08-26")...)
	if !strings.Contains(errOut, "warning: attribution:") {
		t.Errorf("no warning about an unusable rule:\n%s", errOut)
	}
	if !strings.Contains(out, "widget") {
		t.Errorf("the report itself did not run:\n%s", out)
	}
	if strings.Contains(out, "warning") {
		t.Errorf("the warning went to stdout, where the document is:\n%s", out)
	}

	// And the document stays a document. A warning at the top of --json would
	// break every reader of it, including the reproducibility check the README
	// asks people to run.
	asJSON, _ := runBoth(t, withConfig(t, db, broken, "--week=2026-08-26", "--json")...)
	var doc map[string]any
	if err := json.Unmarshal([]byte(asJSON), &doc); err != nil {
		t.Errorf("--json with an unusable rule is not JSON: %v\n%s", err, asJSON)
	}
}

// "How long has this taken" has no range of its own, so it takes the whole
// database. The fixture's week is longer than a day, which is what makes the
// difference visible.
func TestSubjectTakesTheWholeDatabase(t *testing.T) {
	db := reportFixture(t)
	const cfg = `
attribution:
  projects:
    - name: widget
      paths: /home/u/projects/widget
      subjects:
        - name: the feature
          branches: 'main'
`
	out := run(t, withConfig(t, db, cfg, "--subject", "the feature")...)
	if !strings.Contains(out, "the feature") || !strings.Contains(out, "widget") {
		t.Fatalf("the subject view says nothing about the subject:\n%s", out)
	}
	if !strings.Contains(out, "7 days with traces") {
		t.Errorf("the subject did not take the whole database:\n%s", out)
	}

	// And --day narrows it, which is a different and equally reasonable
	// question.
	narrowed := run(t, withConfig(t, db, cfg, "--subject", "the feature", "--day=2026-08-26")...)
	if !strings.Contains(narrowed, "1 day with traces") {
		t.Errorf("--day did not narrow the subject:\n%s", narrowed)
	}
}

func TestSubjectThatIsNotOneSaysSo(t *testing.T) {
	db := reportFixture(t)
	out := run(t, withConfig(t, db, `
attribution:
  projects:
    - name: widget
      paths: /home/u/projects/widget
      subjects:
        - name: the feature
          branches: 'main'
`, "--subject", "a feature nobody has")...)
	if !strings.Contains(out, "nothing") {
		t.Errorf("a subject with no time does not say so:\n%s", out)
	}
	if !strings.Contains(out, "the feature") {
		t.Errorf("it does not say which subjects do have time:\n%s", out)
	}
}

// The maintenance loop: what has no rule yet, busiest first, in the form it is
// written in.
func TestUnmatchedListsWhatNeedsARule(t *testing.T) {
	db := reportFixture(t)
	out := run(t, withConfig(t, db, `
attribution:
  projects:
    - name: something-else
      paths: /nowhere
`, "--unmatched")...)
	if !strings.Contains(out, "/home/u/projects/widget") {
		t.Errorf("the directory with no rule is not listed:\n%s", out)
	}

	covered := run(t, withConfig(t, db, `
attribution:
  projects:
    - name: widget
      paths: /home/u/projects/widget
`, "--unmatched")...)
	if strings.Contains(covered, "/home/u/projects/widget") {
		t.Errorf("a directory a rule covers is still listed:\n%s", covered)
	}
	if !strings.Contains(covered, "covered by a rule") {
		t.Errorf("nothing left to decide, and it does not say so:\n%s", covered)
	}
}

func TestSubjectAndUnmatchedTogetherAreRefused(t *testing.T) {
	db := reportFixture(t)
	out := fails(t, reportArgs(t, db, "--subject", "x", "--unmatched")...)
	if !strings.Contains(out, "pick one") {
		t.Errorf("two views at once were not refused: %s", out)
	}
}

func TestTimelineWithAnotherViewIsRefused(t *testing.T) {
	db := reportFixture(t)
	for _, extra := range [][]string{{"--subject", "x"}, {"--unmatched"}} {
		out := fails(t, reportArgs(t, db, append([]string{"--timeline"}, extra...)...)...)
		if !strings.Contains(out, "does not apply") {
			t.Errorf("--timeline %v was not refused: %s", extra, out)
		}
	}
}

// The flag has to mean the same thing in every view. It used to be printed as
// advice in the subject view — "pass --count-background" — to a reader who had
// just passed it.
func TestCountBackgroundReachesTheSubjectView(t *testing.T) {
	db := reportFixture(t)
	const cfg = `
attribution:
  projects:
    - name: widget
      paths: /home/u/projects/widget
      subjects:
        - name: the feature
          branches: 'main'
`
	off := run(t, withConfig(t, db, cfg, "--subject", "the feature")...)
	if !strings.Contains(off, "pass --count-background") {
		t.Errorf("the subject view does not say the background is left out:\n%s", off)
	}
	on := run(t, withConfig(t, db, cfg, "--subject", "the feature", "--count-background")...)
	if strings.Contains(on, "pass --count-background") {
		t.Errorf("the subject view still advises a flag that was given:\n%s", on)
	}
	if !strings.Contains(on, "counted") {
		t.Errorf("--count-background added no counted line:\n%s", on)
	}
}

// An empty database and a dictionary with no work left in it are the same
// empty list and opposite answers. Both new views have to survive a database
// that has been created and never imported into.
func TestTheNewViewsSurviveAnEmptyDatabase(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "spoor.db")
	src := filepath.Join(tmp, "projects")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	ingest(t, db, src, "--quiet")

	unmatched, errOut := runBoth(t, reportArgs(t, db, "--unmatched")...)
	if !strings.Contains(unmatched, "No traces") {
		t.Errorf("--unmatched on an empty database does not say it is empty:\n%s", unmatched)
	}
	if strings.Contains(unmatched, "covered by a rule") {
		t.Errorf("an empty database reads as a finished dictionary:\n%s", unmatched)
	}
	if !strings.Contains(errOut, "no traces at all") {
		t.Errorf("nothing on stderr about the empty database: %q", errOut)
	}

	// And --json is still a document rather than a sentence.
	asJSON, _ := runBoth(t, reportArgs(t, db, "--unmatched", "--json")...)
	var doc map[string]any
	if err := json.Unmarshal([]byte(asJSON), &doc); err != nil {
		t.Errorf("--unmatched --json on an empty database is not JSON: %v\n%s", err, asJSON)
	}
	subject, _ := runBoth(t, reportArgs(t, db, "--subject", "anything", "--json")...)
	if err := json.Unmarshal([]byte(subject), &doc); err != nil {
		t.Errorf("--subject --json on an empty database is not JSON: %v\n%s", err, subject)
	}
}
