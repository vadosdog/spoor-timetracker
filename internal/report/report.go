// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package report turns stored events into a day.
//
// Every rule here is crude, written by hand and deterministic. No model, no
// scoring, no "smart" heuristic: two runs over the same events produce the
// same report byte for byte, and every number in it can be traced back to a
// row in the database. Where the rules are wrong they are wrong the same way
// every time, which is the only way to find out that they are.
//
// The shape of a day, in one paragraph. Events within ClusterGap of each
// other are one block of work; the time inside a block is active time and
// everything else is not time at all. Each stretch between two adjacent
// events belongs to whichever project was being worked on when it started, so
// a block holding two projects is cut between them rather than handed to the
// larger one. Active time that falls inside the window a human touch casts
// around itself is attention; the rest of the block is the agent working
// alone, and it is reported on its own line rather than added in.
package report

import (
	"sort"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/source/browser"
	"github.com/vadosdog/spoor-timetracker/internal/source/claudecode"
)

// Defaults for the two thresholds. Both are measurements rather than
// constants of nature, which is why both can be overridden from the config
// and from a flag.
//
// DefaultClusterGap is the knee in the density of pauses between events,
// measured on two weeks of one person's traces: up to ten minutes the density
// falls smoothly, and past twelve it goes flat — one process becoming
// another, a pause inside the work becoming a return to it later. The buckets
// that decide it hold four to eleven observations each, so it is the best
// number available rather than a good one.
// DefaultAttentionWindow is half the clustering threshold, and it is half of
// it on purpose rather than by coincidence. At exactly gap/2 the windows of
// two touches meet if and only if the touches are no further apart than the
// threshold — so background time means one precise thing: the agent held a
// block of work together across a pause that would otherwise have ended it.
// An unset window follows the configured gap rather than this constant, so
// the two cannot drift apart when the threshold is measured again.
const (
	DefaultClusterGap      = 10 * time.Minute
	DefaultAttentionWindow = DefaultClusterGap / 2
)

// A prompt does not appear out of nowhere and an answer is not read
// instantly. Both are real time that leaves no trace of its own, so a block is
// stretched by a little at each end: DefaultHead before a block that opens
// with a typed prompt, DefaultTail after a block that had a person in it at
// all. Both need one: a block of nothing but agent output gets neither.
//
// These two are the author's estimate of their own habits, not a measurement —
// unlike the clustering threshold, which has a knee in a density curve under
// it. They are small, they are in the config, and what they add is stated in
// the report rather than folded in silently.
const (
	DefaultHead = 2 * time.Minute
	DefaultTail = 2 * time.Minute
)

// Unnamed is the project of time that no rule could name. It is a real row in
// the report rather than a rounding error: on the data this was written
// against, roughly half the blocks of a day hold nothing but browsing.
const Unnamed = ""

// Options are the knobs. A zero threshold asks for its default; a zero Head or
// Tail asks for nothing to be added, because "do not invent any time" has to be
// expressible.
type Options struct {
	// ClusterGap is the largest pause that still counts as the same block of
	// work.
	ClusterGap time.Duration
	// AttentionWindow is the half-width of the window a moment of human
	// attention casts around itself.
	AttentionWindow time.Duration
	// Head is how long it takes to write a prompt: added before a block that
	// begins with one, and never reaching back into the previous block.
	Head time.Duration
	// Tail is how long it takes to read the last answer: added after a block
	// holding at least one human touch, and never reaching into the next one.
	Tail time.Duration
	// CountBackground adds the agent's own time to the totals. It changes
	// what is summed, never what is measured: the background line is printed
	// either way.
	CountBackground bool
}

func (o Options) normalised() Options {
	if o.ClusterGap <= 0 {
		o.ClusterGap = DefaultClusterGap
	}
	if o.AttentionWindow <= 0 {
		o.AttentionWindow = o.ClusterGap / 2
	}
	if o.Head < 0 {
		o.Head = 0
	}
	if o.Tail < 0 {
		o.Tail = 0
	}
	return o
}

// Origin says how a block of work came by its project.
type Origin string

const (
	// OriginOwn means at least one event in the block carried a project of
	// its own.
	OriginOwn Origin = "own"
	// OriginNeighbours means nothing in the block was named and the nearest
	// named block on either side named the same project.
	OriginNeighbours Origin = "neighbours"
	// OriginOneNeighbour means there was a named block on one side and none at
	// all on the other, so there was nothing for it to disagree with. Weaker
	// evidence than OriginNeighbours and kept apart from it on purpose: the
	// report says how much of itself rests on it.
	OriginOneNeighbour Origin = "one-neighbour"
	// OriginNone means nothing in the block was named and the two named blocks
	// around it named different projects. The block stays unnamed and both
	// candidates are reported instead of one of them being picked.
	OriginNone Origin = "none"
)

