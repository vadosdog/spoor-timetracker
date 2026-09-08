// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package report

import (
	"fmt"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

// Every fixture below is written by hand. Nothing here comes from a real
// session log: the rules are being tested, not somebody's afternoon.

// zone is deliberately not UTC. Days are local days, and a report built in
// UTC would pass while putting a late evening on the wrong date everywhere
// else.
var zone = time.FixedZone("test+03", 3*60*60)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, zone)
}

// at is a local time on 2026-05-04, the day every fixture uses.
func at(clock string) time.Time {
	t, err := time.ParseInLocation("15:04:05", clock, zone)
	if err != nil {
		panic(err)
	}
	return time.Date(2026, 5, 4, t.Hour(), t.Minute(), t.Second(), 0, zone)
}

var nextID int

func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func base(source, ts string) event.Event {
	nextID++
	return event.Event{Source: source, ExternalID: fmt.Sprintf("e%d", nextID), TS: ts}
}

// prompt is a message a person typed: the one Claude Code event that proves
// somebody was at the keyboard.
//
// Every fixture project gets a session of its own, because that is the usual
// case: one chat window, one working directory. The exception — one window
// whose directory changes underneath it — is built by hand in the test that
// cares about it.
func prompt(clock, project string) event.Event {
	e := base("claude-code", stamp(at(clock)))
	e.Type, e.RawText, e.Project, e.SessionID = "user", "prompt", project, "s-"+project
	return e
}

// machine is the agent working: an assistant message. Never an attention
// point, however many of them there are.
func machine(clock, project string) event.Event {
	e := base("claude-code", stamp(at(clock)))
	e.Type, e.RawText, e.Project, e.SessionID = "assistant", "claude-model", project, "s-"+project
	return e
}

// toolResult is the harness feeding a result back, which looks like a user
// message and is not one.
func toolResult(clock, project string) event.Event {
	e := base("claude-code", stamp(at(clock)))
	e.Type, e.RawText, e.Project, e.SessionID = "user", "tool_result", project, "s-"+project
	return e
}

// sidechain is a subagent being told what to do: the machine talking to
// itself.
func sidechain(clock, project string) event.Event {
	e := prompt(clock, project)
	e.IsSidechain = true
	return e
}

// visit is a browser event. It carries no project, which is the whole
// difficulty this stage exists for.
func visit(clock, host, pathHead string) event.Event {
	e := base("browser", stamp(at(clock)))
	e.Type, e.Subtype, e.Host, e.PathHead = "visit", "link", host, pathHead
	return e
}

func buildDayReport(events []event.Event, opts Options) Report {
	from := day(2026, 5, 4)
	return Build(events, from, from.AddDate(0, 0, 1), opts)
}

func projectOf(t *testing.T, r Report, name string) Project {
	t.Helper()
	for _, p := range r.Total.Projects {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no row for project %q; rows: %v", name, projectNames(r))
	return Project{}
}

func projectNames(r Report) []string {
	var names []string
	for _, p := range r.Total.Projects {
		names = append(names, p.Name)
	}
	return names
}

// A pause exactly at the threshold is still the same block; one second more
// is not. The threshold is a measurement that will be taken again, so which
// side of it is inclusive has to be pinned down.
func TestClusterGapBoundary(t *testing.T) {
	for _, tc := range []struct {
		second string
		blocks int
	}{
		{"10:10:00", 1},
		{"10:10:01", 2},
	} {
		r := buildDayReport([]event.Event{
			prompt("10:00:00", "widget"),
			prompt(tc.second, "widget"),
		}, Options{})
		if got := len(r.Days[0].Clusters); got != tc.blocks {
			t.Errorf("pause until %s: %d blocks, want %d", tc.second, got, tc.blocks)
		}
	}
}

// The pause between two blocks is not time. Anything else would let a report
// claim a lunch break as work because the same project sat on both sides.
func TestTimeBetweenBlocksIsNotCounted(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),
		prompt("12:00:00", "widget"),
		prompt("12:05:00", "widget"),
	}, Options{})
	if got, want := r.Total.Active, 10*time.Minute; got != want {
		t.Errorf("active %s, want %s", got, want)
	}
}

// A block holding two projects is cut between them rather than handed to the
// one with more events in it. Each stretch belongs to whatever was being
// worked on when it started.
func TestMixedBlockIsCutNotRounded(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:02:00", "gadget"),
		prompt("10:04:00", "widget"),
		prompt("10:06:00", "gadget"),
	}, Options{})

	if got, want := projectOf(t, r, "widget").Attention, 4*time.Minute; got != want {
		t.Errorf("widget attention %s, want %s", got, want)
	}
	if got, want := projectOf(t, r, "gadget").Attention, 2*time.Minute; got != want {
		t.Errorf("gadget attention %s, want %s", got, want)
	}
	if got, want := r.Total.Active, 6*time.Minute; got != want {
		t.Errorf("active %s, want %s", got, want)
	}
}

