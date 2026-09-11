// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package confirm is the day a person is asked to agree with.
//
// It is a document, not an interface. The terminal draws it and the flags
// change it, and neither of them knows anything the other does not — which is
// the whole of how "everything the TUI does is available as flags" is kept
// true. A rule that lived in the drawing code would be a rule with no flag,
// and it would be found out by somebody's day being wrong.
//
// Three things are in here:
//
//   - the queue of questions, built out of traces rather than out of blocks.
//     One uncovered key seen in eight places is one question: the answer
//     closes all eight, and without that the day does not fit in two minutes;
//   - the blocks, all of them. The queue is the fast way through what spoor
//     could not name; it is not the only way in, and a block spoor named
//     confidently has to be openable and changeable too;
//   - what a person has already said, folded back in, so that the numbers on
//     screen are the numbers that will be frozen.
package confirm

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"github.com/vadosdog/spoor-timetracker/internal/rules"
	"github.com/vadosdog/spoor-timetracker/internal/source/browser"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// TraceKind is which of the four kinds of rule a question is about.
type TraceKind string

const (
	// KindPath is a working directory. It and KindKey are the two a question
	// can be built from, because they are the two that can be listed: the
	// traces of a day are a finite set of directories and hosts.
	KindPath TraceKind = "path"
	// KindKey is a browser key: host, port and first path segment.
	KindKey TraceKind = "key"
	// KindTitle can carry a rule and cannot raise a question of its own — see
	// the note on questionsOf.
	KindTitle TraceKind = "title"
	// KindBranch is the same, one field along.
	KindBranch TraceKind = "branch"
)

// Trace is the thing a question is about and a rule is written on.
type Trace struct {
	Kind  TraceKind
	Value string
}

func (t Trace) String() string { return string(t.Kind) + ":" + t.Value }

// Place is one appearance of a trace in the day: where it was, and what was
// around it. The question is about the trace; the places are what a person
// looks at to answer it.
type Place struct {
	From, To time.Time
	// Project is what the block is called as things stand, and Origin is what
	// that name rests on.
	Project string
	Origin  report.Origin
	// Left and Right are the nearest named blocks on either side. Empty means
	// there is nothing on that side at all, which is not the same as a
	// neighbour that disagrees.
	Left, Right string
}

// Question is one trace to decide about.
type Question struct {
	Trace Trace
	// Folded is the traces this question stands for. A host whose segments do
	// not separate anything is one question, not fifteen: a rule on the host
	// closes every one of them, and asking fifteen times is how two minutes
	// becomes twenty.
	Folded []Trace

	// Events is how many traces carry it and Touches how many of those were a
	// person — a typed prompt or a page they opened. The second number is the
	// one that decides whether a rule here can ever collect time: a directory
	// with three thousand events and no touches is a directory an agent worked
	// in while its owner prompted from somewhere else, and as a subject it
	// would give exactly zero hours.
	Events, Touches int
	// Time is how much of the day happened around this trace, counted the way
	// `report --unmatched` counts it: the stretches that begin with one of its
	// events. It is not the report's time and does not add up to anything —
	// it answers "how much of the day turns on this", which is the question a
	// list to work through has to answer.
	Time time.Duration
	// FirstToday says the database has never seen this trace before today.
	FirstToday bool

	// CalledNow is what the trace counts for as things stand. Seventeen of
	// twenty-two uncovered keys measured were being inherited by one large
	// project, not because they were its but because it was the busiest thing
	// near them. Without this column a key that is already inherited correctly
	// is indistinguishable from one leaking into somebody else's project, and
	// they are not equally urgent.
	CalledNow string

	Places []Place

	// Project and Subject are the ranked candidates. Neither list is a guess:
	// every entry says where it came from, and the first is the default only
	// when the evidence is a block on both sides agreeing.
	Project []Candidate
	Subject []Candidate

	// Titles are the page titles seen on this trace, most common first. They
	// are what "name it by the title" is answered with — the answer that
	// exists because a key question does not always have a key answer. On a
	// repository host the third path segment is the repository and spoor does
	// not store it; the name is in the title and nowhere else.
	Titles []string
}

// Candidate is one thing the answer could be, and why it is on the list.
type Candidate struct {
	Name string
	// Why is where it came from, in words, for the column beside it.
	Why string
	// Default marks the one the cursor starts on. There is at most one, and
	// there is none at all when the evidence does not agree with itself.
	Default bool
}