// Report is a range of days.
type Report struct {
	// From and To bound the range in local time, To exclusive.
	From, To time.Time
	Options  Options
	Days     []Day
	Total    Totals
	// Timeline asks the table renderer for the schedule rather than the
	// day-by-day summary. It changes nothing that is measured.
	Timeline bool
	// MinRow folds projects smaller than this into one line. Presentation
	// only: the folded line carries their time, so nothing stops adding up.
	MinRow time.Duration
	// Unreadable counts rows whose timestamp could not be parsed. Nothing
	// should ever produce one; a report that silently dropped events would be
	// worse than one that says how many.
	Unreadable int
}

// Totals are the sums that both a day and a whole range carry.
type Totals struct {
	// Attention is time a person was at the keyboard, by the rule below.
	// This is the number that gets summed.
	Attention time.Duration
	// Background is active time outside every attention window: the agent
	// working while nobody was watching.
	Background time.Duration
	// Active is Attention + Background: everything inside a block of work.
	Active time.Duration
	// Span is from the first trace of a day to its last, summed over days.
	// Context for Active, never a total of anything.
	Span time.Duration
	// Agent is the per-project agent time added up. Context, like Span: it
	// counts the same second once per project whose agent was busy in it.
	Agent time.Duration
	// Padding is how much of Active the head and the tail put there: time that
	// left no trace of its own and was added because writing a prompt and
	// reading an answer take a while. Already inside Attention; reported
	// apart because it is the one part of the day that was not measured.
	Padding time.Duration
	// OneNeighbour is how much of Active sits in blocks that took their
	// project from the only named block anywhere near them, with nothing on
	// the far side to agree or disagree. It is already counted inside
	// Attention and Background; it is reported separately because it is the
	// weakest rule in here and the report should not hide how much of itself
	// rests on it.
	OneNeighbour time.Duration
	Projects     []Project
}

// Run is a stretch of the day with one owner: what you were on, from when to
// when. Consecutive stretches with the same owner are one run, and the pauses
// between blocks of work are runs too, marked as such — a timeline that hid
// them would imply the day was solid.
type Run struct {
	From, To   time.Time
	Project    string
	Attention  time.Duration
	Background time.Duration
	// Gap marks a pause between blocks: no events, no time counted.
	Gap         bool
	Events      int
	Sources     []SourceCount
	BrowserKeys []BrowserKey
}

// Day is one local day, computed on its own events only. A block of work never
// crosses midnight, so asking for a day and asking for the week that contains
// it give that day the same numbers.
type Day struct {
	Date time.Time
	// First and Last are the day's first and last trace. Zero when there are
	// none.
	First, Last time.Time
	Totals
	Clusters []Cluster
	Runs     []Run
}

// Project is one row of the report.
type Project struct {
	// Name is empty for the unnamed row.
	Name       string
	Attention  time.Duration
	Background time.Duration
	// Wall is from the project's first event to its last, summed over days.
	// It is what four parallel chats would give you if their hours were added
	// up, which is why it sits next to Attention rather than being summed
	// with it: the sum of Wall over projects can exceed the length of a day.
	Wall time.Duration
	// Agent is time this project's own session was producing output while the
	// moment belonged to another project — the agent working while you were
	// in a different window. It is not part of Attention or Background and
	// not part of the day: two projects can be worked on by their agents at
	// the same second, so summing this across projects can exceed 24 hours,
	// exactly like Wall.
	Agent time.Duration
	// Events is how many events carry this project, and Sources is where they
	// came from. This is the "on what grounds" column.
	Events  int
	Sources []SourceCount
	// BrowserKeys are host, port and first path segment together — the unit a
	// browser event is grouped by, because one host serves several projects
	// and the first segment is what separates them.
	BrowserKeys []BrowserKey
	// Candidates are the projects the neighbours of an unnamed block
	// suggested and did not agree on. Both are shown; neither is chosen.
	Candidates []string
}

// SourceCount is how many of a project's events one source produced.
type SourceCount struct {
	Source string
	Events int
}

// BrowserKey is host[:port][/first-segment] and how many visits it holds.
type BrowserKey struct {
	Key    string
	Visits int
}

// Cluster is one block of work: events no further apart than ClusterGap.
type Cluster struct {
	From, To   time.Time
	Attention  time.Duration
	Background time.Duration
	// Project is set when the whole block resolves to one project. A block
	// holding two is not given to the larger one — the time inside it is cut
	// between them — so this stays empty and Projects has the detail.
	Project    string
	Origin     Origin
	Candidates []string
	Events     int
	Projects   []ProjectCount
	Sources    []SourceCount
}

// ProjectCount is one project's presence inside a block: how many events name
// it after inheritance, and how many of those named it by themselves.
type ProjectCount struct {
	Name   string
	Events int
	Own    int
}

// entry is an event with its timestamp parsed and its project resolved.
type entry struct {
	e       event.Event
	t       time.Time
	project string
	// own records that the event arrived carrying a project. Inheritance
	// reads this rather than project, so the order blocks are visited in
	// cannot change the answer.
	own bool
}

