// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

// mixedDay is three blocks: one named session with browsing inside it, one of
// nothing but browsing between two projects that disagree, and one where the
// agent works long enough on its own to show up as background.
func mixedDay() []event.Event {
	return []event.Event{
		prompt("09:00:00", "widget"),
		machine("09:00:30", "widget"),
		visit("09:02:00", "git.test", "team-a"),
		visit("09:04:00", "git.test", "team-b"),
		prompt("09:10:00", "widget"),

		visit("11:00:00", "search.test", "q"),
		visit("11:04:00", "search.test", "q"),

		prompt("12:00:00", "gadget"),
		toolResult("12:08:00", "gadget"),
		prompt("12:12:00", "gadget"),
	}
}

func renderJSON(t *testing.T, r Report) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := RenderJSON(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The invariant the roadmap states in bytes: two runs over the same data
// produce the same JSON, character for character. Anything ranged over a map
// on the way out would fail this within a few runs.
func TestJSONIsByteIdentical(t *testing.T) {
	first := renderJSON(t, buildDayReport(mixedDay(), Options{}))
	for i := 0; i < 20; i++ {
		again := renderJSON(t, buildDayReport(mixedDay(), Options{}))
		if !bytes.Equal(first, again) {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, first, again)
		}
	}
}

// And the same when the events arrive in a different order, which is what a
// re-imported database can do.
func TestJSONIsByteIdenticalWhateverTheInputOrder(t *testing.T) {
	want := renderJSON(t, buildDayReport(mixedDay(), Options{}))
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		events := mixedDay()
		rng.Shuffle(len(events), func(a, b int) { events[a], events[b] = events[b], events[a] })
		if got := renderJSON(t, buildDayReport(events, Options{})); !bytes.Equal(want, got) {
			t.Fatalf("shuffle %d differs:\n%s\n---\n%s", i, want, got)
		}
	}
}

// The JSON has to parse, and the numbers in it have to be the numbers in the
// report rather than a second opinion about them.
func TestJSONNumbersMatchTheReport(t *testing.T) {
	r := buildDayReport(mixedDay(), Options{})
	var out struct {
		Total struct {
			AttentionMS  int64 `json:"attention_ms"`
			BackgroundMS int64 `json:"background_ms"`
			ActiveMS     int64 `json:"active_ms"`
			CountedMS    int64 `json:"counted_ms"`
			Projects     []struct {
				Project     string `json:"project"`
				AttentionMS int64  `json:"attention_ms"`
			} `json:"projects"`
		} `json:"total"`
		Days []struct {
			Clusters []struct {
				ProjectFrom string `json:"project_from"`
			} `json:"clusters"`
		} `json:"days"`
	}
	if err := json.Unmarshal(renderJSON(t, r), &out); err != nil {
		t.Fatal(err)
	}

	if got, want := out.Total.AttentionMS, r.Total.Attention.Milliseconds(); got != want {
		t.Errorf("attention_ms %d, want %d", got, want)
	}
	if out.Total.ActiveMS != out.Total.AttentionMS+out.Total.BackgroundMS {
		t.Errorf("active_ms %d is not attention plus background", out.Total.ActiveMS)
	}
	if out.Total.BackgroundMS == 0 {
		t.Error("the fixture holds no background time, so the check below proves nothing")
	}
	// Background is not counted unless it is asked for.
	if out.Total.CountedMS != out.Total.AttentionMS {
		t.Errorf("counted_ms %d, want the attention %d", out.Total.CountedMS, out.Total.AttentionMS)
	}
	var sum int64
	for _, p := range out.Total.Projects {
		sum += p.AttentionMS
	}
	if sum != out.Total.AttentionMS {
		t.Errorf("projects sum to %d, total says %d", sum, out.Total.AttentionMS)
	}
	if len(out.Days) != 1 || len(out.Days[0].Clusters) != 3 {
		t.Fatalf("want one day of three blocks, got %d days", len(out.Days))
	}
	if got := out.Days[0].Clusters[1].ProjectFrom; got != string(OriginNone) {
		t.Errorf("the browsing block says %q; its neighbours disagree", got)
	}
}