// Encounter is one meeting with a trace already decided to be ambiguous.
//
// The trace was settled once and for all: no rule can name the subject here.
// What is left is this particular visit, which is a different question with a
// different answer, and the answer lives in the day rather than in the config.
type Encounter struct {
	Trace    Trace
	From, To time.Time
	Project  string
	// Around is the subject of the nearest touches on either side inside the
	// block. It is the default when both sides say the same thing.
	Around  string
	Choices []Candidate
}

// Block is one block of the day, whether or not spoor could name it.
type Block struct {
	From, To   time.Time
	Project    string
	Subject    string
	Origin     report.Origin
	Ground     report.Ground
	Events     int
	Attention  time.Duration
	Background time.Duration
	// Traces is what the block has to write a rule on. Empty means an answer
	// about this block cannot become a rule, which is what makes it a one-off.
	Traces []Trace
	// Evidence is the block's browser keys and directories, for the line under
	// it.
	Evidence []string
	// Choices and Subjects are what it could be called, on the same ladder as
	// everywhere else. A block screen that could only be answered by typing a
	// name was a screen where attaching a block to a project you already have
	// meant spelling it again.
	Choices  []Candidate
	Subjects []Candidate
}

// Background is the one question about measured background time: whether it
// counts. Never whether it was work — that is not something a trace can say,
// and guessing it is the one thing the concept forbids by name.
type Background struct {
	Total time.Duration
	// Long is the pieces worth answering about one at a time. There are only
	// ever a handful.
	Long []report.Stretch
	// Count is what the settings say now, and therefore the default.
	Count bool
}

// Window is a pause between two blocks of work, and the one question about it
// that nothing on this machine can answer: was that work.
//
// It is deliberately not called background. Background is a measured quantity —
// active time inside a block, outside every attention window — and this is the
// other thing entirely: the time between blocks, which no trace covers at all.
// A cigarette and a meeting look identical on disk.
//
// Nothing here changes a number. The answers are collected so that "should a
// pause be a record of its own" can be settled with data rather than with
// somebody's memory of a fortnight ago, and the screen that asks goes away
// with the table if the answer is no.
type Window struct {
	From, To time.Time
	// Left and Right are what was being worked on either side. The signal the
	// question is about is whether those two agreeing predicts the answer.
	Left, Right string
	// Answered and Worked are what was said about it, if anything, and
	// Project and Subject are what it was called when it was work.
	Answered bool
	Worked   bool
	Project  string
	Subject  string

	// Choices and Subjects are what it could be called. The same ladder as
	// everywhere else: what is on either side first, then the day, then the
	// dictionary.
	//
	// An answer here **never becomes a rule** and the screen says so. A pause
	// has no trace at all — that is what makes it a pause — so there is
	// nothing to write a rule on, and the question "what is this edit" that
	// every other correction is asked has one possible answer here.
	Choices  []Candidate
	Subjects []Candidate
}

// Duration is how long the pause is.
func (w Window) Duration() time.Duration { return w.To.Sub(w.From) }

// Budget is the header: how much there is to do.
type Budget struct {
	Questions     int
	UnderQuestion time.Duration
	InTheDay      time.Duration
}

// Document is a day, ready to be agreed with.
type Document struct {
	Date   time.Time
	Day    report.Day
	Budget Budget

	Questions  []Question
	Encounters []Encounter
	Blocks     []Block
	Background Background
	// Windows are the pauses between blocks, with whatever has been said about
	// them. They are not part of the budget: nothing about a day depends on
	// them, and a question that moves no measured number has no business making the
	// day look unfinished.
	Windows []Window

	// Confirmed is set when this day has already been frozen. RulesMoved says
	// the dictionary has changed since then, which is the one thing a frozen
	// day cannot notice on its own.
	Confirmed  bool
	RulesMoved bool
	// OneOffs is how many of the answers already given could not become rules.
	OneOffs int
}

// Input is everything needed to build one.
type Input struct {
	Date        time.Time // local midnight
	Events      []event.Event
	Rules       *rules.Rules
	Attribution config.Attribution
	Options     report.Options
	// Answered is what has already been said about this day's pauses.
	Answered []store.WindowAnswer
	// Seen is the traces the database has seen before today, for "first time
	// today". Empty is a fair answer — it only ever removes a note.
	Seen map[string]bool
	// Projects is every project the dictionary knows, in the order the config
	// writes them. They are the tail of the candidate list: something to pick
	// from when the day itself suggests nothing.
	Projects []string
	// Subjects maps a project to the subjects written under it.
	Subjects map[string][]string
}