// Build turns events into a report for [from, to), both in local time.
//
// Events outside the range are ignored, so the caller may hand over more than
// it asks about. from and to are expected to be local midnights.
func Build(events []event.Event, from, to time.Time, opts Options) Report {
	opts = opts.normalised()
	rep := Report{From: from, To: to, Options: opts}

	loc := from.Location()
	entries := make([]entry, 0, len(events))
	for _, e := range events {
		t, err := time.Parse(time.RFC3339, e.TS)
		if err != nil {
			rep.Unreadable++
			continue
		}
		local := t.In(loc)
		if local.Before(from) || !local.Before(to) {
			continue
		}
		entries = append(entries, entry{e: e, t: local, project: e.Project, own: e.Project != ""})
	}
	// The store hands events back in a total order already. Sorting again is
	// what makes Build independent of that promise, and it is a stable sort
	// over an almost-sorted slice.
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].t.Equal(entries[j].t) {
			return entries[i].t.Before(entries[j].t)
		}
		if entries[i].e.Source != entries[j].e.Source {
			return entries[i].e.Source < entries[j].e.Source
		}
		return entries[i].e.ExternalID < entries[j].e.ExternalID
	})

	for day := from; day.Before(to); day = nextMidnight(day) {
		end := nextMidnight(day)
		lo := sort.Search(len(entries), func(i int) bool { return !entries[i].t.Before(day) })
		hi := sort.Search(len(entries), func(i int) bool { return !entries[i].t.Before(end) })
		rep.Days = append(rep.Days, buildDay(entries[lo:hi], day, opts))
	}

	rep.Total = aggregate(rep.Days)
	return rep
}

// nextMidnight advances by one local day. Adding 24 hours would be wrong twice
// a year: a day with a daylight-saving change is 23 or 25 hours long.
func nextMidnight(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, day.Location())
}

