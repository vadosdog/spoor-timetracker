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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every flag of confirm, assign and export, through the flag.
//
// The reason is written down in the roadmap and it is the shape of eight of the
// last stage's defects: a mechanism that looks like it works and quietly does
// nothing, with a test underneath it calling the function the user never
// reaches. So nothing here calls into internal/confirm. Everything goes through
// Run, the way it does for somebody at a keyboard.

// theDay is the day every test here works on: yesterday, so it is closed.
func theDay() time.Time { return time.Now().AddDate(0, 0, -1) }

// aDay builds a database holding one day with two blocks: work in a directory
// no rule mentions, and browsing on a host no rule mentions.
func aDay(t *testing.T) (db, cfg string, date time.Time) {
	t.Helper()
	dir := t.TempDir()
	db = filepath.Join(dir, "spoor.db")
	date = theDay()

	src := filepath.Join(dir, "projects", "-home-u-src-widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	at := func(h, m int) string {
		return time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, time.Local).
			UTC().Format("2006-01-02T15:04:05.000Z")
	}
	for i, clock := range [][2]int{{9, 0}, {9, 5}, {9, 9}} {
		fmt.Fprintf(&b, `{"type":"user","uuid":"p%d","timestamp":"%s","sessionId":"s-1",`+
			`"cwd":"/home/u/src/widget","gitBranch":"main","entrypoint":"cli",`+
			`"isSidechain":false,"userType":"external","version":"2.1.219",`+
			`"message":{"content":"x"}}`+"\n", i, at(clock[0], clock[1]))
	}
	if err := os.WriteFile(filepath.Join(src, "session.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Browsing: three visits to one host on three first path segments, so that
	// the folding by host has something to fold.
	var visits [][2]string
	for i, segment := range []string{"alpha", "beta", "gamma"} {
		when := time.Date(date.Year(), date.Month(), date.Day(), 11, i*20, 0, 0, time.Local)
		visits = append(visits, [2]string{
			when.Format(time.RFC3339),
			"https://ops.example.invalid/" + segment + "/detail",
		})
	}
	history := chromeHistory(t, filepath.Join(dir, "chrome"), visits...)
	cfg = writeConfig(t, dir, "attribution:\n  fallback: none\n")

	run(t, "ingest", "--db", db, "--claude-dir", filepath.Join(dir, "projects"),
		"--config", cfg, "--browser-history", history)
	return db, cfg, date
}

// The whole queue as a document, which is what --questions is for: something a
// program can read, and something to look at when the terminal seems wrong.
func TestQuestionsPrintTheQueueWithWhatItCountsForNow(t *testing.T) {
	db, cfg, date := aDay(t)
	out := run(t, "confirm", "--db", db, "--config", cfg,
		"--day="+date.Format(time.DateOnly), "--questions")

	if !strings.Contains(out, "path:/home/u/src/widget") {
		t.Errorf("the directory is not in the queue:\n%s", out)
	}
	if !strings.Contains(out, "key:ops.example.invalid") {
		t.Errorf("the host is not in the queue:\n%s", out)
	}
	// The column that says where the trace is leaking to now. Without it a key
	// inherited correctly and a key taken by the busiest project nearby look
	// the same, and they are not equally urgent.
	if !strings.Contains(out, "called ") {
		t.Errorf("nothing says what the trace counts for now:\n%s", out)
	}
}

// Fifteen segments of one host are one question, not fifteen. This is the
// difference between closing a day in two minutes and in twenty.
func TestSegmentsOfOneHostAreOneQuestion(t *testing.T) {
	db, cfg, date := aDay(t)
	doc := questionsJSON(t, db, cfg, date)

	var keys []string
	for _, q := range doc.Queue {
		if strings.HasPrefix(q.Trace, "key:") {
			keys = append(keys, q.Trace)
		}
	}
	if len(keys) != 1 {
		t.Fatalf("the three segments of one host became %d questions: %v", len(keys), keys)
	}
	if keys[0] != "key:ops.example.invalid" {
		t.Errorf("the question is about %q, want the bare host", keys[0])
	}
	for _, q := range doc.Queue {
		if q.Trace == keys[0] && len(q.Folded) < 2 {
			t.Errorf("the question does not say what it stands for: %v", q.Folded)
		}
	}
}

// The main test of the whole stage: an answer becomes a rule, and the same
// question does not come back. Not a promise — a mechanism, so it is checked
// on the same data.
func TestAnsweringWritesARuleAndTheQuestionIsGone(t *testing.T) {
	db, cfg, date := aDay(t)
	before := questionsJSON(t, db, cfg, date)

	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "path:/home/u/src/widget", "--project", "Widgets")

	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/home/u/src/widget") {
		t.Fatalf("the rule was not written to the config:\n%s", body)
	}

	after := questionsJSON(t, db, cfg, date)
	if len(after.Queue) >= len(before.Queue) {
		t.Errorf("the queue did not shrink: %d before, %d after", len(before.Queue), len(after.Queue))
	}
	for _, q := range after.Queue {
		if q.Trace == "path:/home/u/src/widget" {
			t.Error("the question came back after being answered")
		}
	}
	// And the day now says so.
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly))
	if !strings.Contains(out, "Widgets") {
		t.Errorf("the report does not use the new rule:\n%s", out)
	}
}

// The trash answer. Not a project and not a subject is an ordinary answer, and
// it has to cost one flag the way it costs one key.
func TestNeverIsAnAnswerAndItSticks(t *testing.T) {
	db, cfg, date := aDay(t)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "key:ops.example.invalid", "--never")

	body, _ := os.ReadFile(cfg)
	if !strings.Contains(string(body), "never") || !strings.Contains(string(body), "ops.example.invalid") {
		t.Fatalf("never was not written:\n%s", body)
	}
	for _, q := range questionsJSON(t, db, cfg, date).Queue {
		if strings.HasPrefix(q.Trace, "key:ops") {
			t.Error("a trace decided about is still being asked about")
		}
	}
}

// The other decision about a trace: it names a subject, and never the same one
// twice. Decided once, and then never asked again.
func TestAmbiguousIsDecidedOnceAndStops(t *testing.T) {
	db, cfg, date := aDay(t)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "key:ops.example.invalid", "--ambiguous")

	body, _ := os.ReadFile(cfg)
	if !strings.Contains(string(body), "ambiguous") {
		t.Fatalf("ambiguous was not written:\n%s", body)
	}
	for _, q := range questionsJSON(t, db, cfg, date).Queue {
		if strings.HasPrefix(q.Trace, "key:ops") {
			t.Error("a trace decided to be ambiguous is still in the queue")
		}
	}
}