// The invariant, stated as the roadmap states it: at any instant attention
// belongs to exactly one project, so the projects' attention adds up to the
// active time and never to more.
func TestAttentionSumsToActiveTime(t *testing.T) {
	var events []event.Event
	for i := 0; i < 240; i++ {
		clock := at("08:00:00").Add(time.Duration(i) * 30 * time.Second).Format("15:04:05")
		events = append(events, prompt(clock, fmt.Sprintf("p%d", i%4)))
		events = append(events, visit(clock, "example.test", "docs"))
	}
	r := buildDayReport(events, Options{})

	var sum time.Duration
	for _, p := range r.Total.Projects {
		sum += p.Attention
	}
	if sum != r.Total.Attention {
		t.Errorf("projects sum to %s, total says %s", sum, r.Total.Attention)
	}
	if r.Total.Attention+r.Total.Background != r.Total.Active {
		t.Errorf("attention %s plus background %s is not active %s",
			r.Total.Attention, r.Total.Background, r.Total.Active)
	}
}

// Four chats running in parallel all day must not produce 32 hours. The
// stronger form of the same invariant: whatever the events, a day cannot hold
// more than a day.
func TestParallelSessionsCannotExceedADay(t *testing.T) {
	var events []event.Event
	for i := 0; i < 2880; i++ { // every 30 seconds, midnight to midnight
		clock := at("00:00:00").Add(time.Duration(i) * 30 * time.Second).Format("15:04:05")
		for _, p := range []string{"one", "two", "three", "four"} {
			events = append(events, prompt(clock, p))
			events = append(events, machine(clock, p))
		}
	}
	r := buildDayReport(events, Options{})

	var sum time.Duration
	for _, p := range r.Total.Projects {
		sum += p.Attention + p.Background
	}
	if sum > 24*time.Hour {
		t.Errorf("a day held %s", sum)
	}
	if r.Total.Attention > 24*time.Hour {
		t.Errorf("attention alone was %s", r.Total.Attention)
	}
	// Four projects, one day, and the wall clock says so four times over.
	// That number is context, and it is meant to look like this.
	var wall time.Duration
	for _, p := range r.Total.Projects {
		wall += p.Wall
	}
	if wall <= 24*time.Hour {
		t.Errorf("wall over four parallel projects was %s; the point of showing it is that it exceeds a day", wall)
	}
}

// A block of nothing but browsing takes its project from the named blocks on
// either side, and only when they agree.
func TestBrowserBlockInheritsFromNeighbours(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),

		visit("11:00:00", "search.test", "q"),
		visit("11:05:00", "search.test", "q"),

		prompt("12:00:00", "widget"),
		prompt("12:05:00", "widget"),
	}, Options{})

	if got, want := projectOf(t, r, "widget").Attention, 15*time.Minute; got != want {
		t.Errorf("widget attention %s, want %s (the browsing block included)", got, want)
	}
	for _, name := range projectNames(r) {
		if name == Unnamed {
			t.Errorf("the browsing block stayed unnamed though both neighbours said widget")
		}
	}
	if got := r.Days[0].Clusters[1].Origin; got != OriginNeighbours {
		t.Errorf("middle block origin %q, want %q", got, OriginNeighbours)
	}
}

// Neighbours that disagree are both reported and neither is chosen. Guessing
// which of two projects a browsing block belonged to is the one thing this
// rule must not do.
func TestDisagreeingNeighboursLeaveItUnnamed(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),

		visit("11:00:00", "search.test", "q"),
		visit("11:05:00", "search.test", "q"),

		prompt("12:00:00", "gadget"),
		prompt("12:05:00", "gadget"),
	}, Options{})

	unnamed := projectOf(t, r, Unnamed)
	if got, want := unnamed.Attention, 5*time.Minute; got != want {
		t.Errorf("unnamed attention %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(unnamed.Candidates), "[gadget widget]"; got != want {
		t.Errorf("candidates %s, want %s", got, want)
	}
	if got := r.Days[0].Clusters[1].Origin; got != OriginNone {
		t.Errorf("middle block origin %q, want %q", got, OriginNone)
	}
}