// Build turns a day into the document.
func Build(in Input) Document {
	end := in.Date.AddDate(0, 0, 1)
	rep := report.Build(in.Events, in.Date, end, in.Options)
	doc := Document{Date: in.Date}
	if len(rep.Days) > 0 {
		doc.Day = rep.Days[0]
	}

	doc.Blocks = blocksOf(doc.Day)
	fillBlockChoices(doc.Blocks, doc.Day, in)
	doc.Questions = questionsOf(in, doc.Day)
	doc.Encounters = encountersOf(in, doc.Day)
	doc.Background = backgroundOf(doc.Day, in.Options)
	doc.Windows = windowsOf(doc.Day, in.Answered, in)
	for _, a := range in.Options.Manual {
		if a.OneOff {
			doc.OneOffs++
		}
	}

	doc.Budget = Budget{Questions: len(doc.Questions) + len(doc.Encounters), InTheDay: doc.Day.Active}
	for _, q := range doc.Questions {
		doc.Budget.UnderQuestion += q.Time
	}
	for _, e := range doc.Encounters {
		doc.Budget.UnderQuestion += e.To.Sub(e.From)
	}
	return doc
}

// blocksOf is every block of the day, named or not. The queue is a shortcut
// through the unnamed ones; this is the list that makes "any block can be
// changed" true, and without it the stage is not done.
func blocksOf(day report.Day) []Block {
	out := make([]Block, 0, len(day.Clusters))
	for _, c := range day.Clusters {
		b := Block{
			From: c.From, To: c.To, Project: c.Project, Origin: c.Origin,
			Events: c.Events, Attention: c.Attention, Background: c.Background,
		}
		if b.Project == "" && len(c.Projects) > 0 {
			// A block holding two projects has no single name. The busiest one
			// is shown with the others counted beside it rather than being
			// promoted: handing a mixed block to its largest project is the
			// thing the cutting rule exists not to do.
			b.Project = c.Projects[0].Name
		}
		for _, s := range day.Stretches {
			if !s.From.Before(c.From) && !s.To.After(c.To) {
				b.Ground = s.Ground
				b.Subject = s.Subject
				break
			}
		}
		out = append(out, b)
	}
	fillBlockTraces(out, day)
	return out
}

// fillBlockChoices gives every block the same two lists a question has: what
// it could be called, and what inside that it could be.
func fillBlockChoices(blocks []Block, day report.Day, in Input) {
	byTime := append([]report.Project(nil), day.Projects...)
	sort.SliceStable(byTime, func(i, j int) bool {
		return byTime[i].Attention > byTime[j].Attention
	})
	for i := range blocks {
		seen := map[string]bool{}
		add := func(name, why string, isDefault bool) {
			if name == "" || seen[name] {
				return
			}
			seen[name] = true
			blocks[i].Choices = append(blocks[i].Choices,
				Candidate{Name: name, Why: why, Default: isDefault})
		}
		add(blocks[i].Project, "what it is called now", true)
		for _, p := range byTime {
			add(p.Name, fmt.Sprintf("a project of today, %s", hm(p.Attention)), false)
		}
		for _, name := range in.Projects {
			add(name, "in the dictionary", false)
		}
		for _, c := range blocks[i].Choices {
			for _, name := range in.Subjects[c.Name] {
				blocks[i].Subjects = append(blocks[i].Subjects,
					Candidate{Name: name, Why: "in " + c.Name})
			}
		}
	}
}

// fillBlockTraces says what each block could carry a rule on. A block with
// nothing here can still be answered, and the answer cannot become a rule —
// which is exactly what a one-off is and why it is counted.
func fillBlockTraces(blocks []Block, day report.Day) {
	for i := range blocks {
		seen := map[string]bool{}
		for _, r := range day.Runs {
			if r.Gap || r.From.Before(blocks[i].From) || r.To.After(blocks[i].To) {
				continue
			}
			for _, k := range r.BrowserKeys {
				if seen[k.Key] {
					continue
				}
				seen[k.Key] = true
				blocks[i].Traces = append(blocks[i].Traces, Trace{KindKey, k.Key})
				blocks[i].Evidence = append(blocks[i].Evidence, k.Key)
			}
		}
	}
}