// A rule from today. The past keeps the name it had, which is the only honest
// way to correct a name that used to be right.
func TestARuleFromTodayLeavesYesterdayAlone(t *testing.T) {
	db, cfg, date := aDay(t)
	// The same directory, worked in the day before as well. Without this the
	// earlier day is empty and the test would pass by having nothing to
	// rename, which is the shape of test this stage exists to stop writing.
	earlierEvents(t, db, cfg, date.AddDate(0, 0, -1))

	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "path:/home/u/src/widget", "--project", "Widgets", "--since")

	body, _ := os.ReadFile(cfg)
	if !strings.Contains(string(body), "since: "+date.Format(time.DateOnly)) {
		t.Fatalf("the rule was written without its start date:\n%s", body)
	}
	// The day it starts on has the name.
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly))
	if !strings.Contains(out, "Widgets") {
		t.Errorf("the dated rule did not apply on the day it starts:\n%s", out)
	}
	// The day before does not, and this is the point of the whole third
	// answer: an undated rule would have renamed it too.
	earlier := date.AddDate(0, 0, -1)
	out = run(t, "report", "--db", db, "--config", cfg, "--day="+earlier.Format(time.DateOnly))
	if strings.Contains(out, "Widgets") {
		t.Errorf("the dated rule reached back before its own date:\n%s", out)
	}
	if strings.Contains(out, "No traces") {
		t.Fatalf("the earlier day holds nothing, so this test proves nothing:\n%s", out)
	}

	// And an undated rule does reach back, which is what makes the two
	// answers different rather than one with a decoration on it.
	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "path:/home/u/src/widget/deep", "--project", "Widgets")
	out = run(t, "report", "--db", db, "--config", cfg, "--day="+earlier.Format(time.DateOnly))
	if !strings.Contains(out, "Widgets") {
		t.Errorf("an undated rule did not rename the earlier day:\n%s", out)
	}
}

// earlierEvents adds work in the same directory on another day, so that a
// dated rule has a past to leave alone.
func earlierEvents(t *testing.T, db, cfg string, date time.Time) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "-home-u-src-widget-deep")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 3; i++ {
		ts := time.Date(date.Year(), date.Month(), date.Day(), 10, i*4, 0, 0, time.Local).
			UTC().Format("2006-01-02T15:04:05.000Z")
		fmt.Fprintf(&b, `{"type":"user","uuid":"before-%d","timestamp":"%s","sessionId":"s-0",`+
			`"cwd":"/home/u/src/widget/deep","gitBranch":"main","entrypoint":"cli",`+
			`"isSidechain":false,"userType":"external","version":"2.1.219",`+
			`"message":{"content":"x"}}`+"\n", i, ts)
	}
	if err := os.WriteFile(filepath.Join(src, "s.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "ingest", "--db", db, "--claude-dir", dir, "--config", cfg, "--no-browser")
}

// An answer no rule could carry. It changes this day, is marked, and is
// counted — hand marking nobody can see is hand marking nobody revisits.
func TestAOneOffChangesTheDayAndIsCounted(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)

	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--from", "09:00", "--to", "09:30", "--project", "Rescued", "--one-off")

	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day)
	if !strings.Contains(out, "Rescued") {
		t.Fatalf("the one-off is not in the report:\n%s", out)
	}
	// It is not a rule: nothing was written to the config.
	body, _ := os.ReadFile(cfg)
	if strings.Contains(string(body), "Rescued") {
		t.Errorf("a one-off was written to the config:\n%s", body)
	}
	out = run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	if !strings.Contains(out, "could not become rules") {
		t.Errorf("the count of hand marking was not printed:\n%s", out)
	}
}

// An answer about a stretch that refuses to become a rule silently would be
// the worst of both: refused with the reason instead.
func TestAStretchAnswerMustSayItIsAOneOff(t *testing.T) {
	db, cfg, date := aDay(t)
	var out, errOut bytes.Buffer
	code := Run([]string{"assign", "--db", db, "--config", cfg,
		"--day=" + date.Format(time.DateOnly),
		"--from", "09:00", "--to", "09:30", "--project", "X"}, &out, &errOut)
	if code == 0 {
		t.Fatal("an answer about a stretch was accepted without --one-off")
	}
	if !strings.Contains(errOut.String(), "one-off") {
		t.Errorf("the message does not say what to add: %s", errOut.String())
	}
}

// A rule on the page title, which is the answer that exists because a question
// about a key does not always have a key answer.
func TestARuleCanBeWrittenOnTheTitleInstead(t *testing.T) {
	db, cfg, date := aDay(t)
	out, errOut := runBoth(t, "assign", "--db", db, "--config", cfg,
		"--day="+date.Format(time.DateOnly),
		"--trace", "key:ops.example.invalid", "--project", "Widgets", "--title", `Widget \w+`)

	body, _ := os.ReadFile(cfg)
	if !strings.Contains(string(body), "titles") {
		t.Fatalf("no title rule was written:\n%s", body)
	}
	if strings.Contains(string(body), "keys") && strings.Contains(string(body), "ops.example.invalid") {
		t.Error("a key rule was written as well; the two can disagree")
	}
	if !strings.Contains(errOut, "every host") {
		t.Errorf("nothing warned that a title rule is not scoped to a host: %s%s", out, errOut)
	}
}

// Nothing is written until it has been shown. --dry-run is that promise as a
// flag; in the terminal it is the line above the answer.
func TestDryRunShowsTheLineAndWritesNothing(t *testing.T) {
	db, cfg, date := aDay(t)
	before, _ := os.ReadFile(cfg)
	out := run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "path:/home/u/src/widget", "--project", "Widgets", "--dry-run")
	if !strings.Contains(out, "/home/u/src/widget") {
		t.Errorf("the line to be written was not shown:\n%s", out)
	}
	after, _ := os.ReadFile(cfg)
	if string(before) != string(after) {
		t.Error("--dry-run wrote to the config")
	}
}