// A missing neighbour is not a disagreement. Browsing that opens a morning or
// closes an evening has nothing on its far side, and there is nothing there to
// disagree with the one side that does exist.
func TestOneNeighbourIsEnoughWhenThereIsNoOther(t *testing.T) {
	r := buildDayReport([]event.Event{
		visit("09:00:00", "search.test", "q"),
		visit("09:05:00", "search.test", "q"),

		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),

		visit("11:00:00", "search.test", "q"),
		visit("11:05:00", "search.test", "q"),
	}, Options{})

	for _, name := range projectNames(r) {
		if name == Unnamed {
			t.Fatalf("browsing at the edge of the day stayed unnamed: %v", projectNames(r))
		}
	}
	if got, want := projectOf(t, r, "widget").Attention, 15*time.Minute; got != want {
		t.Errorf("widget attention %s, want %s", got, want)
	}
	for _, i := range []int{0, 2} {
		if got := r.Days[0].Clusters[i].Origin; got != OriginOneNeighbour {
			t.Errorf("block %d origin %q, want %q", i, got, OriginOneNeighbour)
		}
	}
	// The weaker rule is not hidden inside the total: ten of those fifteen
	// minutes rest on it, and the report says so.
	if got, want := r.Total.OneNeighbour, 10*time.Minute; got != want {
		t.Errorf("one-neighbour time %s, want %s", got, want)
	}
}

// With no named block anywhere in the day there is nothing to inherit from,
// and nothing to suggest either.
func TestNoNamedBlockAtAllLeavesItUnnamed(t *testing.T) {
	r := buildDayReport([]event.Event{
		visit("09:00:00", "search.test", "q"),
		visit("09:05:00", "search.test", "q"),
		visit("11:00:00", "search.test", "q"),
		visit("11:05:00", "search.test", "q"),
	}, Options{})

	unnamed := projectOf(t, r, Unnamed)
	if got, want := unnamed.Attention, 10*time.Minute; got != want {
		t.Errorf("unnamed attention %s, want %s", got, want)
	}
	if len(unnamed.Candidates) != 0 {
		t.Errorf("candidates %v, want none", unnamed.Candidates)
	}
	if r.Total.OneNeighbour != 0 {
		t.Errorf("one-neighbour time %s, want none", r.Total.OneNeighbour)
	}
}

// A run of unnamed blocks between two named ones is decided by those two,
// not by each other: nothing inherited is ever inherited from again.
func TestInheritanceDoesNotChain(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("08:00:00", "widget"),
		prompt("08:05:00", "widget"),

		visit("09:00:00", "search.test", "q"),
		visit("09:05:00", "search.test", "q"),
		visit("10:00:00", "search.test", "q"),
		visit("10:05:00", "search.test", "q"),

		prompt("11:00:00", "gadget"),
		prompt("11:05:00", "gadget"),
	}, Options{})

	// Both middle blocks see widget on the left and gadget on the right.
	// Neither may take a name, and in particular the second must not take
	// one from the first.
	unnamed := projectOf(t, r, Unnamed)
	if got, want := unnamed.Attention, 10*time.Minute; got != want {
		t.Errorf("unnamed attention %s, want %s: a block inherited from an inherited one", got, want)
	}
}

// Inside a block that has a name of its own, browsing takes the project of
// the nearest named event. A tie goes to the earlier one: what you were doing
// before the detour, not what you turned to after it.
func TestBrowsingInsideABlockTakesTheNearestProject(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		visit("10:01:00", "search.test", "q"),
		prompt("10:05:00", "gadget"),
		visit("10:07:30", "search.test", "q"), // exactly between the two
		prompt("10:10:00", "widget"),
	}, Options{})

	// 10:00-10:01 widget, 10:01-10:05 widget (the visit sits nearer widget),
	// 10:05-10:07:30 gadget, 10:07:30-10:10 gadget (the tie went to gadget,
	// which is the earlier of the two named events around it).
	if got, want := projectOf(t, r, "widget").Attention, 5*time.Minute; got != want {
		t.Errorf("widget attention %s, want %s", got, want)
	}
	if got, want := projectOf(t, r, "gadget").Attention, 5*time.Minute; got != want {
		t.Errorf("gadget attention %s, want %s", got, want)
	}
}

// Only a typed prompt and a browser visit say a person was there. An
// assistant message, a tool result and a subagent's prompt are the machine.
func TestOnlyHumanEventsOpenAnAttentionWindow(t *testing.T) {
	// A prompt at 10:00, then forty minutes of the agent working, then the
	// next prompt. With a five minute window that is ten minutes of attention
	// and the rest is the agent's.
	events := []event.Event{prompt("10:00:00", "widget")}
	for i := 1; i <= 40; i++ {
		clock := at("10:00:00").Add(time.Duration(i) * time.Minute).Format("15:04:05")
		if i%2 == 0 {
			events = append(events, toolResult(clock, "widget"))
		} else {
			events = append(events, machine(clock, "widget"))
		}
	}
	events = append(events, sidechain("10:20:00", "widget"))
	events = append(events, prompt("10:41:00", "widget"))

	r := buildDayReport(events, Options{})
	if got, want := r.Total.Active, 41*time.Minute; got != want {
		t.Fatalf("active %s, want %s", got, want)
	}
	if got, want := r.Total.Attention, 10*time.Minute; got != want {
		t.Errorf("attention %s, want %s: something other than a typed prompt opened a window", got, want)
	}
	if got, want := r.Total.Background, 31*time.Minute; got != want {
		t.Errorf("background %s, want %s", got, want)
	}
}