func buildDay(entries []entry, date time.Time, opts Options) Day {
	day := Day{Date: date}
	if len(entries) == 0 {
		return day
	}
	day.First = entries[0].t
	day.Last = entries[len(entries)-1].t

	blocks := splitBlocks(entries, opts.ClusterGap)
	origins, candidates := attribute(entries, blocks)
	windows := attentionWindows(entries, opts.AttentionWindow)
	owners, ownerSessions := intervalOwners(entries, blocks)
	live := liveSessions(entries, opts.ClusterGap)

	// The time between two adjacent events belongs to the project you last
	// touched, and a project you were not in collects agent time for the same
	// seconds instead. Cutting a mixed block this way is what keeps the
	// invariant true — one instant, one project — without handing a block
	// holding two projects to the larger of them.
	type totals struct{ attention, background time.Duration }
	byProject := map[string]*totals{}
	agent := map[string]time.Duration{}
	dayEnd := nextMidnight(date)
	var prevTo time.Time
	for bi, b := range blocks {
		// A block is stretched at both ends by time that leaves no trace:
		// writing the prompt that opened it, and reading the answer that
		// closed it. Bounded by the neighbouring blocks and by midnight, so
		// two blocks can never claim the same second and a day can never hold
		// more than a day.
		// Neither end may take more than half the pause it reaches into. Half
		// rather than all of it, because the block on the other side is
		// reaching back with the same claim, and half each is the only split
		// that needs no order and can never overlap. It is conservative when
		// the other block turns out not to want its share — and conservative
		// is the right way round for a tool whose whole point is not to
		// overstate. With any sane setting it never binds anyway: two blocks
		// are more than the clustering threshold apart by definition.
		from, to := entries[b.lo].t, entries[b.hi-1].t
		if opts.Head > 0 && entries[b.lo].e.Source == claudecode.SourceName && isAttentionPoint(entries[b.lo].e) {
			head := opts.Head
			if bi > 0 {
				if half := entries[b.lo].t.Sub(entries[blocks[bi-1].hi-1].t) / 2; head > half {
					head = half
				}
			}
			if from = from.Add(-head); from.Before(date) {
				from = date
			}
		}
		touched := false
		for i := b.lo; i < b.hi; i++ {
			if isAttentionPoint(entries[i].e) {
				touched = true
				break
			}
		}
		// The tail is somebody reading the last answer, so it needs a somebody.
		// A block of nothing but assistant messages — a session resumed with
		// its prompt in an earlier block — had nobody in it to do the reading,
		// and inventing two minutes of it would be exactly the appropriation
		// this tool exists not to do.
		if opts.Tail > 0 && touched {
			tail := opts.Tail
			if bi+1 < len(blocks) {
				if half := entries[blocks[bi+1].lo].t.Sub(entries[b.hi-1].t) / 2; tail > half {
					tail = half
				}
			}
			if to = to.Add(tail); to.After(dayEnd) {
				to = dayEnd
			}
		}
		c := Cluster{
			From:       from,
			To:         to,
			Origin:     origins[b.lo],
			Candidates: candidates[b.lo],
			Events:     b.hi - b.lo,
			Projects:   blockProjects(entries[b.lo:b.hi]),
			Sources:    sourceCounts(entries[b.lo:b.hi]),
		}
		// The stretches inside the block, plus the head and the tail. Each is
		// a half-open interval with one owner, and together they tile the
		// block exactly once.
		type stretch struct {
			from, to time.Time
			at       int // the event whose owner decides this one
			// human marks the head and the tail. They are attention outright
			// rather than by the window: they exist because somebody was
			// typing or reading, which is what attention is. Letting the
			// window judge them would file the minutes you spent writing a
			// prompt under "the agent worked in the background".
			human bool
		}
		stretches := make([]stretch, 0, b.hi-b.lo+1)
		if from.Before(entries[b.lo].t) {
			stretches = append(stretches, stretch{from, entries[b.lo].t, b.lo, true})
		}
		for i := b.lo; i < b.hi-1; i++ {
			stretches = append(stretches, stretch{entries[i].t, entries[i+1].t, i, false})
		}
		if to.After(entries[b.hi-1].t) {
			stretches = append(stretches, stretch{entries[b.hi-1].t, to, b.hi - 1, true})
		}

		// Gaps run between blocks, not between runs. Reading the end of the
		// last run instead would give the same answer today — every block
		// leaves one, even a block with no measurable time — but it would tie
		// where a pause starts to how a block happens to be cut up inside,
		// which are two unrelated things.
		if !prevTo.IsZero() && from.After(prevTo) {
			day.Runs = append(day.Runs, Run{From: prevTo, To: from, Gap: true})
		}
		prevTo = to

		for _, st := range stretches {
			span := st.to.Sub(st.from)
			if span <= 0 {
				continue
			}
			from, to := st.from, st.to
			i := st.at
			att := span
			if st.human {
				day.Padding += span
			} else {
				att = windows.overlap(from, to)
			}
			owner := owners[i]
			t, ok := byProject[owner]
			if !ok {
				t = &totals{}
				byProject[owner] = t
			}
			t.attention += att
			t.background += span - att
			c.Attention += att
			c.Background += span - att

			// Same owner as the stretch before, and touching it: one run.
			if n := len(day.Runs); n > 0 && !day.Runs[n-1].Gap &&
				day.Runs[n-1].Project == owner && !day.Runs[n-1].To.Before(from) {
				day.Runs[n-1].To = to
				day.Runs[n-1].Attention += att
				day.Runs[n-1].Background += span - att
			} else {
				day.Runs = append(day.Runs, Run{
					From: from, To: to, Project: owner,
					Attention: att, Background: span - att,
				})
			}

			// The same seconds, seen from every window you were not in. Two
			// windows working on one project both count: the question this
			// answers is how much machine work happened, and two agents
			// grinding on the same thing did twice as much of it.
			for _, run := range live {
				if run.project == owner || run.session == ownerSessions[i] {
					continue
				}
				agent[run.project] += run.live.overlap(from, to)
			}
		}
		// A block of one event, with nothing added at either end, holds no
		// time at all. It is still a thing that happened at a time, so the
		// schedule keeps a line for it rather than leaving a hole between two
		// pauses.
		if n := len(day.Runs); n == 0 || day.Runs[n-1].Gap || day.Runs[n-1].To.Before(from) {
			day.Runs = append(day.Runs, Run{From: from, To: from, Project: owners[b.lo]})
		}
		if len(c.Projects) == 1 {
			c.Project = c.Projects[0].Name
		}
		if c.Origin == OriginOneNeighbour {
			day.OneNeighbour += c.Attention + c.Background
		}
		day.Clusters = append(day.Clusters, c)
	}

	// Every project that holds an event gets a row, including the ones that
	// hold no time at all: a single visit, or the last event of a block, is a
	// real trace of a real project and a row of zeros says so.
	for _, name := range distinctProjects(entries) {
		p := Project{Name: name, Agent: agent[name]}
		if t, ok := byProject[name]; ok {
			p.Attention, p.Background = t.attention, t.background
		}
		fillEvidence(&p, entries)
		p.Candidates = candidatesFor(name, blocks, entries, candidates)
		day.Projects = append(day.Projects, p)
	}
	sortProjects(day.Projects)

	fillRuns(day.Runs, entries)

	// Span bounds the counted time rather than the traces, so that a day whose
	// single block runs from the first trace to the last cannot come out at
	// more than 100% covered once the head and the tail are added.
	day.Span = day.Clusters[len(day.Clusters)-1].To.Sub(day.Clusters[0].From)

	for _, p := range day.Projects {
		day.Attention += p.Attention
		day.Background += p.Background
		day.Agent += p.Agent
	}
	day.Active = day.Attention + day.Background
	return day
}

// fillRuns counts the events inside each run. Membership is by time rather
// than by which stretch an event opened: a run is a piece of the clock, and
// what it holds is whatever happened during it.
func fillRuns(runs []Run, entries []entry) {
	at := 0
	for i := range runs {
		for at < len(entries) && entries[at].t.Before(runs[i].From) {
			at++
		}
		// A pause holds no events: that is what makes it a pause. Saying so
		// here rather than letting the arithmetic decide matters, because the
		// last event of a block sits exactly on the boundary — the block ends
		// *at* it — and a half-open rule would drop it into the pause and
		// print a line reading "nothing happened here" carrying an event.
		if runs[i].Gap {
			continue
		}
		// So the last run of a block takes its end as well as its start.
		// Anywhere else an event on the boundary opens the next stretch and
		// belongs to that one.
		last := i+1 == len(runs) || runs[i+1].Gap
		inRun := func(t time.Time) bool {
			if last {
				return !t.After(runs[i].To)
			}
			return t.Before(runs[i].To)
		}
		sources := map[string]int{}
		keys := map[string]int{}
		for j := at; j < len(entries) && inRun(entries[j].t); j++ {
			runs[i].Events++
			sources[entries[j].e.Source]++
			if k := browserKey(entries[j].e); k != "" {
				keys[k]++
			}
		}
		for source, n := range sources {
			runs[i].Sources = append(runs[i].Sources, SourceCount{Source: source, Events: n})
		}
		sortSources(runs[i].Sources)
		for key, n := range keys {
			runs[i].BrowserKeys = append(runs[i].BrowserKeys, BrowserKey{Key: key, Visits: n})
		}
		sortBrowserKeys(runs[i].BrowserKeys)
	}
}