// Confirming freezes the result. The test the roadmap asks for by name: a
// second import does not move a day somebody has already agreed with.
func TestAConfirmedDayDoesNotMoveWhenMoreEventsArrive(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	before := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")

	// More events land in the day that is already confirmed.
	dir := t.TempDir()
	src := filepath.Join(dir, "-home-u-src-other")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 4; i++ {
		ts := time.Date(date.Year(), date.Month(), date.Day(), 15, i*3, 0, 0, time.Local).
			UTC().Format("2006-01-02T15:04:05.000Z")
		fmt.Fprintf(&b, `{"type":"user","uuid":"late-%d","timestamp":"%s","sessionId":"s-2",`+
			`"cwd":"/home/u/src/other","gitBranch":"main","entrypoint":"cli",`+
			`"isSidechain":false,"userType":"external","version":"2.1.219",`+
			`"message":{"content":"x"}}`+"\n", i, ts)
	}
	if err := os.WriteFile(filepath.Join(src, "s.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "ingest", "--db", db, "--claude-dir", dir, "--config", cfg, "--no-browser")

	after := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
	if before != after {
		t.Errorf("a confirmed day moved after an import:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// And a rule written afterwards does not move it either.
	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.AddDate(0, 0, 1).Format(time.DateOnly),
		"--trace", "path:/home/u/src/other", "--project", "Other")
	if got := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json"); got != after {
		t.Error("a confirmed day moved when a rule was written")
	}
}

// The rules moving under a frozen day is the one thing it cannot notice for
// itself, so it has to be said out loud — and --recompute is what shows the
// difference without writing anything.
func TestAConfirmedDaySaysWhenTheRulesHaveMoved(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	run(t, "assign", "--db", db, "--config", cfg, "--day="+date.AddDate(0, 0, 1).Format(time.DateOnly),
		"--trace", "path:/home/u/src/widget", "--project", "Widgets")

	frozen, warned := runBoth(t, "report", "--db", db, "--config", cfg, "--day="+day)
	if !strings.Contains(warned, "rules have changed") {
		t.Errorf("nothing said the rules had moved: %s", warned)
	}
	if strings.Contains(frozen, "Widgets") {
		t.Error("the frozen day was rewritten by a later rule")
	}
	fresh, _ := runBoth(t, "report", "--db", db, "--config", cfg, "--day="+day, "--recompute")
	if !strings.Contains(fresh, "Widgets") {
		t.Errorf("--recompute did not show what the rules would say:\n%s", fresh)
	}
	// And --recompute wrote nothing: the frozen day is still frozen.
	if got, _ := runBoth(t, "report", "--db", db, "--config", cfg, "--day="+day); got != frozen {
		t.Error("--recompute changed the stored day")
	}

	list := run(t, "confirm", "--db", db, "--config", cfg, "--list")
	if !strings.Contains(list, day) || !strings.Contains(list, "rules have changed") {
		t.Errorf("--list does not say which days have moved:\n%s", list)
	}
}

// Reopening a day is the only way to change one, and it has to actually work.
func TestReconfirmReopensADay(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	var out, errOut bytes.Buffer
	if code := Run([]string{"confirm", "--db", db, "--config", cfg, "--day=" + day, "--yes"}, &out, &errOut); code == 0 {
		t.Fatal("a confirmed day was confirmed again without --reconfirm")
	}
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--reconfirm", "--yes")
}

// Export. A confirmed day, byte for byte the same twice, with no trace of a
// trace anywhere in it.
func TestExportIsTheSameTwiceAndNamesNoTrace(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "path:/home/u/src/widget", "--project", "Widgets")
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	first := run(t, "export", "--db", db, "--config", cfg, "--day="+day)
	second := run(t, "export", "--db", db, "--config", cfg, "--day="+day)
	if first != second {
		t.Error("two exports of one day are not the same bytes")
	}
	if !strings.Contains(first, "# "+day) || !strings.Contains(first, "Widgets") {
		t.Errorf("the export is missing its day or its projects:\n%s", first)
	}
	// No host, no directory, no page title. This is the first thing that
	// leaves the tool, and a page title is sometimes a search query.
	for _, trace := range []string{"ops.example.invalid", "/home/u/src/widget", "Widget alpha"} {
		if strings.Contains(first, trace) {
			t.Errorf("the export names a trace: %q\n%s", trace, first)
		}
	}
	if !strings.Contains(first, "Background") {
		t.Errorf("the background line is missing:\n%s", first)
	}

	// To a file, and with the schedule.
	out := filepath.Join(t.TempDir(), "day.md")
	run(t, "export", "--db", db, "--config", cfg, "--day="+day, "--out", out, "--timeline")
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Timeline") {
		t.Errorf("--timeline added no schedule:\n%s", body)
	}
	if !strings.Contains(string(body), "rule") {
		t.Errorf("the schedule does not say what each stretch rests on:\n%s", body)
	}
}

// An unconfirmed day is exported only when asked, and says so on the page: it
// is still computed from the rules and will move when one of them does.
func TestAnUnconfirmedDayIsExportedOnlyWithTheFlag(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)

	var out, errOut bytes.Buffer
	if code := Run([]string{"export", "--db", db, "--config", cfg, "--day=" + day}, &out, &errOut); code == 0 {
		t.Fatal("an unconfirmed day was exported without being asked for")
	}
	got := run(t, "export", "--db", db, "--config", cfg, "--day="+day, "--unconfirmed")
	if !strings.Contains(got, "Not confirmed") {
		t.Errorf("the page does not say it is provisional:\n%s", got)
	}
}