// The window reaches backwards as well as forwards: a person reads before
// they type.
func TestTheAttentionWindowReachesBothWays(t *testing.T) {
	r := buildDayReport([]event.Event{
		machine("10:00:00", "widget"),
		prompt("10:09:00", "widget"),
	}, Options{})
	// Active 10:00-10:09; the window is 10:04-10:14, so four of those nine
	// minutes belong to the agent and five to the person.
	if got, want := r.Total.Attention, 5*time.Minute; got != want {
		t.Errorf("attention %s, want %s", got, want)
	}
	if got, want := r.Total.Background, 4*time.Minute; got != want {
		t.Errorf("background %s, want %s", got, want)
	}
}

// Browser events are grouped by host, port and first path segment together.
// One host serves several projects and the first segment is what separates
// them.
func TestBrowserKeysGroupByHostAndFirstSegment(t *testing.T) {
	one := visit("10:00:00", "git.test", "team-a")
	two := visit("10:01:00", "git.test", "team-b")
	three := visit("10:02:00", "git.test", "team-a")
	four := visit("10:03:00", "localhost", "")
	four.Port = "3000"

	r := buildDayReport([]event.Event{one, two, three, four}, Options{})
	got := fmt.Sprint(projectOf(t, r, Unnamed).BrowserKeys)
	want := "[{git.test/team-a 2} {git.test/team-b 1} {localhost:3000 1}]"
	if got != want {
		t.Errorf("browser keys %s, want %s", got, want)
	}
}

// A day is a local day, and a block never crosses midnight. Otherwise the
// same evening lands in one day when asked about on its own and in another
// when asked about as part of a week.
func TestBlocksDoNotCrossMidnight(t *testing.T) {
	from := day(2026, 5, 4)
	events := []event.Event{
		prompt("23:57:00", "widget"),
		prompt("23:59:00", "widget"),
	}
	// One minute past midnight, which is the next local day.
	late := base("claude-code", stamp(day(2026, 5, 5).Add(time.Minute)))
	late.Type, late.RawText, late.Project = "user", "prompt", "widget"
	events = append(events, late)

	r := Build(events, from, from.AddDate(0, 0, 2), Options{})
	if len(r.Days) != 2 {
		t.Fatalf("%d days, want 2", len(r.Days))
	}
	if got, want := r.Days[0].Active, 2*time.Minute; got != want {
		t.Errorf("first day active %s, want %s", got, want)
	}
	if got, want := r.Days[1].Active, time.Duration(0); got != want {
		t.Errorf("second day active %s, want %s", got, want)
	}
	if got, want := r.Total.Active, 2*time.Minute; got != want {
		t.Errorf("total active %s, want %s: a block crossed midnight", got, want)
	}
}

// Asking for a day on its own and asking for the week that holds it must give
// that day the same numbers.
func TestADayIsTheSameInsideAWeek(t *testing.T) {
	events := []event.Event{
		prompt("10:00:00", "widget"),
		visit("10:03:00", "search.test", "q"),
		prompt("10:09:00", "widget"),
	}
	alone := buildDayReport(events, Options{})
	from := day(2026, 5, 4)
	inWeek := Build(events, from, from.AddDate(0, 0, 7), Options{})

	if alone.Days[0].Attention != inWeek.Days[0].Attention {
		t.Errorf("day alone %s, day in a week %s", alone.Days[0].Attention, inWeek.Days[0].Attention)
	}
	if alone.Total.Attention != inWeek.Total.Attention {
		t.Errorf("totals differ: %s and %s", alone.Total.Attention, inWeek.Total.Attention)
	}
}

// A row of zeros for a project that left one trace and no time is better than
// no row at all: the trace is real and the report should say so.
func TestAProjectWithNoTimeStillGetsARow(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),
		prompt("15:00:00", "gadget"),
	}, Options{})

	p := projectOf(t, r, "gadget")
	if p.Attention != 0 || p.Events != 1 {
		t.Errorf("gadget row: %+v", p)
	}
}

// An unreadable timestamp is counted and reported, never dropped in silence.
func TestUnreadableTimestampsAreCounted(t *testing.T) {
	bad := base("claude-code", "yesterday afternoon")
	bad.Type, bad.RawText, bad.Project = "user", "prompt", "widget"
	r := buildDayReport([]event.Event{prompt("10:00:00", "widget"), bad}, Options{})
	if r.Unreadable != 1 {
		t.Errorf("unreadable %d, want 1", r.Unreadable)
	}
}