// candidatesFor collects what the neighbours of the unnamed blocks suggested.
// Only the unnamed row can have any: a block that resolved to a project has
// nothing left to be undecided about.
func candidatesFor(name string, blocks []block, entries []entry, candidates map[int][]string) []string {
	if name != Unnamed {
		return nil
	}
	var all []string
	for _, b := range blocks {
		if entries[b.lo].project != Unnamed {
			continue
		}
		all = append(all, candidates[b.lo]...)
	}
	return distinct(all)
}

// block is a half-open index range into the day's entries.
type block struct{ lo, hi int }

func splitBlocks(entries []entry, gap time.Duration) []block {
	var blocks []block
	lo := 0
	for i := 1; i < len(entries); i++ {
		if entries[i].t.Sub(entries[i-1].t) > gap {
			blocks = append(blocks, block{lo, i})
			lo = i
		}
	}
	return append(blocks, block{lo, len(entries)})
}

// attribute fills in the project of every event that arrived without one.
//
// Two rules, both crude and both deterministic:
//
//   - inside a block that has a name of its own, an unnamed event takes the
//     project of the nearest named event in that block. A browsing detour in
//     the middle of a session belongs to the session around it; that is the
//     whole reason browsing is worth importing.
//   - a block where nothing is named at all — about half of them, all
//     browsing — looks at the nearest named block before it and after it. Two
//     neighbours naming the same project, or one neighbour and nothing at all
//     on the other side: the block takes the name. Two neighbours naming
//     different projects: the block stays unnamed and both candidates are
//     reported, because choosing between two projects is exactly the thing
//     this tool is not for.
//
// A missing neighbour is not a disagreement. The browsing that opens a
// morning and closes an evening has nothing on its far side, and on real data
// that, rather than genuine disagreement, is what most unnamed time was: three
// quarters of it. The two cases are still told apart — OriginOneNeighbour is
// weaker evidence and the report says how much of itself rests on it.
//
// Both rules read `own` rather than the resolved project, so nothing
// inherited is ever inherited from again and the answer does not depend on
// the order blocks are visited in.
func attribute(entries []entry, blocks []block) (map[int]Origin, map[int][]string) {
	origins := map[int]Origin{}
	candidates := map[int][]string{}

	for _, b := range blocks {
		isAnchor := anchors(entries[b.lo:b.hi])
		prevNamed := make([]int, b.hi-b.lo)
		last := -1
		for i := b.lo; i < b.hi; i++ {
			prevNamed[i-b.lo] = last
			if isAnchor(i - b.lo) {
				last = i
			}
		}
		nextNamed := make([]int, b.hi-b.lo)
		next := -1
		for i := b.hi - 1; i >= b.lo; i-- {
			nextNamed[i-b.lo] = next
			if isAnchor(i - b.lo) {
				next = i
			}
		}
		if last == -1 {
			origins[b.lo] = OriginNone // filled in by the neighbour pass below
			continue
		}
		origins[b.lo] = OriginOwn
		for i := b.lo; i < b.hi; i++ {
			if entries[i].own {
				continue
			}
			p, n := prevNamed[i-b.lo], nextNamed[i-b.lo]
			switch {
			case p >= 0 && n >= 0:
				// A tie goes to the earlier one: what you were doing before
				// the detour, not what you turned to after it.
				if entries[i].t.Sub(entries[p].t) <= entries[n].t.Sub(entries[i].t) {
					entries[i].project = entries[p].project
				} else {
					entries[i].project = entries[n].project
				}
			case p >= 0:
				entries[i].project = entries[p].project
			case n >= 0:
				entries[i].project = entries[n].project
			}
		}
	}

	// The neighbour pass. Named blocks are found by walking outwards, so a
	// run of unnamed blocks between two named ones is decided by those two
	// rather than by each other.
	named := func(b block) (string, string, bool) {
		isAnchor := anchors(entries[b.lo:b.hi])
		first, lastp := "", ""
		for i := b.lo; i < b.hi; i++ {
			if isAnchor(i - b.lo) {
				if first == "" {
					first = entries[i].project
				}
				lastp = entries[i].project
			}
		}
		return first, lastp, first != ""
	}
	for bi, b := range blocks {
		if origins[b.lo] != OriginNone {
			continue
		}
		before, after := "", ""
		for j := bi - 1; j >= 0; j-- {
			if _, l, ok := named(blocks[j]); ok {
				before = l
				break
			}
		}
		for j := bi + 1; j < len(blocks); j++ {
			if f, _, ok := named(blocks[j]); ok {
				after = f
				break
			}
		}
		name, origin := "", OriginNone
		switch {
		case before != "" && before == after:
			name, origin = before, OriginNeighbours
		case before != "" && after == "":
			name, origin = before, OriginOneNeighbour
		case after != "" && before == "":
			name, origin = after, OriginOneNeighbour
		}
		if origin == OriginNone {
			// Either two neighbours that disagree, or no named block anywhere
			// in the day. distinct drops the empty ones, so the second case
			// leaves no candidates rather than a list of nothing.
			candidates[b.lo] = distinct([]string{before, after})
			continue
		}
		origins[b.lo] = origin
		for i := b.lo; i < b.hi; i++ {
			entries[i].project = name
		}
	}
	return origins, candidates
}

