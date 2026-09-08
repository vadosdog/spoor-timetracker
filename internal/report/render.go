// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// keysInTable is how many browser keys the table shows per project. The rest
// are counted out loud rather than dropped quietly, and --json holds all of
// them: that list is the raw material of the domain dictionary.
const keysInTable = 3

// candidatesInTable is how many neighbour suggestions the unnamed row spells
// out before it starts counting them instead.
const candidatesInTable = 3

// DefaultMinRow is the row below which a project is folded into a summary
// line instead of getting one of its own. Measured on real data: 44 of 80
// rows across a fortnight of daily reports held less than five minutes each —
// real, and noise at the same time.
//
// Folded, not hidden: the line says how many projects went into it and carries
// their time, so the column still adds up to the total printed underneath it.
// `--min=0` prints every row.
const DefaultMinRow = 5 * time.Minute

// unnamedLabel is what the row for time no rule could name is called. It is
// not a project called "unnamed" and it should not read like one.
const unnamedLabel = "(no project)"

// backgroundLabel names the agent's own time. Neutral on purpose: the tool
// does not know whether the agent grinding away for forty minutes was you
// working or you at lunch, and calling it "idle" would be a guess dressed up
// as a measurement.
const backgroundLabel = "agent worked in the background"

// RenderTable writes the report as a table for a terminal.
func RenderTable(w io.Writer, r Report) error {
	b := &strings.Builder{}
	fmt.Fprintln(b, heading(r))
	fmt.Fprintln(b)

	if r.Total.Active == 0 && r.Total.Projects == nil {
		fmt.Fprintln(b, "No traces in this range. Has `spoor ingest` run?")
		_, err := io.WriteString(w, b.String())
		return err
	}

	writeProjects(b, r.Total.Projects, r.MinRow)
	fmt.Fprintln(b)
	writeSummary(b, r)

	switch {
	case r.Timeline:
		fmt.Fprintln(b)
		writeTimeline(b, r.Days)
	case len(r.Days) > 1:
		fmt.Fprintln(b)
		writeByDay(b, r.Days)
	}
	if names := ranWithoutYou(r.Total.Projects); len(names) > 0 {
		fmt.Fprintf(b, "\n%s spent time with an agent working and none with you. "+
			"Nothing launches an agent but a person, so either something ran unattended, "+
			"or one chat window is being counted under several names — a shell command that "+
			"changes directory is enough to do that. See project_from in --json.\n",
			strings.Join(names, ", "))
	}
	if r.Unreadable > 0 {
		fmt.Fprintf(b, "\n%d events have a timestamp this build cannot read and are left out.\n", r.Unreadable)
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func heading(r Report) string {
	last := r.To.AddDate(0, 0, -1)
	if len(r.Days) == 1 {
		d := r.Days[0]
		h := fmt.Sprintf("day %s %s", d.Date.Format(time.DateOnly), d.Date.Format("Monday"))
		if !d.First.IsZero() {
			h += fmt.Sprintf(" — traces %s to %s", d.First.Format("15:04"), d.Last.Format("15:04"))
		}
		return h
	}
	return fmt.Sprintf("%s to %s — %d days",
		r.From.Format(time.DateOnly), last.Format(time.DateOnly), len(r.Days))
}

func writeProjects(b *strings.Builder, projects []Project, fold time.Duration) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tATTENTION\tBACKGROUND\tAGENT\tWALL\tON WHAT GROUNDS")

	var folded []Project
	for _, p := range projects {
		// Small on every count, or it stays: a project with no attention and
		// two hours of agent time is the opposite of noise.
		if fold > 0 && p.Attention < fold && p.Background < fold && p.Agent < fold {
			folded = append(folded, p)
			continue
		}
		name := p.Name
		if name == Unnamed {
			name = unnamedLabel
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			name, hm(p.Attention), hm(p.Background), hm(p.Agent), hm(p.Wall), grounds(p))
	}
	if len(folded) > 0 {
		writeFolded(tw, folded, fold)
	}
	_ = tw.Flush()
}

// writeFolded is the one line the small projects become. It carries their
// time, so the column above it still sums to the total below it, and it names
// as many of them as fit rather than saying only how many there were.
//
// WALL is left empty on purpose: it is the span from a project's first event
// to its last, and adding several of those together means nothing at all.
func writeFolded(tw io.Writer, folded []Project, fold time.Duration) {
	var attention, background, agent time.Duration
	var events int
	names := make([]string, 0, len(folded))
	for _, p := range folded {
		attention += p.Attention
		background += p.Background
		agent += p.Agent
		events += p.Events
		if p.Name == Unnamed {
			names = append(names, unnamedLabel)
		} else {
			names = append(names, p.Name)
		}
	}

	listed := names
	if len(listed) > foldedNames {
		listed = listed[:foldedNames]
	}
	grounds := fmt.Sprintf("%d events: %s", events, strings.Join(listed, ", "))
	if rest := len(names) - len(listed); rest > 0 {
		grounds += fmt.Sprintf(" and %d more", rest)
	}
	grounds += " — pass --min=0 to give each a row of its own"

	fmt.Fprintf(tw, "%d under %s\t%s\t%s\t%s\t%s\t%s\n",
		len(folded), short(fold), hm(attention), hm(background), hm(agent), "-", grounds)
}

// foldedNames is how many of the folded projects the line spells out.
const foldedNames = 4

// short prints a threshold the way a person would type it: "5m" rather than
// "5m0s", "1h" rather than "1h0m0s".
//
// Only a whole unit is dropped, which is the whole difficulty. Trimming the
// text "0s" off "20m0s" leaves "20m", and trimming "0m" off that leaves "2" —
// a threshold of twenty minutes printed as two. So each trim has to look at
// what it left behind and keep it only if a unit ends it.
func short(d time.Duration) string {
	s := d.String()
	if t := strings.TrimSuffix(s, "0s"); t != s && (strings.HasSuffix(t, "m") || strings.HasSuffix(t, "h")) {
		s = t
	}
	if t := strings.TrimSuffix(s, "0m"); t != s && strings.HasSuffix(t, "h") {
		s = t
	}
	return s
}

// grounds is the evidence column: which sources produced this project's
// events, which browser keys they landed under, and — for the unnamed row —
// which projects its neighbours suggested without agreeing.
func grounds(p Project) string {
	var parts []string
	for _, s := range p.Sources {
		parts = append(parts, fmt.Sprintf("%s %d", s.Source, s.Events))
	}
	out := strings.Join(parts, ", ")

	if len(p.BrowserKeys) > 0 {
		var keys []string
		for i, k := range p.BrowserKeys {
			if i == keysInTable {
				keys = append(keys, fmt.Sprintf("+%d more in --json", len(p.BrowserKeys)-keysInTable))
				break
			}
			keys = append(keys, fmt.Sprintf("%s %d", k.Key, k.Visits))
		}
		out += "; " + strings.Join(keys, ", ")
	}

	// Over a week the unnamed row collects the suggestions of every block in
	// it, and a list of a dozen projects joined by "or" says nothing at all.
	// Each block still has at most two; --json keeps them per block.
	switch {
	case len(p.Candidates) == 0:
	case len(p.Candidates) == 1:
		out += "; one neighbour only: " + p.Candidates[0]
	case len(p.Candidates) <= candidatesInTable:
		out += "; neighbours disagree: " + strings.Join(p.Candidates, " or ")
	default:
		out += fmt.Sprintf("; neighbours disagree across several blocks: %s and %d more in --json",
			strings.Join(p.Candidates[:candidatesInTable], ", "),
			len(p.Candidates)-candidatesInTable)
	}
	return out
}

// ranWithoutYou finds projects whose agent worked and whose owner never did.
//
// That combination cannot happen: an agent does not start itself. Either
// something ran unattended, or — far more likely — the project is one window
// under a second name, because Claude Code moves its own `cwd` when a shell
// command does and the project is still `basename(cwd)` until the dictionary
// arrives. Worth saying out loud rather than leaving as a row of dashes the
// reader has to interpret.
func ranWithoutYou(projects []Project) []string {
	var names []string
	for _, p := range projects {
		if p.Name != Unnamed && p.Agent > 0 && p.Attention == 0 && p.Background == 0 {
			names = append(names, p.Name)
		}
	}
	return names
}

func writeSummary(b *strings.Builder, r Report) {
	t := r.Total
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	if r.Options.CountBackground {
		fmt.Fprintf(tw, "counted\t%s\tattention plus the background below\n", hm(t.Attention+t.Background))
	}
	fmt.Fprintf(tw, "attention\t%s\t%s\n", hm(t.Attention), "the column above, summed")
	fmt.Fprintf(tw, "%s\t%s\t%s\n", backgroundLabel, hm(t.Background), backgroundNote(r.Options))
	if t.Padding > 0 {
		fmt.Fprintf(tw, "of which written and read\t%s\t%s\n", hm(t.Padding),
			"added by --head and --tail, which are an estimate rather than a measurement")
	}
	if t.Agent > 0 {
		fmt.Fprintf(tw, "agent worked while you were elsewhere\t%s\t%s\n", hm(t.Agent),
			"summed per project — two agents can be busy in the same second, so this is not part of the day")
	}
	fmt.Fprintf(tw, "active\t%s\t%s\n", hm(t.Active), coverage(t))
	if t.OneNeighbour > 0 {
		fmt.Fprintf(tw, "of which named by one neighbour\t%s\t%s\n", hm(t.OneNeighbour),
			"browsing with a named block on one side only and nothing on the other")
	}
	_ = tw.Flush()
}

func backgroundNote(o Options) string {
	if o.CountBackground {
		return "counted, because --count-background was given"
	}
	return "not counted — pass --count-background to add it"
}

// coverage says how much of the day the blocks of work actually fill.
//
// The denominator runs from the start of the first block to the end of the
// last rather than over 24 hours: nobody is asking what share of their sleep
// was productive. Block rather than trace, because a block starts before its
// first event and ends after its last — otherwise a day made of one block
// would come out at more than 100%.
func coverage(t Totals) string {
	if t.Span == 0 {
		return ""
	}
	return fmt.Sprintf("of %s from the first block to the last — %.0f%% covered",
		hm(t.Span), 100*float64(t.Active)/float64(t.Span))
}

func writeByDay(b *strings.Builder, days []Day) {
	fmt.Fprintln(b, "by day")
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, d := range days {
		traces := "no traces"
		if !d.First.IsZero() {
			traces = fmt.Sprintf("%s to %s", d.First.Format("15:04"), d.Last.Format("15:04"))
		}
		fmt.Fprintf(tw, "  %s %s\tattention %s\tbackground %s\t%s\n",
			d.Date.Format("Mon"), d.Date.Format(time.DateOnly),
			hm(d.Attention), hm(d.Background), traces)
	}
	_ = tw.Flush()
}

// writeTimeline prints the day as a schedule: from when to when, on what.
//
// This is the report read the other way round. The table says how the hours
// divide up; the timeline says when they happened, and — because the pauses
// between blocks are printed as pauses rather than skipped — how much of the
// day is not in the table at all.
func writeTimeline(b *strings.Builder, days []Day) {
	fmt.Fprintln(b, "timeline")
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, d := range days {
		if len(days) > 1 {
			if len(d.Runs) == 0 {
				continue
			}
			fmt.Fprintf(tw, "  %s %s\t\t\t\n", d.Date.Format("Mon"), d.Date.Format(time.DateOnly))
		}
		for _, run := range d.Runs {
			name := run.Project
			switch {
			case run.Gap:
				name = "— nothing —"
			case name == Unnamed:
				name = unnamedLabel
			}
			fmt.Fprintf(tw, "  %s-%s\t%s\t%s\t%s\n",
				run.From.Format("15:04"), run.To.Format("15:04"),
				hm(run.To.Sub(run.From)), name, runGrounds(run))
		}
	}
	_ = tw.Flush()
}

// runGrounds is the evidence for one line of the schedule, and it says when
// the line is the agent rather than you: a stretch you spent away from the
// keyboard reads the same as one you spent at it unless it is labelled.
func runGrounds(run Run) string {
	if run.Gap {
		return "not counted"
	}
	var parts []string
	for _, s := range run.Sources {
		parts = append(parts, fmt.Sprintf("%s %d", s.Source, s.Events))
	}
	for i, k := range run.BrowserKeys {
		if i == 2 {
			parts = append(parts, fmt.Sprintf("+%d more", len(run.BrowserKeys)-2))
			break
		}
		parts = append(parts, k.Key)
	}
	out := strings.Join(parts, ", ")
	// Only when it is worth saying. A handful of seconds on the line is the
	// arithmetic showing through, not something about the day.
	if run.Background >= time.Minute {
		out += fmt.Sprintf(" — %s of it the agent alone", hm(run.Background))
	}
	return out
}

// hm formats a duration as hours and minutes. Seconds are never shown and
// never rounded up: a report claiming a minute that was not there is the
// failure this whole tool exists to avoid.
//
// Something shorter than a minute prints as "<1m" rather than "0:00". Both are
// true and only one is useful — "0:00" next to a time range that plainly is
// not empty reads as a bug, and rounding it up to "0:01" would be the thing
// this function exists not to do.
func hm(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return "<1m"
	}
	return fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// RenderJSON writes the report as JSON.
//
// Byte for byte reproducible: every list is sorted by a total order, no map is
// ever ranged over on the way out, and nothing in the output depends on the
// clock, on the machine or on the order rows came back from SQLite. There is a
// test that runs the same events through twice and compares the bytes.
func RenderJSON(w io.Writer, r Report) error {
	out := jsonReport{
		Range: jsonRange{
			From: r.From.Format(time.DateOnly),
			To:   r.To.AddDate(0, 0, -1).Format(time.DateOnly),
			Days: len(r.Days),
		},
		Options: jsonOptions{
			ClusterGap:      r.Options.ClusterGap.String(),
			AttentionWindow: r.Options.AttentionWindow.String(),
			Head:            r.Options.Head.String(),
			Tail:            r.Options.Tail.String(),
			CountBackground: r.Options.CountBackground,
		},
		Total:            jsonTotalsOf(r.Total, r.Options),
		UnreadableEvents: r.Unreadable,
		Days:             []jsonDay{},
	}
	for _, d := range r.Days {
		day := jsonDay{
			Date:       d.Date.Format(time.DateOnly),
			jsonTotals: jsonTotalsOf(d.Totals, r.Options),
		}
		if !d.First.IsZero() {
			day.FirstTrace = d.First.Format(time.RFC3339)
			day.LastTrace = d.Last.Format(time.RFC3339)
		}
		day.Clusters = []jsonCluster{}
		for _, c := range d.Clusters {
			day.Clusters = append(day.Clusters, jsonCluster{
				From:         c.From.Format(time.RFC3339),
				To:           c.To.Format(time.RFC3339),
				Attention:    hm(c.Attention),
				AttentionMS:  ms(c.Attention),
				Background:   hm(c.Background),
				BackgroundMS: ms(c.Background),
				Project:      c.Project,
				ProjectFrom:  string(c.Origin),
				Candidates:   nonNil(c.Candidates),
				Events:       c.Events,
				Projects:     jsonProjectCounts(c.Projects),
				Sources:      jsonSources(c.Sources),
			})
		}
		day.Runs = []jsonRun{}
		for _, run := range d.Runs {
			day.Runs = append(day.Runs, jsonRun{
				From:         run.From.Format(time.RFC3339),
				To:           run.To.Format(time.RFC3339),
				Project:      run.Project,
				Gap:          run.Gap,
				Attention:    hm(run.Attention),
				AttentionMS:  ms(run.Attention),
				Background:   hm(run.Background),
				BackgroundMS: ms(run.Background),
				Events:       run.Events,
				Sources:      jsonSources(run.Sources),
				BrowserKeys:  jsonKeys(run.BrowserKeys),
			})
		}
		out.Days = append(out.Days, day)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}

type jsonReport struct {
	Range            jsonRange   `json:"range"`
	Options          jsonOptions `json:"options"`
	Total            jsonTotals  `json:"total"`
	Days             []jsonDay   `json:"days"`
	UnreadableEvents int         `json:"unreadable_events"`
}

type jsonRange struct {
	From string `json:"from"`
	To   string `json:"to"`
	Days int    `json:"days"`
}

type jsonOptions struct {
	ClusterGap      string `json:"cluster_gap"`
	AttentionWindow string `json:"attention_window"`
	Head            string `json:"head"`
	Tail            string `json:"tail"`
	CountBackground bool   `json:"count_background"`
}

type jsonTotals struct {
	Attention      string        `json:"attention"`
	AttentionMS    int64         `json:"attention_ms"`
	Background     string        `json:"background"`
	BackgroundMS   int64         `json:"background_ms"`
	Active         string        `json:"active"`
	ActiveMS       int64         `json:"active_ms"`
	Span           string        `json:"span"`
	SpanMS         int64         `json:"span_ms"`
	Padding        string        `json:"padding"`
	PaddingMS      int64         `json:"padding_ms"`
	Agent          string        `json:"agent"`
	AgentMS        int64         `json:"agent_ms"`
	OneNeighbour   string        `json:"one_neighbour"`
	OneNeighbourMS int64         `json:"one_neighbour_ms"`
	Counted        string        `json:"counted"`
	CountedMS      int64         `json:"counted_ms"`
	Projects       []jsonProject `json:"projects"`
}

type jsonDay struct {
	Date       string `json:"date"`
	FirstTrace string `json:"first_trace,omitempty"`
	LastTrace  string `json:"last_trace,omitempty"`
	jsonTotals
	Runs     []jsonRun     `json:"runs"`
	Clusters []jsonCluster `json:"clusters"`
}

// jsonRun is one line of the schedule: from when to when, on what.
type jsonRun struct {
	From         string       `json:"from"`
	To           string       `json:"to"`
	Project      string       `json:"project"`
	Gap          bool         `json:"gap"`
	Attention    string       `json:"attention"`
	AttentionMS  int64        `json:"attention_ms"`
	Background   string       `json:"background"`
	BackgroundMS int64        `json:"background_ms"`
	Events       int          `json:"events"`
	Sources      []jsonSource `json:"sources"`
	BrowserKeys  []jsonKey    `json:"browser_keys"`
}

type jsonProject struct {
	Project      string       `json:"project"`
	Attention    string       `json:"attention"`
	AttentionMS  int64        `json:"attention_ms"`
	Background   string       `json:"background"`
	BackgroundMS int64        `json:"background_ms"`
	Agent        string       `json:"agent"`
	AgentMS      int64        `json:"agent_ms"`
	Wall         string       `json:"wall"`
	WallMS       int64        `json:"wall_ms"`
	Events       int          `json:"events"`
	Sources      []jsonSource `json:"sources"`
	BrowserKeys  []jsonKey    `json:"browser_keys"`
	Candidates   []string     `json:"neighbour_candidates"`
}

type jsonSource struct {
	Source string `json:"source"`
	Events int    `json:"events"`
}

type jsonKey struct {
	Key    string `json:"key"`
	Visits int    `json:"visits"`
}

type jsonCluster struct {
	From         string             `json:"from"`
	To           string             `json:"to"`
	Attention    string             `json:"attention"`
	AttentionMS  int64              `json:"attention_ms"`
	Background   string             `json:"background"`
	BackgroundMS int64              `json:"background_ms"`
	Project      string             `json:"project"`
	ProjectFrom  string             `json:"project_from"`
	Candidates   []string           `json:"neighbour_candidates"`
	Events       int                `json:"events"`
	Projects     []jsonProjectCount `json:"projects"`
	Sources      []jsonSource       `json:"sources"`
}

type jsonProjectCount struct {
	Project string `json:"project"`
	Events  int    `json:"events"`
	Own     int    `json:"own"`
}

func jsonTotalsOf(t Totals, o Options) jsonTotals {
	counted := t.Attention
	if o.CountBackground {
		counted += t.Background
	}
	out := jsonTotals{
		Attention:      hm(t.Attention),
		AttentionMS:    ms(t.Attention),
		Background:     hm(t.Background),
		BackgroundMS:   ms(t.Background),
		Active:         hm(t.Active),
		ActiveMS:       ms(t.Active),
		Span:           hm(t.Span),
		SpanMS:         ms(t.Span),
		Padding:        hm(t.Padding),
		PaddingMS:      ms(t.Padding),
		Agent:          hm(t.Agent),
		AgentMS:        ms(t.Agent),
		OneNeighbour:   hm(t.OneNeighbour),
		OneNeighbourMS: ms(t.OneNeighbour),
		Counted:        hm(counted),
		CountedMS:      ms(counted),
		Projects:       []jsonProject{},
	}
	for _, p := range t.Projects {
		out.Projects = append(out.Projects, jsonProject{
			Project:      p.Name,
			Attention:    hm(p.Attention),
			AttentionMS:  ms(p.Attention),
			Background:   hm(p.Background),
			BackgroundMS: ms(p.Background),
			Agent:        hm(p.Agent),
			AgentMS:      ms(p.Agent),
			Wall:         hm(p.Wall),
			WallMS:       ms(p.Wall),
			Events:       p.Events,
			Sources:      jsonSources(p.Sources),
			BrowserKeys:  jsonKeys(p.BrowserKeys),
			Candidates:   nonNil(p.Candidates),
		})
	}
	return out
}

func jsonSources(counts []SourceCount) []jsonSource {
	out := []jsonSource{}
	for _, c := range counts {
		out = append(out, jsonSource(c))
	}
	return out
}

func jsonKeys(keys []BrowserKey) []jsonKey {
	out := []jsonKey{}
	for _, k := range keys {
		out = append(out, jsonKey(k))
	}
	return out
}

func jsonProjectCounts(counts []ProjectCount) []jsonProjectCount {
	out := []jsonProjectCount{}
	for _, c := range counts {
		out = append(out, jsonProjectCount{Project: c.Name, Events: c.Events, Own: c.Own})
	}
	return out
}

// nonNil keeps an empty list an empty list rather than null: a consumer that
// ranges over it should not have to know the difference.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func ms(d time.Duration) int64 { return int64(d / time.Millisecond) }