// questionsOf builds the queue.
//
// The raw material is what `report --unmatched` already computes for the day:
// the directories and browser keys no rule mentions, with how much of the day
// happens around each. That is not a coincidence and not reuse for its own
// sake — the discovery list and the queue answer the same question, and two
// implementations of it would drift apart and then disagree in front of
// somebody trying to close their day.
//
// Titles and branches raise no questions of their own. A branch that names
// something already names the directory it is checked out in, and titles are
// not a list: they are one string per page, and a question per page is the
// marking-as-you-go this project refuses. Both can still be *answered* with —
// see Titles on Question.
func questionsOf(in Input, day report.Day) []Question {
	var out []Question
	for _, u := range day.Unmatched {
		q := Question{
			Events:    u.Events,
			Time:      u.Time,
			CalledNow: u.Project,
		}
		switch {
		case u.Path != "":
			q.Trace = Trace{KindPath, u.Path}
		case u.Key != "":
			q.Trace = Trace{KindKey, u.Key}
		default:
			continue
		}
		q.FirstToday = !in.Seen[q.Trace.String()]
		out = append(out, q)
	}
	out = foldByHost(out)
	fillEvidence(out, in, day)
	for i := range out {
		out[i].Project = projectCandidates(out[i], in, day)
		out[i].Subject = subjectCandidates(out[i], in)
	}
	// Busiest first: the line worth writing next is the one that costs the
	// most day. The order is total, so two runs of the same day agree.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time > out[j].Time
		}
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Trace.String() < out[j].Trace.String()
	})
	return out
}

// foldByHost turns several uncovered keys of one host into one question.
//
// "One uncovered trace, one question" is right about traces and wrong about
// what a trace is: a browser key is a host, a port and a first path segment,
// so fifteen segments of one host are fifteen questions about ninety minutes.
// Where the segments separate nothing — no rule mentions any of them — the
// question is about the host, and the answer is a rule on the host.
//
// The port stays out of the fold. A port is what tells one dev server from
// another, which is the whole reason it is stored.
func foldByHost(qs []Question) []Question {
	type group struct {
		at    int
		count int
	}
	groups := map[string]*group{}
	var out []Question
	for _, q := range qs {
		if q.Trace.Kind != KindKey {
			out = append(out, q)
			continue
		}
		host, segment := splitKey(q.Trace.Value)
		if segment == "" {
			// Already a question about the host itself. Anything else on that
			// host folds into it rather than beside it.
			if g, ok := groups[host]; ok {
				out[g.at] = merge(out[g.at], q, host)
				continue
			}
			groups[host] = &group{at: len(out), count: 1}
			out = append(out, q)
			continue
		}
		if g, ok := groups[host]; ok {
			g.count++
			out[g.at] = merge(out[g.at], q, host)
			continue
		}
		groups[host] = &group{at: len(out), count: 1}
		out = append(out, q)
	}
	return out
}

// merge folds one key question into another and rewrites the pair as a
// question about the host they share.
func merge(into, from Question, host string) Question {
	if into.Trace.Value != host {
		into.Folded = append(into.Folded, into.Trace)
		into.Trace = Trace{KindKey, host}
	}
	into.Folded = append(into.Folded, from.Trace)
	into.Events += from.Events
	into.Time += from.Time
	into.FirstToday = into.FirstToday && from.FirstToday
	if into.CalledNow == "" {
		into.CalledNow = from.CalledNow
	}
	return into
}