// intervalOwners decides, for every stretch between two adjacent events, which
// project it belongs to: the one you last touched.
//
// The obvious rule — whoever produced the event that opens the stretch — is
// wrong as soon as more than one window is open, and that is the normal way to
// work with agents. While you read and answer in one project, the agents in
// the other two keep writing, and their messages would take the seconds you
// spent in the first. The busiest agent would win the day rather than the
// project you were in. Measured on real data: the owner changed 1522 times in
// a fortnight under that rule and 132 times under this one, and 9% of the
// hours moved between projects.
//
// So the owner is the project of the nearest human touch — a prompt somebody
// typed, or a page their browser recorded — with a tie going to the earlier
// one. Nearest rather than most recent, because a person reads before they
// answer: the minute before a prompt was spent on the thing about to be
// prompted, not on the thing left behind. A block with no human touch in it at
// all falls back to the project of the event itself; there is nothing better
// to go on.
func intervalOwners(entries []entry, blocks []block) (owners, sessions []string) {
	owners = make([]string, len(entries))
	sessions = make([]string, len(entries))
	for _, b := range blocks {
		prev := make([]int, b.hi-b.lo)
		last := -1
		for i := b.lo; i < b.hi; i++ {
			prev[i-b.lo] = last
			if isAttentionPoint(entries[i].e) {
				last = i
			}
		}
		next := make([]int, b.hi-b.lo)
		following := -1
		for i := b.hi - 1; i >= b.lo; i-- {
			next[i-b.lo] = following
			if isAttentionPoint(entries[i].e) {
				following = i
			}
		}
		claim := func(i, touch int) {
			if touch < 0 {
				owners[i], sessions[i] = entries[i].project, ""
				return
			}
			owners[i] = entries[touch].project
			// The window the touch was in. Empty for a browser visit, which
			// belongs to no chat: while you are reading a page, every session
			// is one you are not in.
			sessions[i] = entries[touch].e.SessionID
		}
		for i := b.lo; i < b.hi; i++ {
			if isAttentionPoint(entries[i].e) {
				claim(i, i)
				continue
			}
			p, n := prev[i-b.lo], next[i-b.lo]
			switch {
			case p >= 0 && n >= 0:
				if entries[i].t.Sub(entries[p].t) <= entries[n].t.Sub(entries[i].t) {
					claim(i, p)
				} else {
					claim(i, n)
				}
			case p >= 0:
				claim(i, p)
			case n >= 0:
				claim(i, n)
			default:
				claim(i, -1)
			}
		}
	}
	return owners, sessions
}

// liveRun is one window producing output for one project: when that session's
// agent was busy, and what it was busy with.
type liveRun struct {
	session string
	project string
	live    *windows
}

// liveSessions is when each chat window's agent was producing output: the
// stretches between its consecutive Claude Code events, as long as they are no
// further apart than the clustering threshold.
//
// This is what makes the fourth number possible. A second in which one window
// was live while your attention was in a different one is agent time for
// whatever that window was working on.
//
// Keyed by session **and** project, not by project alone, and the session is
// what the comparison is made on. One chat changes its own `cwd` — a `cd` in a
// shell command is enough — so a single window can report work under two
// project names, and comparing names would call that "the agent worked while
// you were elsewhere" when you were sitting right there. Measured on real
// data: one session accounted for 1972 events under two names in a day.
//
// Browser events are left out: a page you opened is you, not the agent.
func liveSessions(entries []entry, gap time.Duration) []liveRun {
	type key struct{ session, project string }
	times := map[key][]time.Time{}
	for _, en := range entries {
		if en.e.Source != claudecode.SourceName || en.project == Unnamed {
			continue
		}
		k := key{en.e.SessionID, en.project}
		times[k] = append(times[k], en.t)
	}
	var runs []liveRun
	for k, ts := range times {
		w := &windows{}
		for i := 0; i+1 < len(ts); i++ {
			if ts[i+1].Sub(ts[i]) <= gap {
				w.add(ts[i], ts[i+1])
			}
		}
		if len(w.from) > 0 {
			runs = append(runs, liveRun{session: k.session, project: k.project, live: w})
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].session != runs[j].session {
			return runs[i].session < runs[j].session
		}
		return runs[i].project < runs[j].project
	})
	return runs
}

