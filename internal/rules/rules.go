// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package rules is the dictionary: it says which project an event belongs to
// and which accumulating thing inside that project.
//
// Everything here is deterministic — a literal path, a literal browser key, a
// regular expression, and a written order to break ties. No model, no scoring
// of similarity, no guessing. Two runs over the same events and the same config
// give the same answer, which is the property the whole tool rests on.
//
// The rules are applied when a report is built rather than when events are
// imported. An imported event is never re-parsed, so a rule written today would
// otherwise apply only to tomorrow's traces; applied here it names everything
// in the database at once, and a rule that turns out to be wrong is a line to
// edit rather than a re-import to arrange.
//
// Two levels and no more: a project, and a subject inside it. The subject is
// the thing that spans weeks — episode 14, level 3, one feature, one ticket.
package rules

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/paths"
	"github.com/vadosdog/spoor-timetracker/internal/source/browser"
	"github.com/vadosdog/spoor-timetracker/internal/source/claudecode"
)

// Rules is a compiled dictionary. The zero value names nothing, which is what
// running without a config file means.
type Rules struct {
	projects []project
	subjects []subject // tried inside every project, after its own
	never    never
	// ambiguous is the other kind of decision about a trace: it does name a
	// subject, and never the same one twice, so no rule may try. Kept beside
	// never because both are "I have looked at this and decided", and both
	// therefore count as covered.
	ambiguous never
	fallback  config.Fallback
	work      map[string]bool
}

// never is the traces that name nothing: a host that serves every project at
// once, a directory nobody has decided about. Both kinds behave the same way —
// the trace is taken away and the event stays, to be named by the block around
// it — and both count as covered, because a decision was made.
type never struct {
	keys  []keyPattern
	paths []pathPattern
}

type project struct {
	name string
	matcher
	subjects []subject
}

type subject struct {
	name string
	matcher
}

// Problem is a line of the config that will never do anything. Reported rather
// than fixed or ignored: a rule silently matching nothing is the failure mode
// this file exists to avoid, and it is invisible from the report.
type Problem struct {
	Entry  string
	Reason string
}

// projectOwner and subjectOwner say where a rule was written, for the tail of a
// message. The role goes in front of the name because a project and a subject
// may legitimately share one, and quotes go round what stands in the config and
// nothing else — a subject that takes its name from a capture group has none to
// quote.
func projectOwner(name string) string { return fmt.Sprintf("project %q", name) }

// neverOwner is the third place a rule can be written, and it has no name to
// quote — there is one never list. ambiguousOwner is the fourth.
const (
	neverOwner     = "the never list"
	ambiguousOwner = "the ambiguous list"
)

func subjectOwner(name, project string) string {
	owner := "a subject named by a capture group"
	if name != "" {
		owner = fmt.Sprintf("subject %q", name)
	}
	if project != "" {
		owner += fmt.Sprintf(" of project %q", project)
	}
	return owner
}

// New compiles the dictionary. It returns whatever it could make sense of
// along with everything it could not, because one unusable line is a warning
// and not a reason to refuse to report at all.
func New(cfg config.Attribution) (*Rules, []Problem) {
	r := &Rules{
		fallback: cfg.Fallback.Or(config.FallbackCWDBasename),
		work:     map[string]bool{},
	}
	var problems []Problem

	r.never, problems = compileNever(cfg.Never.Keys, cfg.Never.Paths, neverOwner, problems)
	r.ambiguous, problems = compileNever(cfg.Ambiguous.Keys, cfg.Ambiguous.Paths, ambiguousOwner, problems)

	r.subjects, problems = compileSubjects("", cfg.Subjects, problems)

	for _, p := range cfg.Projects {
		if p.Name == "" {
			problems = append(problems, Problem{"", "a project with no name can never be reported; give it one"})
			continue
		}
		m, probs := compileMatcher(projectOwner(p.Name), p.Paths, p.Keys, p.Branches, p.Titles)
		problems = append(problems, probs...)
		if m.empty() {
			problems = append(problems, Problem{"", projectOwner(p.Name) +
				" has no rule that could match anything"})
			continue
		}
		subs, probs := compileSubjects(p.Name, p.Subjects, nil)
		problems = append(problems, probs...)
		if p.Work != nil {
			r.work[p.Name] = *p.Work
		}
		r.projects = append(r.projects, project{name: p.Name, matcher: m, subjects: subs})
	}
	return r, append(problems, shadowed(r)...)
}