// --questions --json is a document, so it is byte for byte the same twice and
// does not depend on the order events came out of the database.
func TestTheQueueAsJSONIsTheSameTwice(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	first := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--questions", "--json")
	second := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--questions", "--json")
	if first != second {
		t.Error("two runs of --questions --json disagree")
	}
	var doc struct {
		Day   string `json:"day"`
		Queue []struct {
			Trace   string `json:"trace"`
			Project []struct {
				Name string `json:"name"`
				Why  string `json:"why"`
			} `json:"project"`
		} `json:"queue"`
		Blocks []struct {
			From   string `json:"from"`
			Ground string `json:"ground"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal([]byte(first), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, first)
	}
	if doc.Day != day {
		t.Errorf("the document is about %q", doc.Day)
	}
	// Every block of the day is in it, named or not: the queue is a shortcut,
	// not the only way in.
	if len(doc.Blocks) == 0 {
		t.Error("the document holds no blocks, so nothing else can be edited")
	}
	// And a candidate says where it came from, because a proposal with no
	// grounds is a guess.
	for _, q := range doc.Queue {
		for _, c := range q.Project {
			if c.Why == "" {
				t.Errorf("candidate %q on %q says nothing about where it came from", c.Name, q.Trace)
			}
		}
	}
}

// The terminal refuses to start where there is no terminal, rather than
// looking as if it has hung.
func TestConfirmWithoutATerminalSaysSo(t *testing.T) {
	db, cfg, date := aDay(t)
	var out, errOut bytes.Buffer
	code := Run([]string{"confirm", "--db", db, "--config", cfg,
		"--day=" + date.Format(time.DateOnly)}, &out, &errOut)
	if code == 0 {
		t.Fatal("the terminal interface started with no terminal")
	}
	if !strings.Contains(errOut.String(), "--questions") {
		t.Errorf("the message does not say what to do instead: %s", errOut.String())
	}
}

// A rule that can never fire is the worst outcome there is, because whoever
// wrote it now believes the question is shut.
func TestARuleAlreadyRefusedIsWarnedAbout(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--never")

	_, errOut := runBoth(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--project", "Widgets", "--dry-run")
	if !strings.Contains(errOut, "never fire") {
		t.Errorf("nothing warned about a rule that cannot fire: %s", errOut)
	}
}

// A rule that changed nothing is a rule that is either shadowed or written on
// a trace nobody touches. Both look exactly like a rule that worked.
func TestARuleThatMovedNoTimeSaysSo(t *testing.T) {
	db, cfg, date := aDay(t)
	out := run(t, "assign", "--db", db, "--config", cfg, "--day="+date.Format(time.DateOnly),
		"--trace", "path:/home/u/nowhere", "--project", "Nothing")
	if !strings.Contains(out, "no time moved") {
		t.Errorf("a rule that did nothing reported success:\n%s", out)
	}
}

func questionsJSON(t *testing.T, db, cfg string, date time.Time) struct {
	Queue []struct {
		Trace     string   `json:"trace"`
		Folded    []string `json:"folded"`
		CalledNow string   `json:"called_now"`
		Touches   int      `json:"touches"`
	} `json:"queue"`
} {
	t.Helper()
	var doc struct {
		Queue []struct {
			Trace     string   `json:"trace"`
			Folded    []string `json:"folded"`
			CalledNow string   `json:"called_now"`
			Touches   int      `json:"touches"`
		} `json:"queue"`
	}
	out := run(t, "confirm", "--db", db, "--config", cfg,
		"--day="+date.Format(time.DateOnly), "--questions", "--json")
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return doc
}

// A refusal is not an answer. The one code path in the program that deletes
// events must not read "spoor could not understand this feed" as "the meetings
// are gone" — the day a provider changes a repetition rule would otherwise be
// the day a year of meetings is deleted while ingest exits 0.
func TestAFeedSpoorCannotReadDoesNotEraseTheCalendar(t *testing.T) {
	db, cfg, feed, date := calendarSetup(t)
	writeFeed(t, feed, vevent("keep@example.invalid", date, 12, 60))
	ingestFeed(t, db, cfg)

	// A rule this parser refuses to guess at: the event is readable, the
	// series is not expanded, and no meeting comes out of the whole feed.
	writeFeed(t, feed, vevent("keep@example.invalid", date, 12, 60,
		"RRULE:FREQ=MONTHLY;BYSETPOS=2;BYDAY=MO\r\n"))
	out := ingestFeed(t, db, cfg)

	if n := countOf(t, db, "calendar"); n != 1 {
		t.Errorf("%d calendar events after a feed spoor could not expand, want 1", n)
	}
	if !strings.Contains(out, "nothing was removed") {
		t.Errorf("the refusal was silent:\n%s", out)
	}
}

// An answer can name a subject no trace in the day carries — that is the whole
// point of an answer no rule could have been written for. The time was being
// computed and then dropped, which is the shape this project is built against.
func TestAnAssignedSubjectKeepsItsTime(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "path:/home/u/src/widget", "--project", "Widgets")
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--from", "09:00", "--to", "09:08", "--subject", "release-9", "--one-off")

	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day)
	if !strings.Contains(out, "release-9") {
		t.Fatalf("the assigned subject holds no time:\n%s", out)
	}
	// And it survives being frozen.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	if got := run(t, "export", "--db", db, "--config", cfg, "--day="+day); !strings.Contains(got, "release-9") {
		t.Errorf("the assigned subject is not in the confirmed day:\n%s", got)
	}
}

// Two answers starting at the same minute and ending at different ones are
// somebody widening a correction. The day still has to be freezable.
func TestTwoAnswersStartingAtTheSameMinuteStillConfirm(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	for _, to := range []string{"09:05", "09:08"} {
		run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
			"--from", "09:00", "--to", to, "--project", "Rescued", "--one-off")
	}
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
}

// A dated rule is not refused by an undated never entry — it beats one. Warning
// about it would be a false alarm, and a warning people learn to ignore is
// worse than no warning.
func TestADatedRuleIsNotWarnedAboutAsUnreachable(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--never")

	_, errOut := runBoth(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--project", "Ops", "--since", "--dry-run")
	if strings.Contains(errOut, "never fire") {
		t.Errorf("a dated rule was called unreachable: %s", errOut)
	}
}

// --json prints a document, so a run where it does nothing has to say why
// rather than quietly printing a sentence to something expecting JSON.
func TestJSONWithoutQuestionsIsRefused(t *testing.T) {
	db, cfg, date := aDay(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"confirm", "--db", db, "--config", cfg,
		"--day=" + date.Format(time.DateOnly), "--yes", "--json"}, &out, &errOut); code == 0 {
		t.Fatal("--json was accepted where it does nothing")
	}
}

// --never and --ambiguous are decisions about a trace. Accepted and ignored
// beside --from/--to, they would read as "I said this names nothing and spoor
// agreed".
func TestDecisionsAboutATraceAreRefusedOnAStretch(t *testing.T) {
	db, cfg, date := aDay(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"assign", "--db", db, "--config", cfg,
		"--day=" + date.Format(time.DateOnly),
		"--from", "09:00", "--to", "09:05", "--never", "--one-off"}, &out, &errOut); code == 0 {
		t.Fatal("--never was accepted on a stretch of a day")
	}
}

// A week is the number somebody files. It has to say the same thing about a
// Tuesday as the Tuesday did.
func TestARangeAppliesWhatWasSaidAboutItsDays(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--from", "09:00", "--to", "09:08", "--project", "Rescued", "--one-off")

	out, _ := runBoth(t, "report", "--db", db, "--config", cfg, "--week="+day)
	if !strings.Contains(out, "Rescued") {
		t.Errorf("a week ignored what was said about one of its days:\n%s", out)
	}
	// And a confirmed day inside a range is recomputed, out loud.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	_, errOut := runBoth(t, "report", "--db", db, "--config", cfg, "--week="+day)
	if !strings.Contains(errOut, "recomputed here anyway") {
		t.Errorf("a range recomputed a confirmed day in silence: %s", errOut)
	}
}

// The pauses between blocks, and the one question about them. Through the
// flags, because the screen and the flags have to be the same thing — and
// because this one is a measuring instrument, so what matters is that the
// answer is stored and that nothing else moves.
func TestAPauseCanBeAnsweredAndChangesNoNumber(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)

	list := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows")
	if !strings.Contains(list, "pauses between blocks") {
		t.Fatalf("the pauses were not listed:\n%s", list)
	}
	if !strings.Contains(list, "change no measured number") {
		t.Errorf("the list does not say what answering it does:\n%s", list)
	}

	var doc []struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Answered bool   `json:"answered"`
		Worked   bool   `json:"worked"`
	}
	raw := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	if len(doc) == 0 {
		t.Fatal("this day has no pauses, so the test proves nothing")
	}

	measured := func() (attention, background, active, span int64) {
		t.Helper()
		var d struct {
			Total struct {
				AttentionMS  int64 `json:"attention_ms"`
				BackgroundMS int64 `json:"background_ms"`
				ActiveMS     int64 `json:"active_ms"`
				SpanMS       int64 `json:"span_ms"`
				ClaimedMS    int64 `json:"claimed_pauses_ms"`
			} `json:"total"`
		}
		raw := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatalf("not JSON: %v", err)
		}
		return d.Total.AttentionMS, d.Total.BackgroundMS, d.Total.ActiveMS, d.Total.SpanMS
	}
	wasAttention, wasBackground, wasActive, wasSpan := measured()
	at, _ := time.Parse(time.RFC3339, doc[0].From)
	until, _ := time.Parse(time.RFC3339, doc[0].To)
	spec := at.Local().Format("15:04") + "-" + until.Local().Format("15:04")
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--window", spec, "--worked", "true")

	raw = run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc[0].Answered || !doc[0].Worked {
		t.Errorf("the answer was not recorded: %+v", doc[0])
	}
	// Every measured number is the number it was. What the answer buys is a
	// line of its own, exactly as the background line works: the day measured
	// nothing in a pause, and one total made of a measurement and an answer is
	// a total nobody can check.
	attention, background, active, span := measured()
	if attention != wasAttention || background != wasBackground ||
		active != wasActive || span != wasSpan {
		t.Errorf("answering a pause moved a measured number: %d/%d/%d/%d became %d/%d/%d/%d",
			wasAttention, wasBackground, wasActive, wasSpan,
			attention, background, active, span)
	}
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day)
	if !strings.Contains(out, "pauses you called work") {
		t.Errorf("the answer bought no line of its own:\n%s", out)
	}
	if !strings.Contains(out, "not part of active") {
		t.Errorf("the line does not say it is outside the day:\n%s", out)
	}
	// And it survives being frozen, or a confirmed day would lose it.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	if got := run(t, "export", "--db", db, "--config", cfg, "--day="+day); !strings.Contains(got, "Pauses called work") {
		t.Errorf("the confirmed day lost the line:\n%s", got)
	}
}

// A pause spoor does not know about is time nothing recorded. Refused rather
// than invented, which is where typing intervals by hand would start.
func TestAPauseThatIsNotThereIsRefused(t *testing.T) {
	db, cfg, date := aDay(t)
	var out, errOut bytes.Buffer
	if code := Run([]string{"confirm", "--db", db, "--config", cfg,
		"--day=" + date.Format(time.DateOnly),
		"--window", "03:00-04:00", "--worked", "true"}, &out, &errOut); code == 0 {
		t.Fatal("a pause that does not exist was accepted")
	}
	if !strings.Contains(errOut.String(), "--windows") {
		t.Errorf("the message does not say how to find the real ones: %s", errOut.String())
	}
}

// A rule under a subject alone does nothing: subjects are only consulted once
// a project has matched, so a key filed under the subject and nowhere else
// names neither. The question then comes back tomorrow, and the day after,
// looking exactly like a mechanism that is broken — which is how this was
// found, by somebody marking the same host over and over.
func TestNamingASubjectAlsoNamesTheProject(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	// A project that exists and does not match the host — the ordinary case
	// when a new service turns up inside work you already have a name for.
	if err := os.WriteFile(cfg, []byte(
		"attribution:\n  fallback: none\n  projects:\n    - name: Clips\n      paths: /home/u/elsewhere\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--project", "Clips", "--subject", "clip 1")

	for _, q := range questionsJSON(t, db, cfg, date).Queue {
		if strings.HasPrefix(q.Trace, "key:ops") {
			t.Fatal("the question came back after being answered")
		}
	}
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day)
	if !strings.Contains(out, "Clips") {
		t.Errorf("the project does not name the trace:\n%s", out)
	}
	if !strings.Contains(out, "clip 1") {
		t.Errorf("the subject holds no time:\n%s", out)
	}
	// And the project rule is not written twice when it already names it.
	before, _ := os.ReadFile(cfg)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--subject", "clip 2", "--project", "Clips")
	after, _ := os.ReadFile(cfg)
	if strings.Count(string(after), "- ops.example.invalid") != strings.Count(string(before), "- ops.example.invalid")+1 {
		t.Errorf("the project rule was written again:\n%s", after)
	}
}

// The pauses are answerable on a day that is already frozen. Nothing they
// record is a measurement, so locking them behind --reconfirm would mean
// throwing a snapshot away to answer a question that does not touch it.
func TestPausesCanBeAnsweredOnAConfirmedDay(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	frozen := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")

	list := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows")
	if !strings.Contains(list, "pauses between blocks") {
		t.Fatalf("a confirmed day would not list its pauses:\n%s", list)
	}
	var doc []struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	raw := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) == 0 {
		t.Fatalf("no pauses to answer: %v\n%s", err, raw)
	}
	at, _ := time.Parse(time.RFC3339, doc[0].From)
	until, _ := time.Parse(time.RFC3339, doc[0].To)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", at.Local().Format("15:04")+"-"+until.Local().Format("15:04"), "--worked", "true")

	// The frozen day gains the line and keeps everything it measured.
	after := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
	if after == frozen {
		t.Error("the answer did not reach the confirmed day at all")
	}
	for _, field := range []string{`"attention_ms"`, `"active_ms"`, `"span_ms"`} {
		if valueOf(t, frozen, field) != valueOf(t, after, field) {
			t.Errorf("answering a pause moved %s on a confirmed day", field)
		}
	}
	if !strings.Contains(run(t, "export", "--db", db, "--config", cfg, "--day="+day), "Pauses called work") {
		t.Error("the frozen day's export lost the line")
	}
}

// valueOf pulls one number out of a JSON document by its key, for comparing
// two documents field by field.
func valueOf(t *testing.T, doc, field string) string {
	t.Helper()
	i := strings.Index(doc, field)
	if i < 0 {
		t.Fatalf("no %s in the document", field)
	}
	rest := doc[i+len(field):]
	end := strings.IndexAny(rest, ",\n}")
	return strings.TrimSpace(rest[:end])
}

// A pause attached to a project has to survive being frozen, and an answer
// given after the day was frozen has to reach the rows as well as the total.
// A snapshot whose total is refreshed and whose rows are not is a snapshot
// that stops adding up to itself.
func TestAConfirmedDayKeepsWhatAPauseWasAttachedTo(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "path:/home/u/src/widget", "--project", "Widgets")

	var doc []struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	raw := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) == 0 {
		t.Fatalf("no pauses: %v", err)
	}
	spec := func(i int) string {
		at, _ := time.Parse(time.RFC3339, doc[i].From)
		until, _ := time.Parse(time.RFC3339, doc[i].To)
		return at.Local().Format("15:04") + "-" + until.Local().Format("15:04")
	}

	// One answer before confirming, one after.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", spec(0), "--worked", "true", "--window-project", "Widgets")
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	if len(doc) > 1 {
		run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
			"--window", spec(1), "--worked", "true", "--window-project", "Widgets")
	}

	// The project it was attached to carries it, in the frozen day.
	var got struct {
		Total struct {
			ClaimedMS int64 `json:"claimed_pauses_ms"`
		} `json:"total"`
		Days []struct {
			Projects []struct {
				Project   string `json:"project"`
				ClaimedMS int64  `json:"claimed_pauses_ms"`
			} `json:"projects"`
		} `json:"days"`
	}
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	var perProject int64
	for _, p := range got.Days[0].Projects {
		if p.Project == "Widgets" {
			perProject = p.ClaimedMS
		}
	}
	if perProject == 0 {
		t.Errorf("the confirmed day lost which project the pause went to:\n%s", out)
	}
	if perProject != got.Total.ClaimedMS {
		t.Errorf("the rows hold %d and the total says %d — the snapshot does not add up",
			perProject, got.Total.ClaimedMS)
	}
	if !strings.Contains(run(t, "export", "--db", db, "--config", cfg, "--day="+day), "Pauses") {
		t.Error("the export does not say where the pauses went")
	}
}

// A range that a dated rule begins inside holds two dictionaries, and the
// table under it looks like one. The line saying so was built and never
// called: the mechanism against exactly this failure was itself the failure.
func TestARangeSaysWhenTheRulesChangeInsideIt(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "path:/home/u/src/widget", "--project", "Widgets", "--since")

	// A week whose Monday is the day the rule starts says nothing: the rule
	// covers the whole of it. A week that the day falls inside does.
	_, errOut := runBoth(t, "report", "--db", db, "--config", cfg, "--week="+day)
	inside := !strings.HasPrefix(errOut, "")
	_ = inside
	monday := date.AddDate(0, 0, -(int(date.Weekday())+6)%7)
	if monday.Format(time.DateOnly) == day {
		if strings.Contains(errOut, "rules change inside this range") {
			t.Errorf("a rule starting on the first day of the range warned anyway: %s", errOut)
		}
	} else if !strings.Contains(errOut, "rules change inside this range") {
		t.Errorf("a week holding two dictionaries said nothing: %s", errOut)
	}
	// A range the rule does not start inside says nothing.
	earlier := date.AddDate(0, 0, -30).Format(time.DateOnly)
	if _, quiet := runBoth(t, "report", "--db", db, "--config", cfg, "--week="+earlier); strings.Contains(quiet, "rules change") {
		t.Errorf("a range with no rule starting in it warned anyway: %s", quiet)
	}
	// And the day the rule was written on does not warn about itself.
	if _, quiet := runBoth(t, "report", "--db", db, "--config", cfg, "--day="+day); strings.Contains(quiet, "rules change") {
		t.Errorf("the day a dated rule starts on warned about itself: %s", quiet)
	}
}

// A frozen day keeps its pauses on the schedule. Stretches cover the blocks
// and nothing else, so a day rebuilt from them has no gaps in it at all — and
// the summary would print "pauses you called work" over a schedule showing a
// solid afternoon.
func TestAConfirmedDayStillShowsItsPauses(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	var doc []struct{ From, To string }
	raw := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) == 0 {
		t.Fatalf("no pauses: %v", err)
	}
	at, _ := time.Parse(time.RFC3339, doc[0].From)
	until, _ := time.Parse(time.RFC3339, doc[0].To)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", at.Local().Format("15:04")+"-"+until.Local().Format("15:04"), "--worked", "true")
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--timeline")
	if !strings.Contains(out, "pauses you called work") {
		t.Fatalf("the summary lost the line:\n%s", out)
	}
	if !strings.Contains(out, "you called this work") {
		t.Errorf("the schedule of a frozen day hides the pause it says exists:\n%s", out)
	}
}

// The day's own numbers and the day inside the report agree. A struct copied
// into the report and then assigned to left the two disagreeing, and only
// --json showed it.
func TestAFrozenDayAgreesWithItsOwnTotals(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	// One trace named, so that the stretch beside it is named by one
	// neighbour and that number is a number rather than a zero.
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "path:/home/u/src/widget", "--project", "widget")
	first := longestPause(t, db, cfg, day)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", clock(first), "--worked", "true")
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	var got struct {
		Total struct {
			ClaimedMS      int64 `json:"claimed_pauses_ms"`
			OneNeighbourMS int64 `json:"one_neighbour_ms"`
		} `json:"total"`
		Days []struct {
			ClaimedMS      int64 `json:"claimed_pauses_ms"`
			OneNeighbourMS int64 `json:"one_neighbour_ms"`
		} `json:"days"`
	}
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	// Both numbers have to be there before agreeing about them means
	// anything: 0 == 0 is what this test said for as long as the feature was
	// working, and would have gone on saying if it stopped.
	if got.Total.ClaimedMS == 0 {
		t.Fatalf("no claimed pause reached the frozen day at all:\n%s", out)
	}
	if got.Total.OneNeighbourMS == 0 {
		t.Fatalf("nothing in this day was named by one neighbour:\n%s", out)
	}
	if got.Total.ClaimedMS != got.Days[0].ClaimedMS {
		t.Errorf("the total says %d of claimed pauses and the day says %d",
			got.Total.ClaimedMS, got.Days[0].ClaimedMS)
	}
	if got.Total.OneNeighbourMS != got.Days[0].OneNeighbourMS {
		t.Errorf("the total says %d named by one neighbour and the day says %d",
			got.Total.OneNeighbourMS, got.Days[0].OneNeighbourMS)
	}
}

// Answering a pause again replaces the earlier answer, even when the pause has
// moved since. Two answers about overlapping time are somebody changing their
// mind, and the later one is what they meant.
//
// The same pause twice is the easy half: the answer is keyed by its clock, so
// it overwrites itself. The half that needs the delete is a pause that has
// *moved* — a later ingest puts an event in the middle of it and one pause
// becomes two. Nothing the person did retracts the first answer, and without
// the delete both survive: the report then counts the whole original pause and
// the new half of it, claiming more time than the day has.
func TestAnsweringAPauseAgainReplacesTheAnswer(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	claimed := func() time.Duration {
		var got struct {
			Total struct {
				ClaimedMS int64 `json:"claimed_pauses_ms"`
			} `json:"total"`
		}
		out := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		return time.Duration(got.Total.ClaimedMS) * time.Millisecond
	}

	// The longest pause of the day, claimed as work.
	first := longestPause(t, db, cfg, day)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", clock(first), "--worked", "true")
	whole := claimed()
	if whole < first.To.Sub(first.From) {
		t.Fatalf("claimed %s of a %s pause", whole, first.To.Sub(first.From))
	}

	// Retracting the very same pause leaves nothing of it.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", clock(first), "--worked", "false")
	if now := claimed(); now != 0 {
		t.Fatalf("a retracted pause still counts %s — the old answer survived", now)
	}
	list := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows")
	if strings.Count(list, "not work") != 1 || claims(list) != 0 {
		t.Fatalf("the retraction did not replace the claim:\n%s", list)
	}

	// Claim it again, then make it move: an event in the middle of it turns one
	// pause into two, and the answer the person gives is about the first half.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", clock(first), "--worked", "true")
	middle := first.From.Add(first.To.Sub(first.From) / 2)
	ingestOneEvent(t, db, cfg, middle)

	second := longestPause(t, db, cfg, day)
	if !second.To.Before(first.To) && !second.From.After(first.From) {
		t.Fatalf("the event did not split the pause: %s still runs %s-%s",
			clock(second), second.From, second.To)
	}
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", clock(second), "--worked", "true")

	// Only the second answer may count. Adding the two together is the failure
	// this guards: it claims time the day never had.
	if now := claimed(); now >= whole {
		t.Errorf("claimed %s after the pause moved, want less than the %s of the "+
			"pause it replaced — the stale answer is still being counted", now, whole)
	}
	if n := claims(run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows")); n != 1 {
		t.Errorf("%d pauses are claimed, want 1", n)
	}
}

// claims counts the pauses a listing says were work. "not work" contains the
// word, so the rows that are not claims have to be taken out first.
func claims(list string) int {
	n := 0
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, "→") && strings.Contains(line, "work") &&
			!strings.Contains(line, "not work") {
			n++
		}
	}
	return n
}

// pause is one window of the day as `confirm --windows --json` prints it.
type pause struct{ From, To time.Time }

// longestPause is the one with the most to answer for, which is the one a
// person answers first.
func longestPause(t *testing.T, db, cfg, day string) pause {
	t.Helper()
	var doc []struct{ From, To string }
	raw := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) == 0 {
		t.Fatalf("no pauses (%v):\n%s", err, raw)
	}
	var best pause
	for _, w := range doc {
		var p pause
		var err error
		if p.From, err = time.Parse(time.RFC3339, w.From); err != nil {
			t.Fatal(err)
		}
		if p.To, err = time.Parse(time.RFC3339, w.To); err != nil {
			t.Fatal(err)
		}
		if p.To.Sub(p.From) > best.To.Sub(best.From) {
			best = p
		}
	}
	return best
}

// clock names a pause the way the listing does, which is how --window takes it.
func clock(p pause) string {
	return p.From.Local().Format("15:04") + "-" + p.To.Local().Format("15:04")
}

// ingestOneEvent adds a single session record at a given instant, the way a
// later run of ingest would after more work happened.
func ingestOneEvent(t *testing.T, db, cfg string, at time.Time) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "-home-u-src-widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"user","uuid":"mid-1","timestamp":"%s","sessionId":"s-2",`+
		`"cwd":"/home/u/src/widget","gitBranch":"main","entrypoint":"cli",`+
		`"isSidechain":false,"userType":"external","version":"2.1.219",`+
		`"message":{"content":"x"}}`+"\n", at.UTC().Format("2006-01-02T15:04:05.000Z"))
	if err := os.WriteFile(filepath.Join(src, "later.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "ingest", "--db", db, "--claude-dir", dir, "--config", cfg, "--no-browser")
}

// A frozen day's rows come out of the database by name, for byte-stable
// exports, and a report is read busiest-first. One set of renderers exists so
// that the frozen day and the live one cannot disagree — including about the
// order of their rows.
func TestAFrozenDayIsOrderedLikeALiveOne(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	// Two projects whose alphabetical order is the reverse of their time.
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "path:/home/u/src/widget", "--project", "Zulu")
	run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
		"--trace", "key:ops.example.invalid", "--project", "Alpha")

	live := projectOrder(t, run(t, "report", "--db", db, "--config", cfg, "--day="+day))
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	frozen := projectOrder(t, run(t, "report", "--db", db, "--config", cfg, "--day="+day))

	if len(live) == 0 || len(frozen) != len(live) {
		t.Fatalf("live %v, frozen %v", live, frozen)
	}
	for i := range live {
		if live[i] != frozen[i] {
			t.Fatalf("freezing re-sorted the table: live %v, frozen %v", live, frozen)
		}
	}
}

// projectOrder is the project column of a table, in the order it was printed.
func projectOrder(t *testing.T, table string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(table, "\n") {
		for _, name := range []string{"Zulu", "Alpha"} {
			if strings.HasPrefix(line, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// A confirmed day whose evidence moved says so on the next import.
//
// Until this stage nothing could take events out from under a frozen day. The
// calendar can — a meeting that moved is restated, and restating deletes — and
// an appending source reaches backwards too. The snapshot still reads as it
// did, which is the point of freezing it; what must not happen is the ground
// moving in silence.
func TestAnImportThatDisturbsAConfirmedDaySaysSo(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	frozen := run(t, "report", "--db", db, "--config", cfg, "--day="+day)

	// An import that adds nothing to that day is silent.
	quiet, _ := runBoth(t, "ingest", "--db", db, "--config", cfg,
		"--claude-dir", t.TempDir(), "--no-browser", "--no-calendar")
	if strings.Contains(quiet, day) {
		t.Errorf("an import that changed nothing complained about the day:\n%s", quiet)
	}

	// One that puts an event inside it does not stay silent.
	ingestOneEvent(t, db, cfg, time.Date(date.Year(), date.Month(), date.Day(), 15, 0, 0, 0, time.Local))
	said, _ := runBoth(t, "ingest", "--db", db, "--config", cfg,
		"--claude-dir", t.TempDir(), "--no-browser", "--no-calendar")
	if !strings.Contains(said, day) || !strings.Contains(said, "--reconfirm") {
		t.Errorf("the import said nothing about the confirmed day it changed:\n%s", said)
	}

	// And the day still reads exactly as it was frozen. The warning says the
	// ground moved; it does not move the answer, which is the whole reason a
	// day is frozen at all.
	if now := run(t, "report", "--db", db, "--config", cfg, "--day="+day); now != frozen {
		t.Errorf("the confirmed day changed after an import:\n%s\nwas:\n%s", now, frozen)
	}
}

// An export holds project and subject names — the dictionary — so the file it
// writes is the owner's to read, whether spoor created it or found it.
//
// OpenFile's mode applies only to a file it creates. An export written a
// second time into a file an editor left at 0644 would otherwise keep that
// mode, and be readable by every account on the machine.
func TestAnExportedFileIsNotReadableByEverybody(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	out := filepath.Join(t.TempDir(), "day.md")
	if err := os.WriteFile(out, []byte("left here by something else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "export", "--db", db, "--config", cfg, "--day="+day, "--out", out)

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the export is %04o, want 0600", perm)
	}
}

// Two pauses answered on a frozen day, and the first one survives the second.
//
// Every claim is stored beside the numbers it belongs to, so answering a pause
// rewrites what the earlier ones recorded. Computing that from a fresh build of
// the day is how the earlier claim disappears: an import since then can grow a
// block over the pause it was about, which subtracts it to nothing and deletes
// it from a snapshot nobody meant to touch.
func TestASecondPauseOnAFrozenDayKeepsTheFirst(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	var windows []struct{ From, To string }
	raw := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows", "--json")
	if err := json.Unmarshal([]byte(raw), &windows); err != nil || len(windows) < 2 {
		t.Fatalf("want at least two pauses (%v):\n%s", err, raw)
	}
	spec := func(i int) string {
		at, _ := time.Parse(time.RFC3339, windows[i].From)
		until, _ := time.Parse(time.RFC3339, windows[i].To)
		return at.Local().Format("15:04") + "-" + until.Local().Format("15:04")
	}

	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", spec(0), "--worked", "true", "--window-project", "widget")
	after1 := claimedOfDay(t, db, cfg, day)

	// An import lands a block over the first pause, and then a second pause is
	// answered. Neither may touch what the first answer recorded.
	ingestOneEvent(t, db, cfg, midOf(t, windows[0].From, windows[0].To))
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
		"--window", spec(1), "--worked", "true", "--window-project", "widget")

	// Exactly the second pause more than before. A block grew over the first
	// one in the meantime, so a day rebuilt from today's events would subtract
	// that claim away; the frozen day is what the answers are applied to, and
	// on the frozen day nothing grew anywhere.
	second := midOf(t, windows[1].To, windows[1].To).Sub(midOf(t, windows[1].From, windows[1].From))
	after2 := claimedOfDay(t, db, cfg, day)
	if want := after1 + second; after2 != want {
		t.Errorf("claimed %s after the second pause, want %s (%s and %s) — the "+
			"first answer was recomputed and lost time", after2, want, after1, second)
	}
}

// claimedOfDay is the day's claimed-pause total as the report prints it.
func claimedOfDay(t *testing.T, db, cfg, day string) time.Duration {
	t.Helper()
	var got struct {
		Total struct {
			ClaimedMS int64 `json:"claimed_pauses_ms"`
		} `json:"total"`
	}
	out := run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	return time.Duration(got.Total.ClaimedMS) * time.Millisecond
}

// midOf is the instant halfway between two RFC3339 stamps.
func midOf(t *testing.T, from, to string) time.Time {
	t.Helper()
	a, err := time.Parse(time.RFC3339, from)
	if err != nil {
		t.Fatal(err)
	}
	b, err := time.Parse(time.RFC3339, to)
	if err != nil {
		t.Fatal(err)
	}
	return a.Add(b.Sub(a) / 2)
}

// The flags that had no test of their own. Hard limit 6 says everything the
// interface does is available as a flag, and a flag nobody runs is a promise
// nobody has checked — three of these four had no occurrence anywhere outside
// the line that defines them.
func TestTheRemainingFlagsDoWhatTheySay(t *testing.T) {
	t.Run("assign --work marks a project as not work", func(t *testing.T) {
		db, cfg, date := aDay(t)
		day := date.Format(time.DateOnly)
		run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
			"--trace", "key:ops.example.invalid", "--project", "reading", "--work", "false")
		body, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "work: false") {
			t.Errorf("--work=false did not reach the config:\n%s", body)
		}
		// And the report keeps it out of the working total.
		out := run(t, "report", "--db", db, "--config", cfg, "--day="+day)
		if !strings.Contains(out, "reading") {
			t.Errorf("the project is not in the report at all:\n%s", out)
		}
	})

	t.Run("confirm --window-subject names the pause", func(t *testing.T) {
		db, cfg, date := aDay(t)
		day := date.Format(time.DateOnly)
		run(t, "assign", "--db", db, "--config", cfg, "--day="+day,
			"--trace", "path:/home/u/src/widget", "--project", "widget", "--subject", "review")
		run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
			"--window", clock(longestPause(t, db, cfg, day)), "--worked", "true",
			"--window-project", "widget", "--window-subject", "review")
		out := run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--windows")
		if !strings.Contains(out, "review") {
			t.Errorf("the subject of the pause is nowhere:\n%s", out)
		}
	})

	t.Run("confirm --count-background changes what is summed", func(t *testing.T) {
		// The agent working on its own, well away from anything a person
		// touched: that is what "background" means, and without some of it the
		// flag has nothing to change.
		with := func(counting string) string {
			db, cfg, date := aDay(t)
			day := date.Format(time.DateOnly)
			agentAlone(t, db, cfg, date)
			run(t, "confirm", "--db", db, "--config", cfg, "--day="+day,
				"--count-background="+counting, "--yes")
			return run(t, "report", "--db", db, "--config", cfg, "--day="+day, "--json")
		}
		counted, apart := with("true"), with("false")
		if countedOf(t, counted) <= countedOf(t, apart) {
			t.Errorf("counting the background changed nothing: %d vs %d",
				countedOf(t, counted), countedOf(t, apart))
		}
	})

	t.Run("export --format refuses one it does not have", func(t *testing.T) {
		db, cfg, date := aDay(t)
		day := date.Format(time.DateOnly)
		run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
		var out, errOut bytes.Buffer
		code := Run([]string{"export", "--db", db, "--config", cfg,
			"--day=" + day, "--format", "csv"}, &out, &errOut)
		if code == 0 {
			t.Errorf("an unknown format was accepted:\n%s", out.String())
		}
		if !strings.Contains(errOut.String(), "csv") {
			t.Errorf("the refusal does not name the format asked for: %q", errOut.String())
		}
	})
}

// agentAlone adds a stretch of the agent working with nobody there: two turns
// four minutes apart, hours from the day's human traces.
func agentAlone(t *testing.T, db, cfg string, date time.Time) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "-home-u-src-widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i, clock := range [][2]int{{13, 0}, {13, 4}} {
		at := time.Date(date.Year(), date.Month(), date.Day(), clock[0], clock[1], 0, 0, time.Local)
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"bg%d","timestamp":"%s","sessionId":"s-bg",`+
			`"cwd":"/home/u/src/widget","gitBranch":"main","entrypoint":"cli",`+
			`"isSidechain":false,"userType":"external","version":"2.1.219",`+
			`"message":{"content":"x"}}`+"\n", i, at.UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	if err := os.WriteFile(filepath.Join(src, "agent.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, "ingest", "--db", db, "--claude-dir", dir, "--config", cfg, "--no-browser")
}

// countedOf is the number the flag exists to change: attention, with the
// agent's own time added or not.
func countedOf(t *testing.T, doc string) int64 {
	t.Helper()
	var got struct {
		Total struct {
			CountedMS int64 `json:"counted_ms"`
		} `json:"total"`
	}
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatal(err)
	}
	return got.Total.CountedMS
}

// A subject with no name takes it from the first capture group, and that text
// is printed in the export. This is the second of the two exceptions to "no
// trace is printed in an export", and it is here so that it stays a decision
// rather than becoming an oversight.
//
// What the column holds is matched text, not typed text: off a branch here,
// off a page title in the case that matters. An expression loose enough to
// capture half a title puts half a title in a file that leaves the machine.
func TestASubjectFromACaptureGroupReachesTheExport(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	if err := os.WriteFile(cfg, []byte(`attribution:
  fallback: none
  projects:
    - name: widget
      paths: /home/u/src/widget
      subjects:
        - branches: '^(ma.*)$'
`), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	out := run(t, "export", "--db", db, "--config", cfg, "--day="+day)

	// "main" is the branch the fixture's events carry. Nobody typed it into
	// the config; the expression captured it.
	if !strings.Contains(out, "main") {
		t.Errorf("the captured subject is not in the export, so the exception "+
			"documented in README and export.go no longer holds:\n%s", out)
	}
}

// A threshold given on the command line does nothing to a confirmed day, and
// the day says so.
//
// This is the project's own rule about settings: one that can quietly do
// nothing ships with the check that says so. Five flags are parsed and then
// overwritten by whatever the day was frozen under — correct, and invisible
// from the table, so a reader concludes their threshold changes nothing about
// their day.
func TestAThresholdOnAConfirmedDaySaysItDidNothing(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")

	table, note := runBoth(t, "report", "--db", db, "--config", cfg,
		"--day="+day, "--gap", "30m")
	if !strings.Contains(note, "--gap") || !strings.Contains(note, "no effect") {
		t.Errorf("nothing said the flag was discarded:\n%s", note)
	}
	// And the table is still the frozen one, not one built with the flag.
	plain := run(t, "report", "--db", db, "--config", cfg, "--day="+day)
	if table != plain {
		t.Errorf("the flag changed the frozen day after all:\n%s\nvs\n%s", table, plain)
	}
	// A flag that is not one of the five stays quiet.
	if _, quiet := runBoth(t, "report", "--db", db, "--config", cfg,
		"--day="+day, "--min", "1m"); strings.Contains(quiet, "no effect") {
		t.Errorf("--min was reported as discarded, and it is not:\n%s", quiet)
	}
}

// Reopening a day and then walking away leaves it exactly as it was.
//
// The snapshot is the one thing here that cannot be rebuilt: it exists because
// the rules have moved since, which is precisely why recomputing it would give
// other numbers. So `--reconfirm` must not delete anything by itself — the
// confirmation that replaces it does that, in one transaction — and a command
// that only reads a day must not reopen it at all.
func TestReopeningAndNotConfirmingKeepsTheSnapshot(t *testing.T) {
	db, cfg, date := aDay(t)
	day := date.Format(time.DateOnly)
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--yes")
	frozen := run(t, "report", "--db", db, "--config", cfg, "--day="+day)

	// Reading a confirmed day, with the flag that reopens it, reopens nothing.
	for _, reading := range [][]string{
		{"--reconfirm", "--questions"},
		{"--reconfirm", "--windows"},
	} {
		args := append([]string{"confirm", "--db", db, "--config", cfg, "--day=" + day}, reading...)
		run(t, args...)
		if _, ok := confirmedDays(t, db, cfg)[day]; !ok {
			t.Fatalf("%v threw the snapshot away", reading)
		}
	}
	if now := run(t, "report", "--db", db, "--config", cfg, "--day="+day); now != frozen {
		t.Errorf("the frozen day changed:\n%s\nwas:\n%s", now, frozen)
	}

	// And the day that is genuinely reopened for answering keeps its snapshot
	// until a new one replaces it — quitting without confirming is not a way
	// to lose a day.
	run(t, "confirm", "--db", db, "--config", cfg, "--day="+day, "--reconfirm", "--list")
	if _, ok := confirmedDays(t, db, cfg)[day]; !ok {
		t.Error("the snapshot went away without a new one being written")
	}
}

// confirmedDays is the set of days `confirm --list` reports as confirmed.
func confirmedDays(t *testing.T, db, cfg string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, line := range strings.Split(run(t, "confirm", "--db", db, "--config", cfg, "--list"), "\n") {
		if len(line) >= 10 && strings.Contains(line, "confirmed") {
			out[line[:10]] = true
		}
	}
	return out
}