// anchors decides which events inside a block a project name may be inherited
// from.
//
// A prompt somebody typed is the best evidence there is of which project they
// were in, so where a block holds any, only those count. Without this the same
// flaw intervalOwners fixes would come back through the side door: browsing is
// a human touch and owns the seconds around it, but it carries no project of
// its own, so it would take the name of whichever agent happened to speak
// nearest — the busiest one — and hand those seconds to a project you were not
// in.
//
// A block with no prompt in it at all — a resumed session, or one whose prompt
// fell in an earlier block — falls back to any event carrying a project. There
// is nothing better to go on, and leaving it unnamed would lose real work.
func anchors(block []entry) func(i int) bool {
	prompt := func(i int) bool { return block[i].own && isAttentionPoint(block[i].e) }
	for i := range block {
		if prompt(i) {
			return prompt
		}
	}
	return func(i int) bool { return block[i].own }
}

// attentionWindows merges the windows every human touch casts around itself.
//
// What counts as a touch is deliberately short: a prompt somebody typed, and a
// page somebody's browser recorded. An assistant message is not one — that is
// the machine — and neither is a tool result or a subagent's prompt. So a
// session where the agent worked for forty minutes without being spoken to
// contributes five minutes of attention at each end and thirty of background,
// which is what the concept asks for and roughly what it feels like.
func attentionWindows(entries []entry, half time.Duration) windows {
	var w windows
	for _, en := range entries {
		if !isAttentionPoint(en.e) {
			continue
		}
		w.add(en.t.Add(-half), en.t.Add(half))
	}
	return w
}

func isAttentionPoint(e event.Event) bool {
	switch e.Source {
	case browser.SourceName:
		// Every visit. A page opened by a redirect or by a frame is not a
		// human act, but telling those apart needs each browser's own
		// vocabulary and they do not agree; on real data it is a rounding
		// error either way.
		return true
	case claudecode.SourceName:
		// The one event a person is known to have produced. RawText is the
		// short label the parser writes: "prompt" for a message somebody
		// typed, "tool_result" for one the harness fed back. A sidechain is a
		// subagent being told what to do, which is the machine talking to
		// itself.
		return e.Type == "user" && e.RawText == "prompt" && !e.IsSidechain
	}
	return false
}

// windows is a sorted, disjoint set of intervals. Attention points arrive in
// time order, so a new window either extends the last one or starts after it.
type windows struct {
	from []time.Time
	to   []time.Time
}

func (w *windows) add(from, to time.Time) {
	if n := len(w.from); n > 0 && !from.After(w.to[n-1]) {
		if to.After(w.to[n-1]) {
			w.to[n-1] = to
		}
		return
	}
	w.from = append(w.from, from)
	w.to = append(w.to, to)
}

// overlap is how much of [from, to) falls inside a window. It keeps no state
// between calls: the answer for an interval is the same whenever it is asked,
// which is what lets the same interval be counted into a project and into a
// block without the two disagreeing.
func (w *windows) overlap(from, to time.Time) time.Duration {
	i := sort.Search(len(w.from), func(i int) bool { return w.to[i].After(from) })
	var total time.Duration
	for ; i < len(w.from) && w.from[i].Before(to); i++ {
		lo, hi := from, to
		if w.from[i].After(lo) {
			lo = w.from[i]
		}
		if w.to[i].Before(hi) {
			hi = w.to[i]
		}
		if hi.After(lo) {
			total += hi.Sub(lo)
		}
	}
	return total
}

// blockProjects is the presence of each project inside one block: how many
// events name it once inheritance has run, and how many named it by
// themselves. The second number is what the purity of a block is measured on,
// and keeping both is the only way to see what inheritance changed.
func blockProjects(entries []entry) []ProjectCount {
	index := map[string]int{}
	var counts []ProjectCount
	for _, en := range entries {
		i, ok := index[en.project]
		if !ok {
			i = len(counts)
			index[en.project] = i
			counts = append(counts, ProjectCount{Name: en.project})
		}
		counts[i].Events++
		if en.own {
			counts[i].Own++
		}
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Events != counts[j].Events {
			return counts[i].Events > counts[j].Events
		}
		return lessName(counts[i].Name, counts[j].Name)
	})
	return counts
}

func sourceCounts(entries []entry) []SourceCount {
	index := map[string]int{}
	var counts []SourceCount
	for _, en := range entries {
		i, ok := index[en.e.Source]
		if !ok {
			i = len(counts)
			index[en.e.Source] = i
			counts = append(counts, SourceCount{Source: en.e.Source})
		}
		counts[i].Events++
	}
	sortSources(counts)
	return counts
}

func sortSources(counts []SourceCount) {
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Events != counts[j].Events {
			return counts[i].Events > counts[j].Events
		}
		return counts[i].Source < counts[j].Source
	})
}

// distinctProjects lists every project present, in a stable order.
func distinctProjects(entries []entry) []string {
	seen := map[string]bool{}
	var names []string
	for _, en := range entries {
		if seen[en.project] {
			continue
		}
		seen[en.project] = true
		names = append(names, en.project)
	}
	sort.Slice(names, func(i, j int) bool { return lessName(names[i], names[j]) })
	return names
}