// --count-background changes what is summed, never what is measured.
func TestCountBackgroundOnlyChangesTheTotal(t *testing.T) {
	events := mixedDay()
	off := buildDayReport(events, Options{})
	on := buildDayReport(events, Options{CountBackground: true})

	if off.Total.Attention != on.Total.Attention || off.Total.Background != on.Total.Background {
		t.Errorf("the flag moved a measurement: %+v against %+v", off.Total, on.Total)
	}

	var b bytes.Buffer
	if err := RenderTable(&b, on); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "counted") {
		t.Errorf("with --count-background the table does not say what was counted:\n%s", b.String())
	}
}

func TestTableSaysWhatItIsBasedOn(t *testing.T) {
	var b bytes.Buffer
	if err := RenderTable(&b, buildDayReport(mixedDay(), Options{})); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, want := range []string{
		"day 2026-05-04 Monday",
		"widget",
		"git.test/team-a", // grouped by host and first segment, not by host
		backgroundLabel,   // the neutral name, not "idle"
		unnamedLabel,      // unnamed time is a row, not a rounding error
		"neighbours disagree: gadget or widget",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "idle") {
		t.Errorf("the table calls the agent's own time idle:\n%s", out)
	}
}

// A range with nothing in it is an answer, not an empty table.
func TestEmptyRangeSaysSo(t *testing.T) {
	from := day(2026, 5, 4)
	var b bytes.Buffer
	if err := RenderTable(&b, Build(nil, from, from.AddDate(0, 0, 1), Options{})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "No traces") {
		t.Errorf("an empty day printed:\n%s", b.String())
	}
}