// shadowed finds rules that can never fire because a never entry of the same
// kind covers everything they could match, at least as specifically.
//
// The floor a never entry raises is what lets a broad silence coexist with a
// narrow rule, and it has one hole: where the two are equally specific the
// silence wins, and the rule sits there looking as if it works. That is the
// failure this whole list of problems exists to catch, so it is caught here
// rather than left to be noticed by a missing row.
func shadowed(r *Rules) []Problem {
	var problems []Problem
	// The owner is spelled with its role in front of it: a project and a subject
	// may legitimately share a name, and without the role the two messages are
	// the same sentence. Quotes go round what was written in the config and
	// nothing else.
	say := func(owner, entry, by string) {
		problems = append(problems, Problem{entry, fmt.Sprintf(
			"is refused by never %q, which is at least as specific, so the rule can never "+
				"fire (in %s)", by, owner)})
	}
	check := func(owner string, m matcher) {
		for _, rule := range m.paths {
			for _, n := range r.never.paths {
				// Every directory the rule could match is one the silence
				// matches too, and the silence scores at least as high. With a
				// prefix relation that can only happen when the two are the
				// same, and only a glob silence reaches past its own directory.
				// And in force wherever the rule would be: a silence that
				// starts later than the rule leaves the rule working until
				// then, so calling it dead would be a false alarm.
				if n.glob && n.prefix == rule.prefix && sameStart(n.since, rule.since) {
					say(owner, rule.entry, n.entry)
				}
			}
		}
		for _, rule := range m.keys {
			for _, n := range r.never.keys {
				if n.covers(rule) && n.length >= rule.length && sameStart(n.since, rule.since) {
					say(owner, rule.entry, n.entry)
				}
			}
		}
	}
	// Subjects go through the same floors as projects and die the same way, so
	// they are checked the same way. A subject with no name of its own is one
	// that takes it from a capture group, and there is nothing else to call it
	// here.
	subjects := func(project string, list []subject) {
		for _, s := range list {
			check(subjectOwner(s.name, project), s.matcher)
		}
	}
	for _, p := range r.projects {
		check(projectOwner(p.name), p.matcher)
		subjects(p.name, p.subjects)
	}
	subjects("", r.subjects)
	return problems
}

// sameStart reports whether two rules of equal specificity come into force at
// the same moment, which is the only case where one of them can be completely
// dead.
//
// Work it through. A refusal beats a rule of equal specificity unless the rule
// starts later — see the score type. So where the rule starts later it always wins once it
// applies, and where the refusal starts later the rule fires until then. Only
// when the two start together does the refusal cover every moment the rule
// could ever have had — and that is the case worth a warning, because a
// warning that fires on the other two is a warning people stop reading.
func sameStart(a, b config.Date) bool {
	if a.IsZero() || b.IsZero() {
		return a.IsZero() && b.IsZero()
	}
	return a.Equal(b.Time)
}

// covers reports whether every visit k matches, other matches too.
func (k keyPattern) covers(other keyPattern) bool {
	switch {
	case k.address || other.address:
		if k.host != other.host {
			return false
		}
	case k.host != other.host && !strings.HasSuffix(other.host, "."+k.host):
		return false
	}
	if k.port != "" && k.port != other.port {
		return false
	}
	return k.segment == "" || k.segment == other.segment
}

func compileSubjects(project string, list []config.Subject, problems []Problem) ([]subject, []Problem) {
	var out []subject
	for _, s := range list {
		m, probs := compileMatcher(subjectOwner(s.Name, project), s.Paths, s.Keys, s.Branches, s.Titles)
		problems = append(problems, probs...)
		if m.empty() {
			problems = append(problems, Problem{"", subjectOwner(s.Name, project) +
				" has no rule that could match anything"})
			continue
		}
		// A subject with no name takes it from the first capture group of the
		// expression that matched, which is how one line covers every ticket
		// without listing any. With neither a name nor a group there is
		// nothing to call the subject, and it would silently produce empty
		// ones.
		if s.Name == "" && !m.captures() {
			problems = append(problems, Problem{"",
				`a subject with no name needs an expression with a capture group, as in '\b([A-Z]+-\d+)\b'`})
			continue
		}
		out = append(out, subject{name: s.Name, matcher: m})
	}
	return out, problems
}