// The order events arrive in cannot change the report. The store promises an
// order; Build does not rely on that promise.
func TestInputOrderDoesNotMatter(t *testing.T) {
	events := []event.Event{
		prompt("10:00:00", "widget"),
		visit("10:03:00", "search.test", "q"),
		prompt("10:09:00", "gadget"),
		machine("10:12:00", "gadget"),
	}
	forwards := buildDayReport(events, Options{})

	reversed := make([]event.Event, len(events))
	for i, e := range events {
		reversed[len(events)-1-i] = e
	}
	backwards := buildDayReport(reversed, Options{})

	if forwards.Total.Attention != backwards.Total.Attention {
		t.Errorf("attention %s and %s", forwards.Total.Attention, backwards.Total.Attention)
	}
	if fmt.Sprint(projectNames(forwards)) != fmt.Sprint(projectNames(backwards)) {
		t.Errorf("projects %v and %v", projectNames(forwards), projectNames(backwards))
	}
}

// The threshold is a measurement and it will be taken again, so it has to be
// reachable. Everything else in the report is stated against it.
func TestThresholdIsNotAConstant(t *testing.T) {
	events := []event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:12:00", "widget"),
	}
	tight := buildDayReport(events, Options{ClusterGap: 10 * time.Minute})
	loose := buildDayReport(events, Options{ClusterGap: 15 * time.Minute})

	if got := tight.Total.Active; got != 0 {
		t.Errorf("at ten minutes the pause was counted: %s", got)
	}
	if got, want := loose.Total.Active, 12*time.Minute; got != want {
		t.Errorf("at fifteen minutes active %s, want %s", got, want)
	}
}

// The zero Options are the defaults, and the defaults are the measured ones.
func TestZeroOptionsAreTheDefaults(t *testing.T) {
	r := buildDayReport([]event.Event{prompt("10:00:00", "widget")}, Options{})
	if r.Options.ClusterGap != DefaultClusterGap {
		t.Errorf("cluster gap %s, want %s", r.Options.ClusterGap, DefaultClusterGap)
	}
	if r.Options.AttentionWindow != DefaultAttentionWindow {
		t.Errorf("attention window %s, want %s", r.Options.AttentionWindow, DefaultAttentionWindow)
	}
}

// Purity is what the survey measured before this stage and what it will be
// measured on again afterwards, so a block keeps both counts: how many events
// name a project now, and how many named it by themselves.
func TestBlockKeepsOwnAndInheritedCounts(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		visit("10:01:00", "search.test", "q"),
		visit("10:02:00", "search.test", "q"),
	}, Options{})

	c := r.Days[0].Clusters[0]
	if len(c.Projects) != 1 {
		t.Fatalf("block projects %+v, want one", c.Projects)
	}
	if c.Projects[0].Name != "widget" || c.Projects[0].Events != 3 || c.Projects[0].Own != 1 {
		t.Errorf("block projects %+v, want widget with 3 events of which 1 own", c.Projects)
	}
}

// The rule that made this stage worth revisiting. Two windows are open; the
// agent in the one you are not in keeps writing. Its messages must not take
// the minutes you spent reading and answering in the other.
func TestAChattyAgentDoesNotStealTime(t *testing.T) {
	events := []event.Event{prompt("10:00:00", "widget")}
	// gadget's agent talks every ten seconds for four minutes while nothing
	// at all happens in widget, which is what waiting on an agent looks like.
	for i := 10; i < 250; i += 10 {
		clock := at("10:00:00").Add(time.Duration(i) * time.Second).Format("15:04:05")
		events = append(events, machine(clock, "gadget"))
	}
	events = append(events, prompt("10:05:00", "gadget"))

	r := buildDayReport(events, Options{})

	// Nearest touch wins, so the first half belongs to widget — the project
	// being answered — and the second to gadget, which is about to be.
	widget, gadget := projectOf(t, r, "widget"), projectOf(t, r, "gadget")
	if widget.Attention < 2*time.Minute {
		t.Errorf("widget got %s of five minutes; the noisy agent took the rest", widget.Attention)
	}
	if got := widget.Attention + gadget.Attention; got != 5*time.Minute {
		t.Errorf("attention %s, want the whole five minutes", got)
	}
	// And gadget's agent working while you were in widget is agent time.
	if gadget.Agent == 0 {
		t.Error("gadget's agent worked through widget's minutes and none of it was recorded")
	}
}