// fillEvidence is the "on what grounds" column: how many events, from which
// sources, and — for the browser — under which host and first path segment.
//
// Browser events are grouped by host, port and first segment together rather
// than by host alone. One host serves several projects and the first segment
// is what separates them; a dev server is only told apart by its port.
// Wall is filled in here too: it is the span from a project's first event of
// the day to its last, which is the number four parallel chats would give you
// if their hours were simply added up.
func fillEvidence(p *Project, entries []entry) {
	sources := map[string]int{}
	keys := map[string]int{}
	var first, last time.Time
	for _, en := range entries {
		if en.project != p.Name {
			continue
		}
		if first.IsZero() {
			first = en.t
		}
		last = en.t
		p.Events++
		sources[en.e.Source]++
		if k := browserKey(en.e); k != "" {
			keys[k]++
		}
	}
	p.Wall = last.Sub(first)
	for source, n := range sources {
		p.Sources = append(p.Sources, SourceCount{Source: source, Events: n})
	}
	sortSources(p.Sources)
	for key, n := range keys {
		p.BrowserKeys = append(p.BrowserKeys, BrowserKey{Key: key, Visits: n})
	}
	sortBrowserKeys(p.BrowserKeys)
}

func sortBrowserKeys(keys []BrowserKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Visits != keys[j].Visits {
			return keys[i].Visits > keys[j].Visits
		}
		return keys[i].Key < keys[j].Key
	})
}

// browserKey is host[:port][/first-segment], the unit browsing is grouped by.
//
// The first segment is what the browser source stored, and what it stored is
// cut at the first character outside an allow-list taken from RFC 3986 §3.3.
// Two segments that differ only past such a character therefore arrive here as
// one key, and no amount of grouping can tell them apart again. A key that
// looks implausibly busy is a candidate for the domain dictionary rather than
// evidence that the cut is wrong.
func browserKey(e event.Event) string {
	if e.Source != browser.SourceName || e.Host == "" {
		return ""
	}
	key := e.Host
	if e.Port != "" {
		key += ":" + e.Port
	}
	if e.PathHead != "" {
		key += "/" + e.PathHead
	}
	return key
}

// distinct returns the non-empty entries, sorted and without repeats.
func distinct(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// sortProjects puts the busiest first and the unnamed row last. Project names
// are unique within a report, so the order is total: two runs cannot disagree.
func sortProjects(projects []Project) {
	sort.Slice(projects, func(i, j int) bool {
		a, b := projects[i], projects[j]
		if (a.Name == Unnamed) != (b.Name == Unnamed) {
			return b.Name == Unnamed
		}
		if a.Attention != b.Attention {
			return a.Attention > b.Attention
		}
		if a.Background != b.Background {
			return a.Background > b.Background
		}
		if a.Events != b.Events {
			return a.Events > b.Events
		}
		return a.Name < b.Name
	})
}

// lessName orders project names with the unnamed one last.
func lessName(a, b string) bool {
	if (a == Unnamed) != (b == Unnamed) {
		return b == Unnamed
	}
	return a < b
}

// aggregate sums the days. Attention and background add up because they are
// disjoint slices of real time; Span is added up too, but it is the sum of
// each day's first-to-last and means nothing more than that.
func aggregate(days []Day) Totals {
	var total Totals
	index := map[string]int{}
	for _, day := range days {
		total.Attention += day.Attention
		total.Background += day.Background
		total.Active += day.Active
		total.Span += day.Span
		total.Agent += day.Agent
		total.Padding += day.Padding
		total.OneNeighbour += day.OneNeighbour
		for _, p := range day.Projects {
			i, ok := index[p.Name]
			if !ok {
				i = len(total.Projects)
				index[p.Name] = i
				total.Projects = append(total.Projects, Project{Name: p.Name})
			}
			t := &total.Projects[i]
			t.Attention += p.Attention
			t.Background += p.Background
			t.Agent += p.Agent
			t.Wall += p.Wall
			t.Events += p.Events
			t.Sources = mergeSources(t.Sources, p.Sources)
			t.BrowserKeys = mergeKeys(t.BrowserKeys, p.BrowserKeys)
			t.Candidates = distinct(append(t.Candidates, p.Candidates...))
		}
	}
	sortProjects(total.Projects)
	return total
}

func mergeSources(into, from []SourceCount) []SourceCount {
	for _, s := range from {
		found := false
		for i := range into {
			if into[i].Source == s.Source {
				into[i].Events += s.Events
				found = true
				break
			}
		}
		if !found {
			into = append(into, s)
		}
	}
	sortSources(into)
	return into
}

func mergeKeys(into, from []BrowserKey) []BrowserKey {
	index := map[string]int{}
	for i, k := range into {
		index[k.Key] = i
	}
	for _, k := range from {
		if i, ok := index[k.Key]; ok {
			into[i].Visits += k.Visits
			continue
		}
		index[k.Key] = len(into)
		into = append(into, k)
	}
	sortBrowserKeys(into)
	return into
}