// Resolve names an event: which project it belongs to, and which subject inside
// that project. Either can come back empty, and empty is a real answer — the
// report has a row for time no rule could name, and inventing a project for a
// search engine would be worse than leaving it there.
//
// byRule is what the name rests on: a line somebody wrote, or the guess the
// source made from the working directory. The two look identical in a report
// and are not the same claim at all, which is what `report --unmatched` exists
// to say and what the confirmed day records per stretch.
func (r *Rules) Resolve(e event.Event) (project, subject string, byRule bool) {
	if r == nil {
		return e.Project, "", false
	}
	t := targetOf(e)

	// A trace on the never list is taken away; the event stays. No rule can
	// name this visit by where it went — one such key on real data carried a
	// sixth of all browsing and 99% of its own host, and what it is not is a
	// project.
	//
	// Taken away against its own kind only, and only while nothing more
	// specific disagrees. Two things follow, and both are needed. A rule
	// reading the page title or the branch still applies, which is what lets an
	// issue tracker be recognised without an API. And a longer path or key
	// still wins: silencing /home/u must not take away the rule on the one
	// checkout inside it, or the entry would be a foot-gun rather than a
	// decision.
	for _, k := range r.never.keys {
		if s := k.match(t); s.hit() && s.beats(t.minKey) {
			t.minKey = s
		}
	}
	for _, p := range r.never.paths {
		if s := p.match(t); s.hit() && s.beats(t.minPath) {
			t.minPath = s
		}
	}
	// A silenced directory also beats the fallback. Refusing to let a path name
	// a project and then letting the last element of that same path name it
	// anyway would leave the entry doing nothing at all.
	silenced := t.minPath.hit()

	best, bestScore := -1, zero
	for i := range r.projects {
		if s, _ := r.projects[i].match(t); s.hit() && s.beats(bestScore) {
			best, bestScore = i, s
		}
	}
	if best >= 0 {
		return r.projects[best].name, pick(t, r.projects[best].subjects, r.subjects), true
	}
	// Nothing matched. A Claude Code event keeps the name its working directory
	// gave it at import time unless the config says otherwise; a browser visit
	// has never had one.
	if r.fallback == config.FallbackCWDBasename && !silenced {
		project = e.Project
	}
	return project, pick(t, nil, r.subjects), false
}

// pick is the subject: the project's own rules first, then the ones that apply
// inside every project. Most specific wins, and the order written breaks a tie.
func pick(t target, own, global []subject) string {
	best, bestScore := "", zero
	for _, list := range [][]subject{own, global} {
		for _, s := range list {
			got, capture := s.match(t)
			if !got.hit() || !got.beats(bestScore) {
				continue
			}
			name := s.name
			if name == "" {
				name = capture
			}
			if name == "" {
				continue
			}
			best, bestScore = name, got
		}
		// A project's own subjects win over the global ones outright rather
		// than on specificity: a rule written inside a project was written
		// about that project.
		if best != "" {
			return best
		}
	}
	return best
}

// Covers says whether the dictionary has an opinion about this event at all:
// either a rule named it, or the never list refused to name it on purpose.
//
// It is what "which line should I write next" is answered with. An event the
// dictionary does not cover may still have a name — the working directory gave
// it one — but that name is a guess nobody wrote down, and the guess is wrong
// in both directions at once: it splits one project across the directories
// inside it, and merges unrelated directories that end in the same word.
func (r *Rules) Covers(e event.Event) bool {
	if r == nil {
		return false
	}
	t := targetOf(e)
	for _, k := range r.never.keys {
		if k.match(t).hit() {
			return true
		}
	}
	for _, p := range r.never.paths {
		if p.match(t).hit() {
			return true
		}
	}
	// A trace decided to be ambiguous is decided about: the answer was "no
	// rule can name the subject here", which is a decision and not a gap. It
	// must not keep coming back up as a line somebody has yet to write.
	for _, k := range r.ambiguous.keys {
		if k.match(t).hit() {
			return true
		}
	}
	for _, p := range r.ambiguous.paths {
		if p.match(t).hit() {
			return true
		}
	}
	for i := range r.projects {
		if s, _ := r.projects[i].match(t); s.hit() {
			return true
		}
	}
	return false
}

// Ambiguous says whether this event's trace was decided to be one no rule can
// name a subject for. The second value is the trace that says so, in the form
// it stands in the config — which is what the question about one encounter is
// headed with.
func (r *Rules) Ambiguous(e event.Event) (bool, string) {
	if r == nil {
		return false, ""
	}
	t := targetOf(e)
	// Path first, then key, which is the order of the ladder everywhere else.
	for _, p := range r.ambiguous.paths {
		if p.match(t).hit() {
			return true, "path:" + p.entry
		}
	}
	for _, k := range r.ambiguous.keys {
		if k.match(t).hit() {
			return true, "key:" + k.entry
		}
	}
	return false, ""
}