// Hours and minutes, never rounded up. A report that claims a minute nobody
// worked is the failure this tool exists to avoid.
func TestDurationsAreNotRoundedUp(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "-"},
		{59 * time.Second, "<1m"},
		{time.Minute + 59*time.Second, "0:01"},
		{90 * time.Minute, "1:30"},
		{25 * time.Hour, "25:00"},
	} {
		if got := hm(tc.d); got != tc.want {
			t.Errorf("hm(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// The timeline is the report read the other way round: when, rather than how
// much. The pauses between blocks are lines of their own, because a schedule
// that skipped them would imply the day was solid.
func TestTimelineListsRunsAndGaps(t *testing.T) {
	r := buildDayReport(mixedDay(), Options{})
	r.Timeline = true
	var b bytes.Buffer
	if err := RenderTable(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, want := range []string{
		"timeline",
		"09:00-09:10", // the first block, one owner throughout
		"— nothing —", // and the pause after it
		"12:00-12:12", // the last block
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the timeline does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "by day") {
		t.Errorf("the timeline and the day-by-day summary were both printed:\n%s", out)
	}
}

// Consecutive stretches with the same owner are one line; a change of owner
// starts a new one. Otherwise the schedule would be a line per event.
func TestTimelineMergesAndSplitsByOwner(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		machine("10:01:00", "widget"),
		machine("10:02:00", "widget"),
		prompt("10:03:00", "gadget"),
		machine("10:04:00", "gadget"),
	}, Options{})

	var runs []string
	for _, run := range r.Days[0].Runs {
		runs = append(runs, fmt.Sprintf("%s:%s", run.From.Format("15:04"), run.Project))
	}
	// The switch lands at 10:02, not at the 10:03 prompt: the stretch from
	// 10:02 is nearer to the prompt about to be typed than to the one before
	// it, which is the rule — a person reads before they answer.
	if got, want := fmt.Sprint(runs), "[10:00:widget 10:02:gadget]"; got != want {
		t.Errorf("runs %s, want %s", got, want)
	}
	// The whole block is covered exactly once, gaps and all.
	var sum time.Duration
	for _, run := range r.Days[0].Runs {
		if !run.Gap {
			sum += run.Attention + run.Background
		}
	}
	if sum != r.Total.Active {
		t.Errorf("runs hold %s, the day is %s", sum, r.Total.Active)
	}
}

// The report is a new place for something to leak out of, and the database
// holds two columns that would matter: `title`, which on a search results page
// is the query somebody typed, and `raw_text`, which carries tool names. The
// report reads `raw_text` to tell a prompt from a tool result and prints
// neither. Asserted over both renderers rather than by reading the code,
// because the next person to add a column will not read this comment.
func TestNeitherRendererPrintsATitleOrALabel(t *testing.T) {
	const secret = "SECRET-QUERY-THAT-MUST-NOT-APPEAR"

	page := visit("10:01:00", "search.test", "q")
	page.Title = secret + " - search results"

	labelled := machine("10:02:00", "widget")
	labelled.RawText = "claude-model mcp__" + secret + "__tool"

	events := []event.Event{
		prompt("10:00:00", "widget"),
		page,
		labelled,
		prompt("10:05:00", "widget"),
	}

	// The dictionary reads titles — that is how an issue key is found without
	// an API — so the report now holds them in memory where it used not to.
	// Every view is checked, including the two that exist because of the
	// dictionary. What a rule may still put on screen is whatever its capture
	// group matched, and that is the author's own expression: here it captures
	// a key and not the query around it.
	d := dict{
		byCWD:   map[string]string{"/src/widget": "widget"},
		subject: map[string]string{page.Title: "WID-1"},
	}
	r := buildDayReport(events, Options{Attribution: d})

	views := map[string]Report{"day": r}
	timeline := r
	timeline.Timeline = true
	views["timeline"] = timeline
	subject := r
	subject.Subject = "WID-1"
	views["subject"] = subject
	missing := r
	missing.Subject = "no such subject"
	views["subject not found"] = missing
	unmatched := r
	unmatched.ShowUnmatched = true
	views["unmatched"] = unmatched

	for view, rep := range views {
		var table bytes.Buffer
		if err := RenderTable(&table, rep); err != nil {
			t.Fatal(err)
		}
		for name, out := range map[string]string{
			"table": table.String(),
			"json":  string(renderJSON(t, rep)),
		} {
			if strings.Contains(out, secret) {
				t.Errorf("the %s renderer printed a page title or a tool label in the %s view:\n%s",
					name, view, out)
			}
		}
	}
}

// The subject view answers "how long has this taken". Asked about something
// that is not a subject, it has to say so and say what is: zero hours and a
// misspelling look identical otherwise.
func TestTheSubjectViewSaysWhenThereIsNothing(t *testing.T) {
	d := dict{
		byCWD:   map[string]string{"/src/widget": "widget"},
		subject: map[string]string{"[WID-42] the thing": "WID-42"},
	}
	events := []event.Event{
		in(prompt("10:00:00", ""), "/src/widget"),
		titled(visit("10:04:00", "tracker.test", "browse"), "[WID-42] the thing"),
		in(prompt("10:08:00", ""), "/src/widget"),
	}
	r := buildDayReport(events, Options{Attribution: d})

	found := r
	found.Subject = "WID-42"
	var out bytes.Buffer
	if err := RenderTable(&out, found); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WID-42", "widget", "by day"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the subject view does not mention %q:\n%s", want, out.String())
		}
	}

	missing := r
	missing.Subject = "WID-99"
	out.Reset()
	if err := RenderTable(&out, missing); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing") || !strings.Contains(out.String(), "WID-42") {
		t.Errorf("a subject with no time does not say so, or does not say what does have time:\n%s", out.String())
	}
}