// splitKey takes a browser key apart into the host with its port and the first
// path segment. An address literal is the one host with colons of its own, so
// the bracketed form is kept whole.
func splitKey(key string) (host, segment string) {
	if i := strings.Index(key, "/"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

// fillEvidence walks the day once and fills in, for every question, where its
// trace turned up, how many of those were a person, and what the pages were
// called.
func fillEvidence(qs []Question, in Input, day report.Day) {
	if len(qs) == 0 {
		return
	}
	index := map[string]int{}
	for i, q := range qs {
		index[q.Trace.String()] = i
		for _, f := range q.Folded {
			index[f.String()] = i
		}
	}
	titles := make([]map[string]int, len(qs))

	for _, e := range in.Events {
		i, ok := index[traceOf(e).String()]
		if !ok {
			continue
		}
		if isTouch(e) {
			qs[i].Touches++
		}
		if e.Title != "" {
			if titles[i] == nil {
				titles[i] = map[string]int{}
			}
			titles[i][e.Title]++
		}
	}
	for i := range qs {
		qs[i].Titles = mostCommon(titles[i])
	}

	// Where in the day, with what on either side. Blocks rather than events:
	// a person answering looks at a piece of their afternoon, not at a row.
	at := map[int][]Place{}
	for ci, c := range day.Clusters {
		left, right := neighbours(day.Clusters, ci)
		for _, r := range day.Runs {
			if r.Gap || r.From.Before(c.From) || r.To.After(c.To) {
				continue
			}
			for _, k := range r.BrowserKeys {
				if i, ok := index[(Trace{KindKey, k.Key}).String()]; ok {
					at[i] = appendPlace(at[i], Place{
						From: c.From, To: c.To, Project: c.Project,
						Origin: c.Origin, Left: left, Right: right,
					})
				}
			}
		}
	}
	// Directories do not appear in a run's evidence, so they are walked from
	// the events themselves.
	for _, e := range in.Events {
		t := traceOf(e)
		if t.Kind != KindPath {
			continue
		}
		i, ok := index[t.String()]
		if !ok {
			continue
		}
		when, err := time.Parse(time.RFC3339, e.TS)
		if err != nil {
			continue
		}
		when = when.In(in.Date.Location())
		for ci, c := range day.Clusters {
			if when.Before(c.From) || when.After(c.To) {
				continue
			}
			left, right := neighbours(day.Clusters, ci)
			at[i] = appendPlace(at[i], Place{
				From: c.From, To: c.To, Project: c.Project,
				Origin: c.Origin, Left: left, Right: right,
			})
			break
		}
	}
	for i := range qs {
		qs[i].Places = at[i]
	}
}

func appendPlace(list []Place, p Place) []Place {
	for _, got := range list {
		if got.From.Equal(p.From) {
			return list
		}
	}
	return append(list, p)
}

// neighbours is the nearest named block before and after, found by walking
// outwards. A run of unnamed blocks between two named ones is decided by those
// two rather than by each other — the same rule the report attributes by.
func neighbours(clusters []report.Cluster, at int) (before, after string) {
	for i := at - 1; i >= 0; i-- {
		if n := clusters[i].Project; n != "" {
			before = n
			break
		}
	}
	for i := at + 1; i < len(clusters); i++ {
		if n := clusters[i].Project; n != "" {
			after = n
			break
		}
	}
	return before, after
}

// projectCandidates is the ladder: what could name this, strongest first.
//
// Nothing here is a guess. Every entry says what it rests on, and the default
// is only set where the evidence agrees with itself — a block on both sides
// saying the same thing. Two neighbours that disagree put both on the list and
// neither under the cursor, because choosing between two projects on somebody's
// behalf is the thing this tool is not for.
func projectCandidates(q Question, in Input, day report.Day) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(name, why string, isDefault bool) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, Candidate{Name: name, Why: why, Default: isDefault})
	}

	agreed, oneSided, disagreeing := "", "", []string{}
	for _, p := range q.Places {
		switch {
		case p.Left != "" && p.Left == p.Right:
			agreed = p.Left
		case p.Left != "" && p.Right == "":
			oneSided = p.Left
		case p.Right != "" && p.Left == "":
			oneSided = p.Right
		case p.Left != "" && p.Right != "":
			disagreeing = append(disagreeing, p.Left, p.Right)
		}
		if p.Project != "" && p.Origin == report.OriginOwn {
			add(p.Project, "the block it is in", agreed == "")
		}
	}
	add(agreed, "both neighbours", true)
	add(oneSided, "one neighbour, nothing on the other side", len(out) == 0)
	for _, name := range disagreeing {
		add(name, "a neighbour, and the other one disagrees", false)
	}

	// The day's own projects, busiest first. Not evidence about this trace —
	// a list to pick from, and labelled as one.
	byTime := append([]report.Project(nil), day.Projects...)
	sort.SliceStable(byTime, func(i, j int) bool {
		return byTime[i].Attention > byTime[j].Attention
	})
	for _, p := range byTime {
		if p.Name == "" {
			continue
		}
		add(p.Name, fmt.Sprintf("a project of today, %s", hm(p.Attention)), false)
	}
	// And everything else the dictionary knows, in the order it is written in.
	for _, name := range in.Projects {
		add(name, "in the dictionary", false)
	}
	return out
}