// Refuses says whether a never entry already covers a trace at least as
// specifically as a rule written on it would be — which makes that rule one
// that can never fire. The answer is the entry, so the message can name it.
//
// It is asked before a rule is written rather than found afterwards by a row
// that never appears: a rule written in silence that does nothing is the worst
// outcome there is, because whoever wrote it is now sure the question is shut.
//
// The start date is part of the answer. A dated rule beats an undated refusal
// of the same specificity — that is the whole of the tie-break on a start date — so warning about one
// would be a false alarm, and a warning people learn to ignore is worse than
// no warning at all.
func (r *Rules) Refuses(kind, value string, since config.Date) (string, bool) {
	if r == nil {
		return "", false
	}
	switch kind {
	case "path":
		want, err := parsePath(config.Trace{Value: value, Since: since})
		if err != nil {
			return "", false
		}
		for _, n := range r.never.paths {
			if n.glob && n.prefix == want.prefix && sameStart(n.since, want.since) {
				return n.entry, true
			}
		}
	case "key":
		want, err := parseKey(config.Trace{Value: value, Since: since})
		if err != nil {
			return "", false
		}
		for _, n := range r.never.keys {
			if n.covers(want) && n.length >= want.length && sameStart(n.since, want.since) {
				return n.entry, true
			}
		}
	}
	return "", false
}

// NamesTrace says which project a trace resolves to on its own, with no block
// around it to lend it a name.
//
// It answers one question, and that question is load-bearing: a rule written
// under a subject does nothing at all unless the *project* also names the
// trace. Subjects are only consulted once a project has matched, so a key
// filed under projects[X].subjects[Y] and nowhere else names neither — the
// event falls through to the fallback and the question comes back tomorrow,
// and the day after, looking exactly like a mechanism that is broken.
func (r *Rules) NamesTrace(kind, value string, at time.Time) string {
	if r == nil {
		return ""
	}
	t := target{at: at}
	switch kind {
	case "path":
		p, err := parsePath(config.Trace{Value: value})
		if err != nil {
			return ""
		}
		t.cwd = p.prefix
	case "key":
		k, err := parseKey(config.Trace{Value: value})
		if err != nil {
			return ""
		}
		t.isBrowser, t.host, t.port, t.segment = true, k.host, k.port, k.segment
		t.isAddress = k.address
	default:
		return ""
	}
	best, bestScore := -1, zero
	for i := range r.projects {
		if s, _ := r.projects[i].match(t); s.hit() && s.beats(bestScore) {
			best, bestScore = i, s
		}
	}
	if best < 0 {
		return ""
	}
	return r.projects[best].name
}

// Starts lists the rules whose start date falls inside [from, to).
//
// `report` prints a line for each, because a range a dated rule begins inside
// holds two dictionaries and the table under it looks like one. Without that
// line the reader sees one table and assumes one dictionary made it.
func (r *Rules) Starts(from, to time.Time) []string {
	if r == nil {
		return nil
	}
	var out []string
	say := func(owner, entry string, since config.Date) {
		// Strictly inside. A rule starting on the first day of the range covers
		// the whole of it, so nothing changes within — and warning about it
		// would fire on every day somebody wrote a dated rule on, for ever.
		if since.IsZero() || !since.After(from) || !since.Before(to) {
			return
		}
		out = append(out, fmt.Sprintf("%s: %s from %s",
			owner, entry, since.Format(time.DateOnly)))
	}
	walk := func(owner string, m matcher) {
		for _, p := range m.paths {
			say(owner, p.entry, p.since)
		}
		for _, k := range m.keys {
			say(owner, k.entry, k.since)
		}
		for _, list := range [][]config.Regexp{m.branches, m.titles} {
			for _, re := range list {
				say(owner, re.String(), re.Since)
			}
		}
	}
	for _, p := range r.projects {
		walk(projectOwner(p.name), p.matcher)
		for _, s := range p.subjects {
			walk(subjectOwner(s.name, p.name), s.matcher)
		}
	}
	for _, s := range r.subjects {
		walk(subjectOwner(s.name, ""), s.matcher)
	}
	for _, list := range []struct {
		owner string
		of    never
	}{{neverOwner, r.never}, {ambiguousOwner, r.ambiguous}} {
		for _, k := range list.of.keys {
			say(list.owner, k.entry, k.since)
		}
		for _, p := range list.of.paths {
			say(list.owner, p.entry, p.since)
		}
	}
	sort.Strings(out)
	// Two rules written identically are one line, not two: the reader is being
	// told the dictionary changed inside the range, and saying it twice reads
	// as two changes.
	return slices.Compact(out)
}