// Agent time is a fourth number, not a slice of the first three. It is counted
// per project and the same second can belong to two of them.
func TestAgentTimeIsNotPartOfTheDay(t *testing.T) {
	events := []event.Event{prompt("10:00:00", "widget")}
	for i := 30; i < 300; i += 30 {
		clock := at("10:00:00").Add(time.Duration(i) * time.Second).Format("15:04:05")
		events = append(events, machine(clock, "widget"), machine(clock, "gadget"))
	}
	events = append(events, prompt("10:05:00", "widget"))

	r := buildDayReport(events, Options{})
	if got, want := r.Total.Active, 5*time.Minute; got != want {
		t.Fatalf("active %s, want %s", got, want)
	}
	if r.Total.Attention+r.Total.Background != r.Total.Active {
		t.Error("agent time leaked into the day")
	}
	gadget := projectOf(t, r, "gadget")
	if gadget.Attention != 0 {
		t.Errorf("gadget attention %s; you were never in it", gadget.Attention)
	}
	if gadget.Agent < 4*time.Minute {
		t.Errorf("gadget agent time %s, want most of the five minutes", gadget.Agent)
	}
}

// Browsing is a human touch and owns the seconds around it, so where it gets
// its name from matters as much as who owns the interval. It must not take the
// name of whichever agent happened to speak nearest.
func TestBrowsingTakesItsNameFromAPromptNotFromAnAgent(t *testing.T) {
	events := []event.Event{
		prompt("10:00:00", "widget"),
		machine("10:00:30", "gadget"),
		machine("10:00:50", "gadget"),
		visit("10:01:00", "search.test", "q"), // nearest event is gadget's
		machine("10:01:10", "gadget"),
		prompt("10:02:00", "widget"),
	}
	r := buildDayReport(events, Options{})

	for _, p := range r.Total.Projects {
		if p.Name == "gadget" && p.Attention > 0 {
			t.Errorf("gadget took %s: browsing was named after the noisiest agent", p.Attention)
		}
	}
	if got, want := projectOf(t, r, "widget").Attention, 2*time.Minute; got != want {
		t.Errorf("widget attention %s, want %s", got, want)
	}
}

// A block whose prompt fell outside it — a resumed session — still names its
// browsing rather than losing it.
func TestABlockWithNoPromptStillNamesItsBrowsing(t *testing.T) {
	r := buildDayReport([]event.Event{
		machine("10:00:00", "widget"),
		visit("10:01:00", "search.test", "q"),
		machine("10:02:00", "widget"),
	}, Options{})

	for _, name := range projectNames(r) {
		if name == Unnamed {
			t.Errorf("a block with no prompt lost its browsing: %v", projectNames(r))
		}
	}
}

// The attention window follows the clustering threshold rather than a constant
// of its own: at exactly half of it, two touches closer together than the
// threshold leave no background between them, and that is what the background
// line means.
func TestTheWindowFollowsTheThreshold(t *testing.T) {
	events := []event.Event{
		prompt("10:00:00", "widget"),
		machine("10:07:00", "widget"),
		prompt("10:14:00", "widget"),
	}
	// At a fifteen minute threshold the window is 7m30s, so the two prompts,
	// fourteen minutes apart, leave nothing to the agent.
	loose := buildDayReport(events, Options{ClusterGap: 15 * time.Minute})
	if loose.Options.AttentionWindow != 7*time.Minute+30*time.Second {
		t.Fatalf("window %s, want half the threshold", loose.Options.AttentionWindow)
	}
	if loose.Total.Background != 0 {
		t.Errorf("background %s, want none: the prompts are closer than the threshold", loose.Total.Background)
	}
	// An explicit window still wins over the threshold.
	tight := buildDayReport(events, Options{ClusterGap: 15 * time.Minute, AttentionWindow: time.Minute})
	if tight.Total.Background == 0 {
		t.Error("an explicit window was ignored")
	}
}

// One chat window whose working directory changes under it — a `cd` in a shell
// command does it — reports its work under two project names. That is not two
// windows, and the agent column must not read it as the agent working while
// you were somewhere else: you were sitting right there.
func TestOneWindowUnderTwoNamesIsNotAgentTime(t *testing.T) {
	var events []event.Event
	for i, project := range []string{"widget", "widget", "widget-sub", "widget-sub", "widget"} {
		clock := at("10:00:00").Add(time.Duration(i) * time.Minute).Format("15:04:05")
		e := machine(clock, project)
		e.SessionID = "one-window" // the same chat throughout
		events = append(events, e)
	}
	p := prompt("10:00:00", "widget")
	p.SessionID = "one-window"
	events = append(events, p)

	r := buildDayReport(events, Options{})
	for _, p := range r.Total.Projects {
		if p.Agent != 0 {
			t.Errorf("%q collected %s of agent time from its own window", p.Name, p.Agent)
		}
	}
	if r.Total.Agent != 0 {
		t.Errorf("total agent time %s, want none", r.Total.Agent)
	}
}