// Small projects are folded into one line, not dropped. The line carries their
// time, so the column above the total still adds up to it — which is what the
// summary claims two lines further down.
func TestSmallProjectsAreFoldedNotHidden(t *testing.T) {
	var events []event.Event
	// One real project, and six that leave a trace of a minute each.
	for i := 0; i < 10; i++ {
		clock := at("10:00:00").Add(time.Duration(i) * time.Minute).Format("15:04:05")
		events = append(events, prompt(clock, "widget"))
	}
	for i := 0; i < 6; i++ {
		start := at("12:00:00").Add(time.Duration(i) * 20 * time.Minute)
		name := fmt.Sprintf("scratch-%d", i)
		events = append(events,
			prompt(start.Format("15:04:05"), name),
			prompt(start.Add(time.Minute).Format("15:04:05"), name))
	}

	r := buildDayReport(events, Options{})
	r.MinRow = DefaultMinRow
	var b bytes.Buffer
	if err := RenderTable(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	if !strings.Contains(out, "6 under 5m") {
		t.Errorf("the small projects were not folded into a line:\n%s", out)
	}
	for i := 0; i < 6; i++ {
		if strings.Contains(out, fmt.Sprintf("scratch-%d  ", i)) {
			t.Errorf("scratch-%d still has a row of its own:\n%s", i, out)
		}
	}
	// Named rather than merely counted: four of the six are spelled out.
	if !strings.Contains(out, "scratch-0") || !strings.Contains(out, "and 2 more") {
		t.Errorf("the folded line does not name what went into it:\n%s", out)
	}

	// Folding is presentation and nothing else: the same events with the fold
	// turned off print the same totals, to the character.
	r.MinRow = 0
	var full bytes.Buffer
	if err := RenderTable(&full, r); err != nil {
		t.Fatal(err)
	}
	summary := func(text string) string {
		at := strings.Index(text, "\nattention")
		if at < 0 {
			t.Fatalf("no summary in:\n%s", text)
		}
		return text[at:]
	}
	if summary(out) != summary(full.String()) {
		t.Errorf("folding moved a total:\n%s\n---\n%s", summary(out), summary(full.String()))
	}
}

// A project with no attention and hours of agent time is the opposite of
// noise: it is the case the report warns about. It must not be folded away.
func TestAProjectWithAgentTimeIsNotFolded(t *testing.T) {
	events := []event.Event{prompt("10:00:00", "widget")}
	for i := 30; i < 1800; i += 30 {
		clock := at("10:00:00").Add(time.Duration(i) * time.Second).Format("15:04:05")
		events = append(events, machine(clock, "widget"), machine(clock, "headless"))
	}
	events = append(events, prompt("10:30:00", "widget"))

	r := buildDayReport(events, Options{})
	r.MinRow = DefaultMinRow
	var b bytes.Buffer
	if err := RenderTable(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "headless") {
		t.Errorf("a project with agent time and no attention was folded away:\n%s", b.String())
	}
}

// Zero means every project gets a row, for when the fold is in the way.
func TestMinZeroFoldsNothing(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),
		prompt("12:00:00", "speck"),
		prompt("12:01:00", "speck"),
	}, Options{})
	r.MinRow = 0
	var b bytes.Buffer
	if err := RenderTable(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "speck") || strings.Contains(b.String(), "under") {
		t.Errorf("--min=0 folded something:\n%s", b.String())
	}
}

// A threshold is printed back the way somebody would have typed it. Getting
// this wrong is invisible at the default and silly everywhere else: the first
// version turned twenty minutes into "2" by trimming one zero too many.
func TestShortPrintsWholeUnits(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Minute, "5m"},
		{20 * time.Minute, "20m"},
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h30m"},
		{time.Hour, "1h"},
		{2 * time.Hour, "2h"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{10*time.Minute + 20*time.Second, "10m20s"},
		// The one shape holding a "0m" that does not end the string. A trim
		// that went looking for it anywhere would turn this into "1h30s".
		{time.Hour + 30*time.Second, "1h0m30s"},
	} {
		if got := short(tc.d); got != tc.want {
			t.Errorf("short(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