// Work says whether a project was declared work. The second value is whether it
// said anything at all: an undeclared project is not personal, it is unknown,
// and the difference is the whole point of the question.
func (r *Rules) Work(project string) (work, declared bool) {
	if r == nil {
		return false, false
	}
	w, ok := r.work[project]
	return w, ok
}

// target is the part of an event a rule can look at: where it happened and what
// it was called. Never the contents of anything.
type target struct {
	cwd    string
	branch string
	// host, port and segment are the browser key taken apart. A non-browser
	// event has none of them, and a key rule therefore cannot match it.
	host, port, segment string
	isBrowser           bool
	// isAddress marks a host that is an address literal. An address has no
	// labels, so no rule may reach it through the subdomain rule.
	isAddress bool
	// at is when the event happened, in local time. A rule with a start date
	// does not apply before it, which is the only thing this is for.
	at time.Time
	// minKey and minPath are the scores a rule has to beat to count at all.
	// A never entry raises the floor for traces of its own kind, so that a
	// silenced host or directory can still be spoken for by something more
	// specific — never /home/u does not take away a rule on the checkout
	// inside it.
	minKey, minPath score
	title           string
}

// targetOf takes an event apart into the parts a rule may look at. at is the
// event's own time in local terms — parsed here rather than passed in, so that
// every caller of Resolve gets dated rules right without having to know they
// exist.
func targetOf(e event.Event) target {
	t := target{title: e.Title}
	if ts, err := time.Parse(time.RFC3339, e.TS); err == nil {
		t.at = ts.Local()
	}
	switch e.Source {
	case claudecode.SourceName:
		t.cwd = strings.TrimRight(e.CWD, "/")
		t.branch = e.GitBranch
	case browser.SourceName:
		t.isBrowser = e.Host != ""
		// Through the same call as the entries in the config. The stored host
		// has already been through it once at import, so this changes nothing
		// for a row written by this version — and it is what keeps a row
		// written by an older one, or by hand, comparable at all.
		t.host = browser.CanonicalHost(e.Host)
		t.isAddress = browser.IsAddress(t.host)
		t.port = e.Port
		t.segment = e.PathHead
	}
	return t
}

// matcher is one rule: any of its patterns matching is a match, and the best
// score among them is the rule's.
type matcher struct {
	paths    []pathPattern
	keys     []keyPattern
	branches []config.Regexp
	titles   []config.Regexp
}

func (m matcher) empty() bool {
	return len(m.paths)+len(m.keys)+len(m.branches)+len(m.titles) == 0
}

// captures says whether any expression in the rule has a capture group, which
// is what a subject with no name of its own needs.
func (m matcher) captures() bool {
	for _, list := range [][]config.Regexp{m.branches, m.titles} {
		for _, re := range list {
			if re.NumSubexp() > 0 {
				return true
			}
		}
	}
	return false
}

// score is how specific a match is.
//
// Two numbers rather than one, because two different things decide between
// rules and they are not comparable. Specificity comes first: a literal beats
// an expression, and a longer literal beats a shorter one — where something
// happened is better evidence than what a piece of text looked like. The start
// date breaks a tie between rules of equal specificity, and only that.
//
// The date is here because a dictionary is not a statement about now, it is a
// statement about a history that is still being reported on. The same
// directory was one project in March and another in September, and rules run
// when the report is built (rules run when a report is built), so a plain correction rewrites the March
// numbers too. A dated rule leaves them alone.
//
// So "this is X, and Y from the tenth" is two entries and needs no end date on
// the first: from the tenth the dated one wins on the tie-break, before it the
// dated one does not apply at all.
type score struct {
	n int
	// since is the rule's start date as a Unix second, or 0 for an undated
	// rule. Later beats earlier; an undated rule is the earliest there is.
	since int64
}

// zero is what "no match" is.
var zero = score{}

func (s score) hit() bool { return s.n > 0 }

// beats is the whole order between two matching rules.
func (s score) beats(o score) bool {
	if s.n != o.n {
		return s.n > o.n
	}
	return s.since > o.since
}

// dated turns a start date into the tie-break half of a score.
func dated(since config.Date) int64 {
	if since.IsZero() {
		return 0
	}
	return since.Unix()
}

// started reports whether a rule dated since is in force at the moment at.
// An undated rule always is; a dated one is not, before its day.
func started(since config.Date, at time.Time) bool {
	return since.IsZero() || !at.Before(since.Time)
}