// subjectCandidates is the same list one level down: the subjects written under
// the project that is about to be chosen, and the ones under every project the
// trace has been near.
func subjectCandidates(q Question, in Input) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, p := range q.Project {
		for _, s := range in.Subjects[p.Name] {
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, Candidate{Name: s, Why: "in " + p.Name})
		}
	}
	return out
}

// encountersOf is the short kind of question: one visit to a trace already
// decided to be one no rule can name.
//
// They come last in the queue. They are cheaper than the others and skipping
// one breaks nothing — the time simply stays where it was.
func encountersOf(in Input, day report.Day) []Encounter {
	if in.Rules == nil {
		return nil
	}
	split := in.Attribution.Ambiguous.Split()
	var out []Encounter
	for _, s := range day.Stretches {
		if s.Kind == report.KindPadding || s.Project == "" {
			continue
		}
		e, ok := eventAt(in, s.From)
		if !ok {
			continue
		}
		ambiguous, trace := in.Rules.Ambiguous(e)
		if !ambiguous {
			continue
		}
		// A short visit is not a distraction at all: the person carried on
		// with what they were doing, and the time stays with it. The threshold
		// is one person's measurement of their own days and it is in the
		// config, where 0 means zero rather than "use the default".
		if s.Duration() < split {
			continue
		}
		enc := Encounter{
			Trace:   parseTrace(trace),
			From:    s.From,
			To:      s.To,
			Project: s.Project,
			Around:  around(day, s),
		}
		if enc.Around != "" {
			enc.Choices = append(enc.Choices, Candidate{
				Name: enc.Around, Why: "what you were on either side", Default: true,
			})
		}
		for _, name := range in.Subjects[s.Project] {
			if name == enc.Around {
				continue
			}
			enc.Choices = append(enc.Choices, Candidate{Name: name, Why: "a subject of " + s.Project})
		}
		out = append(out, enc)
	}
	return out
}

// around is the subject of the stretches either side of this one, when they
// agree. It is the default answer and nothing more: the person sees what it
// rests on and picks.
func around(day report.Day, at report.Stretch) string {
	before, after := "", ""
	for i, s := range day.Stretches {
		if !s.From.Equal(at.From) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if day.Stretches[j].Subject != "" {
				before = day.Stretches[j].Subject
				break
			}
		}
		for j := i + 1; j < len(day.Stretches); j++ {
			if day.Stretches[j].Subject != "" {
				after = day.Stretches[j].Subject
				break
			}
		}
		break
	}
	if before != "" && before == after {
		return before
	}
	return ""
}

func eventAt(in Input, at time.Time) (event.Event, bool) {
	for _, e := range in.Events {
		when, err := time.Parse(time.RFC3339, e.TS)
		if err != nil {
			continue
		}
		if when.In(at.Location()).Equal(at) {
			return e, true
		}
	}
	return event.Event{}, false
}

func parseTrace(s string) Trace {
	if i := strings.Index(s, ":"); i >= 0 {
		return Trace{TraceKind(s[:i]), s[i+1:]}
	}
	return Trace{KindKey, s}
}

// backgroundOf is the one decision about measured background: count it or not.
//
// One question for the day, and the pieces longer than a quarter of an hour
// separately, because there are only ever a handful of those and they are the
// ones somebody might answer differently.
func backgroundOf(day report.Day, opts report.Options) Background {
	b := Background{Total: day.Background, Count: opts.CountBackground}
	for _, s := range day.Stretches {
		if s.Kind == report.KindBackground && s.Duration() > 15*time.Minute {
			b.Long = append(b.Long, s)
		}
	}
	return b
}

// covering finds an answer that spans a pause, for a pause whose boundaries
// have moved since it was answered.
func covering(answered []store.WindowAnswer, from, to time.Time) (store.WindowAnswer, bool) {
	for _, a := range answered {
		if !a.From.After(from) && !a.To.Before(to) {
			return a, true
		}
	}
	return store.WindowAnswer{}, false
}