// Writing a prompt takes time and leaves no trace of its own, so a block that
// opens with one starts a little earlier than its first event.
func TestABlockThatOpensWithAPromptStartsEarlier(t *testing.T) {
	opts := Options{Head: 2 * time.Minute}
	withHead := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		machine("10:05:00", "widget"),
	}, opts)
	if got, want := withHead.Total.Active, 7*time.Minute; got != want {
		t.Errorf("active %s, want %s", got, want)
	}
	// And it is attention, not background: you were the one typing.
	if withHead.Total.Background != 0 {
		t.Errorf("background %s, want none", withHead.Total.Background)
	}

	// A block that opens with browsing gets no head. Typing an address is not
	// composing a prompt.
	browsing := buildDayReport([]event.Event{
		visit("10:00:00", "search.test", "q"),
		visit("10:05:00", "search.test", "q"),
	}, opts)
	if got, want := browsing.Total.Active, 5*time.Minute; got != want {
		t.Errorf("browsing active %s, want %s", got, want)
	}
}

// Reading the last answer takes time too, so a block ends later than its last
// event — every block here, because a person left a trace in both of them.
// The case where nobody did is TestABlockWithNobodyInItGetsNoTail.
func TestABlockEndsLaterThanItsLastEvent(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		machine("10:05:00", "widget"),

		visit("12:00:00", "search.test", "q"),
		visit("12:05:00", "search.test", "q"),
	}, Options{Tail: 2 * time.Minute})
	if got, want := r.Total.Active, 14*time.Minute; got != want {
		t.Errorf("active %s, want %s: two blocks of five minutes and two tails", got, want)
	}
}

// Neither end may reach into a neighbouring block, or two blocks would claim
// the same second and the invariant would go with it.
func TestHeadAndTailStopAtTheNeighbouringBlock(t *testing.T) {
	// Two blocks a minute long, eleven minutes apart, each asking for ten
	// minutes at both ends. The first block's head has nothing before it and
	// takes all ten; the eleven minute pause between them is split down the
	// middle; the last block's tail has nothing after it and takes all ten.
	// 10 + 1 + 5.5, then 5.5 + 1 + 10.
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:01:00", "widget"),

		prompt("10:12:00", "gadget"),
		prompt("10:13:00", "gadget"),
	}, Options{Head: 10 * time.Minute, Tail: 10 * time.Minute})

	if got, want := r.Total.Active, 33*time.Minute; got != want {
		t.Errorf("active %s, want %s: the pause was counted twice or not at all", got, want)
	}
	// All of it is attention: a head and a tail are somebody typing and
	// somebody reading, whatever the attention window would have said.
	if got, want := projectOf(t, r, "widget").Attention, 16*time.Minute+30*time.Second; got != want {
		t.Errorf("widget %s, want %s", got, want)
	}
	if r.Total.Background != 0 {
		t.Errorf("background %s, want none: a head or a tail was filed as the agent's", r.Total.Background)
	}
	var sum time.Duration
	for _, p := range r.Total.Projects {
		sum += p.Attention + p.Background
	}
	if sum != r.Total.Active {
		t.Errorf("projects sum to %s, active is %s", sum, r.Total.Active)
	}
}

// Nor past midnight, whatever the tail is set to.
func TestTheTailStopsAtMidnight(t *testing.T) {
	from := day(2026, 5, 4)
	r := Build([]event.Event{
		prompt("23:58:00", "widget"),
		prompt("23:59:00", "widget"),
	}, from, from.AddDate(0, 0, 1), Options{Tail: time.Hour})

	if got, want := r.Total.Active, 2*time.Minute; got != want {
		t.Errorf("active %s, want %s: the tail ran into the next day", got, want)
	}
}

// Zero has to be expressible: somebody who wants no invented time at all must
// be able to ask for it, and get the same numbers as before the idea existed.
func TestZeroHeadAndTailAddNothing(t *testing.T) {
	events := mixedDay()
	bare := buildDayReport(events, Options{})
	explicit := buildDayReport(events, Options{Head: 0, Tail: 0})
	if bare.Total.Active != explicit.Total.Active {
		t.Errorf("%s and %s", bare.Total.Active, explicit.Total.Active)
	}
}

// The day still cannot hold more than a day, however generously it is padded.
func TestPaddingCannotOverflowTheDay(t *testing.T) {
	var events []event.Event
	for i := 0; i < 480; i++ { // a prompt every three minutes, all day
		clock := at("00:00:00").Add(time.Duration(i) * 3 * time.Minute).Format("15:04:05")
		for _, p := range []string{"one", "two", "three"} {
			events = append(events, prompt(clock, p))
		}
	}
	r := buildDayReport(events, Options{Head: 30 * time.Minute, Tail: 30 * time.Minute})
	if r.Total.Active > 24*time.Hour {
		t.Errorf("a day held %s", r.Total.Active)
	}
	if r.Total.Active > r.Total.Span {
		t.Errorf("active %s exceeds the span %s it is quoted against", r.Total.Active, r.Total.Span)
	}
}