// literalScore lifts a literal match above every expression match. A directory
// and a browser key say where something happened; an expression says what a
// piece of text looked like, and where beats what. Above that, the longer
// entry wins: /src/thing/prototype is more specific than /src/thing, and
// example.com/issues than example.com.
const literalScore = 1 << 20

// match returns how specific the match is, and the first capture group of the
// expression that produced it. A score of zero means no match; the capture is
// empty unless an expression matched and had a group.
func (m matcher) match(t target) (score, string) {
	best, capture := zero, ""
	better := func(s score, c string) {
		if s.hit() && s.beats(best) {
			best, capture = s, c
		}
	}
	for _, p := range m.paths {
		better(p.match(t), "")
	}
	for _, k := range m.keys {
		better(k.match(t), "")
	}
	if t.branch != "" {
		for _, re := range m.branches {
			if s, c, ok := matchRegexp(re, t.branch, t.at); ok {
				better(s, c)
			}
		}
	}
	if t.title != "" {
		for _, re := range m.titles {
			if s, c, ok := matchRegexp(re, t.title, t.at); ok {
				better(s, c)
			}
		}
	}
	return best, capture
}

// matchRegexp scores an expression match. Every expression scores the same:
// there is no honest way to say one pattern is more specific than another, so
// the order they are written in decides between them.
func matchRegexp(re config.Regexp, s string, at time.Time) (score, string, bool) {
	if !started(re.Since, at) {
		return zero, "", false
	}
	found := re.FindStringSubmatch(s)
	if found == nil {
		return zero, "", false
	}
	got := score{n: 1, since: dated(re.Since)}
	if len(found) > 1 {
		return got, found[1], true
	}
	return got, "", true
}

// pathPattern matches the working directory of a Claude Code event.
type pathPattern struct {
	// entry is what was written, kept for the message when a rule turns out
	// never to be able to fire.
	entry  string
	prefix string
	// glob marks an entry written with a trailing "*": a plain string prefix
	// rather than a directory. Agents create a worktree per session with a
	// random suffix — thing-a1b2c3 beside thing-d4e5f6 — and those are one
	// project written one way and a dozen written the other.
	glob bool
	// subtree is true for a rule that names a project: the directory and
	// everything under it, which is what makes one line cover a checkout and
	// all the directories inside it.
	//
	// It is false on the never list, and the difference is deliberate. A rule
	// says "this tree is this project", so it wants the tree. Silencing says
	// "I looked at this directory and it names nothing", which is a statement
	// about the one directory somebody looked at — and `report --unmatched`
	// prints the home directory as a candidate, so a subtree there would take
	// one paste to switch discovery off for the whole machine, silently and
	// for ever. The tree is two entries — "/x" and "/x/*" — because a trailing
	// "*" is a plain string prefix here as everywhere else, and "/x*" would
	// take "/xylophone" with it.
	subtree bool
	// since is the day this rule starts applying. Zero for almost every rule.
	since config.Date
}

func parsePath(t config.Trace) (pathPattern, error) {
	entry := t.Value
	p := pathPattern{entry: entry, prefix: entry, subtree: true, since: t.Since}
	if strings.HasSuffix(p.prefix, "*") {
		p.glob = true
		p.prefix = strings.TrimSuffix(p.prefix, "*")
	}
	expanded, err := expandHome(p.prefix)
	if err != nil {
		return pathPattern{}, err
	}
	p.prefix = expanded
	if p.prefix == "" {
		return pathPattern{}, fmt.Errorf("is empty")
	}
	if !strings.HasPrefix(p.prefix, "/") {
		return pathPattern{}, fmt.Errorf("is not an absolute path; a working directory always is")
	}
	if !p.glob {
		p.prefix = path.Clean(p.prefix)
	}
	return p, nil
}

func (p pathPattern) match(t target) score {
	if t.cwd == "" || !started(p.since, t.at) {
		return zero
	}
	s := score{n: literalScore + len(p.prefix), since: dated(p.since)}
	if p.glob {
		if strings.HasPrefix(t.cwd, p.prefix) && s.beats(t.minPath) {
			return s
		}
		return zero
	}
	// The directory itself, or anything under it. Testing for the separator
	// rather than for the string is what keeps /src/thing from swallowing
	// /src/thing-other, which is a different project with a similar name.
	if p.subtree && strings.HasPrefix(t.cwd, p.prefix+"/") && s.beats(t.minPath) {
		return s
	}
	if t.cwd == p.prefix && s.beats(t.minPath) {
		return s
	}
	return zero
}