// windowsOf is the day's pauses, in order, with whatever was said about each.
//
// They come out of the same runs the timeline is printed from, so a pause on
// this screen is a pause on that one — there is no second definition of what a
// gap is, which is how two definitions of anything start disagreeing.
func windowsOf(day report.Day, answered []store.WindowAnswer, in Input) []Window {
	said := map[string]store.WindowAnswer{}
	for _, a := range answered {
		said[a.From.UTC().Format(time.RFC3339)+"/"+a.To.UTC().Format(time.RFC3339)] = a
	}
	var out []Window
	for i, r := range day.Runs {
		if !r.Gap {
			continue
		}
		w := Window{From: r.From, To: r.To}
		for j := i - 1; j >= 0; j-- {
			if !day.Runs[j].Gap && day.Runs[j].Project != "" {
				w.Left = day.Runs[j].Project
				break
			}
		}
		for j := i + 1; j < len(day.Runs); j++ {
			if !day.Runs[j].Gap && day.Runs[j].Project != "" {
				w.Right = day.Runs[j].Project
				break
			}
		}
		// An answer for exactly this pause, or failing that one that covers
		// it. Block boundaries move whenever a setting does or an import
		// lands, and an answer matched only on exact boundaries becomes
		// invisible the moment they shift — it still counts, and there is no
		// longer any way to see it or change it.
		key := w.From.UTC().Format(time.RFC3339) + "/" + w.To.UTC().Format(time.RFC3339)
		if a, ok := said[key]; ok {
			w.Answered, w.Worked = true, a.Worked
			w.Project, w.Subject = a.Project, a.Subject
		} else if a, ok := covering(answered, w.From, w.To); ok {
			w.Answered, w.Worked = true, a.Worked
			w.Project, w.Subject = a.Project, a.Subject
		}
		w.Choices = pauseCandidates(w, day, in)
		for _, c := range w.Choices {
			for _, name := range in.Subjects[c.Name] {
				w.Subjects = append(w.Subjects, Candidate{Name: name, Why: "in " + c.Name})
			}
		}
		out = append(out, w)
	}
	return out
}

// pauseCandidates is what a pause could be called: what was on either side
// first, then the projects of the day, then the dictionary. The same ladder as
// a question about a trace, and for the same reason — a proposal has to say
// where it came from or it is a guess.
func pauseCandidates(w Window, day report.Day, in Input) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(name, why string, isDefault bool) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, Candidate{Name: name, Why: why, Default: isDefault})
	}
	if w.Left != "" && w.Left == w.Right {
		add(w.Left, "on both sides of it", true)
	} else {
		add(w.Left, "before it", false)
		add(w.Right, "after it", false)
	}
	byTime := append([]report.Project(nil), day.Projects...)
	sort.SliceStable(byTime, func(i, j int) bool {
		return byTime[i].Attention > byTime[j].Attention
	})
	for _, p := range byTime {
		add(p.Name, fmt.Sprintf("a project of today, %s", hm(p.Attention)), false)
	}
	for _, name := range in.Projects {
		add(name, "in the dictionary", false)
	}
	return out
}

// traceOf is the trace an event would raise a question about — the same one
// `report --unmatched` groups it by, so that the queue and the discovery list
// cannot disagree about what a trace is.
func traceOf(e event.Event) Trace {
	if key := browserKey(e); key != "" {
		return Trace{KindKey, key}
	}
	if cwd := strings.TrimRight(e.CWD, "/"); cwd != "" {
		return Trace{KindPath, cwd}
	}
	return Trace{}
}

func browserKey(e event.Event) string {
	if e.Source != browser.SourceName || e.Host == "" {
		return ""
	}
	key := e.Host
	if strings.Contains(key, ":") {
		key = "[" + key + "]"
	}
	if e.Port != "" {
		key += ":" + e.Port
	}
	if e.PathHead != "" {
		key += "/" + e.PathHead
	}
	return key
}

// isTouch is a person: a prompt somebody typed or a page their browser
// recorded. The count of these is what says whether a rule here can ever
// collect time.
func isTouch(e event.Event) bool {
	switch e.Source {
	case browser.SourceName:
		return true
	default:
		return e.Type == "user" && e.RawText == "prompt" && !e.IsSidechain
	}
}

func mostCommon(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	out := make([]string, 0, len(counts))
	for k := range counts {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// hm is the report's formatter, not a copy of it: a length printed here and
// the same length printed by `report` have to be the same string.
func hm(d time.Duration) string { return report.HM(d) }