// A project whose agent worked and whose owner never did cannot happen: an
// agent does not start itself. The report says so rather than printing a row
// of dashes and leaving the reader to notice.
func TestAgentWithoutAttentionIsCalledOut(t *testing.T) {
	events := []event.Event{prompt("10:00:00", "widget")}
	for i := 30; i < 300; i += 30 {
		clock := at("10:00:00").Add(time.Duration(i) * time.Second).Format("15:04:05")
		events = append(events, machine(clock, "widget"), machine(clock, "headless"))
	}
	events = append(events, prompt("10:05:00", "widget"))

	r := buildDayReport(events, Options{})
	if got := fmt.Sprint(ranWithoutYou(r.Total.Projects)); got != "[headless]" {
		t.Errorf("flagged %s, want [headless]", got)
	}
}

// The tail is somebody reading; a block with nobody in it does not get one.
// This is the guard the real data never exercises, which is exactly why it
// needs a test rather than a comment.
func TestABlockWithNobodyInItGetsNoTail(t *testing.T) {
	nobody := buildDayReport([]event.Event{
		machine("10:00:00", "widget"),
		machine("10:05:00", "widget"),
		machine("10:09:00", "widget"),
	}, Options{Tail: 2 * time.Minute})
	if got, want := nobody.Total.Active, 9*time.Minute; got != want {
		t.Errorf("active %s, want %s: a block of pure machine output was given a tail", got, want)
	}

	// The same block with one prompt in it does get one.
	touched := buildDayReport([]event.Event{
		machine("10:00:00", "widget"),
		prompt("10:05:00", "widget"),
		machine("10:09:00", "widget"),
	}, Options{Tail: 2 * time.Minute})
	if got, want := touched.Total.Active, 11*time.Minute; got != want {
		t.Errorf("active %s, want %s", got, want)
	}
}

// A block holding no measurable time still has to appear in the schedule, and
// the pauses either side of it have to stay two pauses rather than merging
// into one that never happened.
func TestABlockWithNoTimeStillHasALineAndTwoPauses(t *testing.T) {
	r := buildDayReport([]event.Event{
		prompt("10:00:00", "widget"),
		prompt("10:05:00", "widget"),

		prompt("12:00:00", "gadget"), // alone: no duration at all

		prompt("14:00:00", "widget"),
		prompt("14:05:00", "widget"),
	}, Options{Head: 0, Tail: 0})

	var shape []string
	for _, run := range r.Days[0].Runs {
		if run.Gap {
			shape = append(shape, "gap")
			continue
		}
		shape = append(shape, fmt.Sprintf("%s:%s", run.From.Format("15:04"), run.Project))
	}
	want := "[10:00:widget gap 12:00:gadget gap 14:00:widget]"
	if got := fmt.Sprint(shape); got != want {
		t.Errorf("schedule %s, want %s", got, want)
	}

	// And the lone event belongs to its own line, not to the pause after it:
	// a stretch labelled "nothing happened here" must not carry an event.
	for _, run := range r.Days[0].Runs {
		if run.Gap && run.Events > 0 {
			t.Errorf("a pause from %s carries %d events", run.From.Format("15:04"), run.Events)
		}
	}
	for _, run := range r.Days[0].Runs {
		if !run.Gap && run.Events == 0 {
			t.Errorf("the line at %s carries no events", run.From.Format("15:04"))
		}
	}
}

// Every event belongs to exactly one line of the schedule. There are two ways
// to get this wrong and they pull in opposite directions, so both are here:
//
//   - the last event of a block sits on the boundary with the pause after it,
//     and a half-open rule sends it into the pause — a line reading "nothing
//     happened here" that carries an event;
//   - fixing that by taking both ends inclusive counts an event twice wherever
//     two lines meet *inside* a block, which is at every change of project.
//
// The first fixture has one line per block and catches only the first; the
// second changes project mid-block and catches only the second. The version of
// this test that shipped with the fix had just the first, and the double count
// it was written for went straight through it.
func TestEveryEventBelongsToExactlyOneLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []event.Event
		want   int
	}{
		{
			name: "two blocks, one line each",
			events: []event.Event{
				prompt("10:00:00", "widget"),
				prompt("10:05:00", "widget"),
				prompt("13:00:00", "widget"),
				prompt("13:05:00", "widget"),
			},
			want: 4,
		},
		{
			name: "one block, the project changes inside it",
			events: []event.Event{
				prompt("10:00:00", "widget"),
				prompt("10:05:00", "gadget"),
				prompt("10:09:00", "gadget"),
			},
			want: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := buildDayReport(tc.events, Options{Head: 0, Tail: 0})
			var inRuns int
			for _, run := range r.Days[0].Runs {
				if run.Gap && run.Events > 0 {
					t.Errorf("a pause carries %d events", run.Events)
				}
				inRuns += run.Events
			}
			if inRuns != tc.want {
				t.Errorf("the schedule accounts for %d events of %d", inRuns, tc.want)
			}
		})
	}
}