// keyPattern matches a browser visit by host, port and first path segment —
// the same key the report prints, so a line of the report can be pasted into
// the config unchanged.
type keyPattern struct {
	// entry is what was written, kept for the message when a rule turns out
	// never to be able to fire.
	entry   string
	host    string
	port    string
	segment string
	// address marks a host that is an address literal rather than a name.
	// An address has no subdomains, so it has to match exactly: walking labels
	// would let an entry of "10" swallow 192.0.2.10.
	address bool
	// length is what the entry was written as, which is what decides between
	// two matching entries: example.com/issues over example.com.
	length int
	// since is the day this rule starts applying. Zero for almost every rule.
	since config.Date
}

func parseKey(t config.Trace) (keyPattern, error) {
	entry := t.Value
	k := keyPattern{entry: entry, length: len(entry), since: t.Since}
	rest := entry
	if strings.Contains(rest, "://") {
		return keyPattern{}, fmt.Errorf("looks like a URL; write host[:port][/first-segment], as the report prints it")
	}
	if strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "~") {
		return keyPattern{}, fmt.Errorf("looks like a path; keys are what the report prints for a browser visit — a directory goes under never.paths or a project's paths")
	}
	if i := strings.Index(rest, "/"); i >= 0 {
		k.segment, rest = rest[i+1:], rest[:i]
		if strings.Contains(k.segment, "/") {
			return keyPattern{}, fmt.Errorf("has more than one path segment; spoor stores only the first, so nothing below it can ever match")
		}
	}
	host, port, err := splitHostPort(rest)
	if err != nil {
		return keyPattern{}, err
	}
	k.port = port
	// The same call the browser source puts a stored host through, because an
	// entry written one way and a host stored another would never meet and
	// nothing would say so. "::1", "0:0:0:0:0:0:0:1" and "Example.COM." all
	// have a second spelling; both sides end up written the way netip and this
	// package write them.
	k.host = browser.CanonicalHost(host)
	if k.host == "" {
		return keyPattern{}, fmt.Errorf("has no host")
	}
	if strings.ContainsAny(k.host, " \t") {
		return keyPattern{}, fmt.Errorf("is not a host name")
	}
	k.address = browser.IsAddress(k.host)
	// A colon left in a host that is not an address means the port was not
	// one: "example.com:" or a typed "example.com:80O". splitHostPort hands the
	// whole thing back as the host rather than guessing, and a host with a
	// colon in it matches nothing that was ever stored — which is exactly the
	// silent non-match this list of problems exists to prevent.
	if !k.address && strings.Contains(k.host, ":") {
		return keyPattern{}, fmt.Errorf("has something after the colon that is not a port number")
	}
	return k, nil
}

// defaultPorts are the ones a browser event never carries: the source drops a
// scheme's own port, because https://x:443/ and https://x/ are the same place
// and storing them differently would split one host into two keys.
var defaultPorts = map[string]string{"443": "https", "80": "http"}

// defaultPortProblem warns about a port copied out of an address bar. It is
// usually the scheme's own, and the source drops those on the way in, so the
// entry would match nothing without ever saying why.
//
// Every list that takes a key goes through this, `never` included: the same
// typo in the same shape deserves the same warning wherever it is written, and
// a never entry that quietly does nothing is worse than a keys entry that
// does, because nothing about the report looks different either way.
//
// The rule is kept rather than dropped: the same port on the other scheme is
// stored, and that is somebody's odd server rather than a mistake.
func defaultPortProblem(entry string, k keyPattern, owner string) (Problem, bool) {
	scheme, ok := defaultPorts[k.port]
	if !ok {
		return Problem{}, false
	}
	return Problem{entry, fmt.Sprintf(
		"names port %s, which is not stored for %s — write the host without it (in %s)",
		k.port, scheme, owner)}, true
}

// splitHostPort takes a port off the end of a key, and knows that an address
// literal is the one host with colons of its own.
//
// "[::1]:3000" is the written form; "::1" on its own is what the report prints,
// since a stored host has no brackets. Splitting that on the last colon would
// give a host of "::" and a port of "1", and the rule would match nothing, for
// ever, without a warning.
func splitHostPort(s string) (host, port string, err error) {
	if inner, ok := strings.CutPrefix(s, "["); ok {
		end := strings.Index(inner, "]")
		if end < 0 {
			return "", "", fmt.Errorf("opens a bracket it never closes")
		}
		host, rest := inner[:end], inner[end+1:]
		switch {
		case rest == "":
			return host, "", nil
		case strings.HasPrefix(rest, ":") && isDigits(rest[1:]):
			return host, rest[1:], nil
		}
		return "", "", fmt.Errorf("has something other than a port after the address")
	}
	i := strings.LastIndex(s, ":")
	// No colon at all, a colon inside an address, or something after it that
	// is not a port: the whole thing is the host.
	if i < 0 || strings.Contains(s[:i], ":") || !isDigits(s[i+1:]) {
		return s, "", nil
	}
	return s[:i], s[i+1:], nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (k keyPattern) match(t target) score {
	if !t.isBrowser || !started(k.since, t.at) {
		return zero
	}
	// A bare host covers its subdomains, the way the browser ignore list does:
	// one instance serving a dozen projects is still one instance, and nobody
	// wants to list every subdomain of it. A written-out subdomain is longer
	// and therefore wins over the bare host.
	//
	// An address has neither subdomains nor labels, so it matches exactly —
	// and the test has to be on the *host*, not on the entry. "10" is a legal
	// host name and does not parse as an address, so guarding on the entry
	// alone would let it swallow 192.0.2.10 through the suffix rule, which is
	// over-matching of the worst kind: a rule that quietly takes somebody
	// else's traffic.
	switch {
	case t.host == k.host:
	case k.address || t.isAddress:
		return zero
	case !strings.HasSuffix(t.host, "."+k.host):
		return zero
	}
	// A port or a segment left out matches any. Written out, it has to be the
	// one: localhost:3000 and localhost:5173 are two dev servers, and telling
	// them apart is the reason the port is stored at all.
	if k.port != "" && k.port != t.port {
		return zero
	}
	if k.segment != "" && k.segment != t.segment {
		return zero
	}
	s := score{n: literalScore + k.length, since: dated(k.since)}
	if !s.beats(t.minKey) {
		return zero
	}
	return s
}

// compileNever builds one of the two lists of decided-about traces. Both are
// compiled here rather than each in its own loop, because they are the same
// thing said about a different question and a second copy is how the two
// silently stop behaving alike.
func compileNever(keys, paths config.Traces, owner string, problems []Problem) (never, []Problem) {
	var n never
	for _, entry := range keys {
		k, err := parseKey(entry)
		if err != nil {
			problems = append(problems, Problem{entry.Value, fmt.Sprintf("%v (in %s)", err, owner)})
			continue
		}
		if p, bad := defaultPortProblem(entry.Value, k, owner); bad {
			problems = append(problems, p)
		}
		n.keys = append(n.keys, k)
	}
	for _, entry := range paths {
		p, err := parsePath(entry)
		if err != nil {
			problems = append(problems, Problem{entry.Value, fmt.Sprintf("%v (in %s)", err, owner)})
			continue
		}
		// The one directory written, not the tree under it. See pathPattern.
		p.subtree = false
		n.paths = append(n.paths, p)
	}
	return n, problems
}

func compileMatcher(owner string, paths, keys config.Traces, branches, titles config.Regexps) (matcher, []Problem) {
	var m matcher
	var problems []Problem
	for _, entry := range paths {
		p, err := parsePath(entry)
		if err != nil {
			problems = append(problems, Problem{entry.Value, fmt.Sprintf("%v (in %s)", err, owner)})
			continue
		}
		m.paths = append(m.paths, p)
	}
	for _, entry := range keys {
		k, err := parseKey(entry)
		if err != nil {
			problems = append(problems, Problem{entry.Value, fmt.Sprintf("%v (in %s)", err, owner)})
			continue
		}
		if p, bad := defaultPortProblem(entry.Value, k, owner); bad {
			problems = append(problems, p)
		}
		m.keys = append(m.keys, k)
	}
	// An empty expression matches every string that exists. Written by hand it
	// is always a mistake — `titles: ''` hands a project every event with a
	// title — and it is the one mistake that produces no error, no warning and
	// a report full of the wrong project.
	for _, list := range []struct {
		key string
		res config.Regexps
	}{{"branches", branches}, {"titles", titles}} {
		for _, re := range list.res {
			if re.Regexp == nil || re.String() == "" {
				problems = append(problems, Problem{"", fmt.Sprintf(
					"an empty %s expression matches everything; delete the key instead (in %s)",
					list.key, owner)})
				continue
			}
			switch list.key {
			case "branches":
				m.branches = append(m.branches, re)
			default:
				m.titles = append(m.titles, re)
			}
		}
	}
	return m, problems
}

// expandHome is paths.ExpandHome. There is one of it, in the package that owns
// where things live, because the calendar source needs the same rule and a
// second copy is how the two drift apart.
func expandHome(p string) (string, error) { return paths.ExpandHome(p) }
