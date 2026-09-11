// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package confirm

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/configfile"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Scope is what an edit is, and it is asked every single time.
//
// It is not a setting and not a mode. Somebody correcting a block that a rule
// already named could mean any of three things, and which one cannot be worked
// out from anything: today it is a one-off, tomorrow the same trace deserves a
// rule, the day after it is a one-off again. So the question is asked at the
// moment of the edit, in one keystroke, and it has three answers.
type Scope string

const (
	// ScopeOnce changes this stretch of this day and nothing else. No rule is
	// written and no other day moves.
	ScopeOnce Scope = "once"
	// ScopeRule writes a rule. Rules run when a report is built, so this
	// renames every unconfirmed day that has the same trace in it — which is
	// what you want when the old name was simply wrong.
	ScopeRule Scope = "rule"
	// ScopeSince writes a rule that starts on the day being confirmed — not on
	// the day somebody is sitting at, which may be later. Everything before
	// that day keeps the name it had, which is what you want when the name did
	// not use to be wrong: the same directory really was a different project
	// six months ago, and an undated rule would say it always was this one.
	ScopeSince Scope = "since"
)

// Scopes is the three, in the order they are offered.
var Scopes = []Scope{ScopeOnce, ScopeRule, ScopeSince}

// Describe is the one-line explanation shown beside each.
func Describe(s Scope) string {
	switch s {
	case ScopeOnce:
		return "this block only — nothing else moves"
	case ScopeRule:
		return "a rule — every unconfirmed day with this trace is renamed"
	case ScopeSince:
		return "a rule from this day on — earlier days keep the name they had"
	}
	return string(s)
}

// Answer is one thing a person said, in the form the rest of the program takes
// it in. The terminal builds one of these; so does every flag.
type Answer struct {
	Scope Scope

	// Trace is what the rule is written on. It is the trace the question was
	// about — never a choice, because the question was built around it. The
	// one exception is Title below, which is the answer that says the key
	// cannot carry the rule after all.
	Trace Trace

	Project string
	Subject string
	// Work is written only when the project is new, and only when it says
	// something. Not saying is a third answer, not a default.
	Work *bool

	// Never says the trace names no project: the answer that costs one key,
	// because "not a project and not a subject" is an ordinary answer and
	// should cost what naming costs.
	Never bool
	// Ambiguous says the trace names a subject and never the same one twice.
	Ambiguous bool

	// Title is an expression to write as a title rule instead of writing on
	// the trace itself. It exists because "the question was about a key so the
	// rule goes on the key" is tidy and wrong: on a repository host the name
	// is in the third path segment, which spoor does not store and never will,
	// and in the page title, which it does.
	//
	// The expression is never invented. What is offered is a title that was
	// actually seen, escaped, and then edited by whoever is answering.
	Title string

	// From and To are the stretch a one-off answer is about. Ignored for a
	// rule, which is about a trace rather than about a piece of a day.
	From, To time.Time
	// Encounter marks an answer about one visit to a trace already decided to
	// be one no rule can name. It is not hand marking of the counted kind:
	// the decision that no rule can name this *was* a rule, written once, and
	// only which subject this particular visit belonged to is by hand.
	Encounter bool
	// Reason says why no rule could be written, for the count of hand marking
	// that the confirmed day carries.
	Reason string
}

// Effect is what an answer will do, before it does it.
type Effect struct {
	// Additions is what goes into the config file, and Preview is the text it
	// will add, exactly as it will appear.
	Additions []configfile.Addition
	Preview   []string
	// Assignment is what goes into the database instead, for an answer no rule
	// could carry.
	Assignment *store.Assignment
	// Warnings are things worth knowing before the write, not after. A rule
	// that is already covered by a more specific one will never fire; a
	// subject rule on a trace nobody touches will collect no time at all. Both
	// are silent failures, and both have been measured rather than imagined.
	Warnings []string
}

// Plan works out what an answer does. Nothing is written.
func (s *Session) Plan(a Answer) (Effect, error) {
	var e Effect
	if a.Scope == "" {
		return e, errors.New("an edit has to say what it is: this block only, a rule, or a rule from today")
	}
	if a.Scope == ScopeOnce {
		if a.To.IsZero() || !a.To.After(a.From) {
			return e, errors.New("an answer about this block only needs the stretch it is about")
		}
		e.Assignment = &store.Assignment{
			Day:          s.date.Format(time.DateOnly),
			From:         a.From,
			To:           a.To,
			Project:      a.Project,
			Subject:      a.Subject,
			ClearSubject: a.Subject == "" && a.Project != "",
			OneOff:       !a.Encounter,
			Reason:       reasonOr(a.Reason),
		}
		return e, nil
	}

	if a.Trace.Value == "" && a.Title == "" {
		return e, errors.New("there is no trace here to write a rule on; answer for this block only")
	}
	var since time.Time
	if a.Scope == ScopeSince {
		since = s.date
	}

	switch {
	case a.Never:
		e.Additions = append(e.Additions, configfile.Addition{
			List: configfile.Never, Kind: kindOf(a.Trace), Value: a.Trace.Value, Since: since,
		})
	case a.Ambiguous:
		e.Additions = append(e.Additions, configfile.Addition{
			List: configfile.Ambiguous, Kind: kindOf(a.Trace), Value: a.Trace.Value, Since: since,
		})
	case a.Project == "":
		return e, errors.New("a rule needs something to name: a project, or a decision that this names nothing")
	case a.Title != "":
		// The rule goes on the title, and the trace it was asked about gets
		// nothing. Writing both would be a rule on the key *and* a rule on the
		// title, which is two rules that can disagree, not one that is more
		// precise: this file has no way to say "this host and this title".
		e.Additions = append(e.Additions, configfile.Addition{
			Project: a.Project, Subject: a.Subject,
			Kind: configfile.Titles, Value: a.Title, Since: since, Work: a.Work,
		})
		e.Warnings = append(e.Warnings,
			"a title rule applies to every host, not only this one — title formats differ "+
				"between hosts, and one that names a project here may name nothing there")
	default:
		// A subject rule alone does nothing. Subjects are only looked at once
		// a project has matched, so a key filed under the subject and nowhere
		// else names neither: the event falls through to the fallback and the
		// same question comes back tomorrow, and the day after — a mechanism
		// that looks broken because it is.
		//
		// So when an answer names both, the trace is written under both:
		// under the project unless the project already names it, and under the
		// subject always.
		if a.Subject != "" && s.rules.NamesTrace(string(a.Trace.Kind), a.Trace.Value, s.date) != a.Project {
			e.Additions = append(e.Additions, configfile.Addition{
				Project: a.Project,
				Kind:    kindOf(a.Trace), Value: a.Trace.Value, Since: since, Work: a.Work,
			})
			a.Work = nil // the project exists now; saying it twice would be noise
		}
		e.Additions = append(e.Additions, configfile.Addition{
			Project: a.Project, Subject: a.Subject,
			Kind: kindOf(a.Trace), Value: a.Trace.Value, Since: since, Work: a.Work,
		})
	}

	for _, add := range e.Additions {
		text, err := s.file.Preview(add)
		if err != nil {
			return e, err
		}
		e.Preview = append(e.Preview, text)
	}
	e.Warnings = append(e.Warnings, s.warn(a)...)
	return e, nil
}

func reasonOr(reason string) string {
	if reason == "" {
		return "no trace here could carry a rule"
	}
	return reason
}

func kindOf(t Trace) string {
	switch t.Kind {
	case KindPath:
		return configfile.Paths
	case KindTitle:
		return configfile.Titles
	case KindBranch:
		return configfile.Branches
	default:
		return configfile.Keys
	}
}

// warn is the two checks that run before a rule is written, both of which
// catch a rule that parses, looks right and does nothing.
func (s *Session) warn(a Answer) []string {
	var out []string

	// A rule the dictionary already refuses, at least as specifically. The
	// worst possible outcome is a rule written in silence that can never fire,
	// because its author is now sure the question is closed.
	if a.Project != "" && a.Trace.Value != "" && a.Title == "" {
		if by, covered := s.refusedBy(a.Trace, a.Scope); covered {
			out = append(out, fmt.Sprintf(
				"%q already refuses this, and it is at least as specific — the new rule "+
					"would never fire", by))
		}
	}

	// And the measured one. A directory an agent worked in while its owner
	// prompted from the parent has thousands of events and no touches; time
	// follows touches, so as a subject it collects exactly nothing. This was
	// found in real data, not imagined, and it cannot be spotted by memory.
	if a.Subject != "" {
		for _, q := range s.doc.Questions {
			if q.Trace != a.Trace {
				continue
			}
			if q.Touches == 0 && q.Events > 0 {
				out = append(out, fmt.Sprintf(
					"%d events here and not one of them is you — as a subject this gives zero "+
						"hours, because time follows the last thing you touched", q.Events))
			}
			break
		}
	}
	return out
}

// refusedBy says whether a never entry already covers this trace at least as
// specifically, and which one.
func (s *Session) refusedBy(t Trace, scope Scope) (string, bool) {
	if s.rules == nil {
		return "", false
	}
	var since config.Date
	if scope == ScopeSince {
		since.Time = s.date
	}
	return s.rules.Refuses(string(t.Kind), t.Value, since)
}

// Delta is what an answer did to the day, in hours.
//
// It is shown after every write, and a delta of nothing is a warning rather
// than a quiet success: a rule that changed no time is a rule that is either
// covered by another one or written on a trace nobody touches, and both look
// exactly like a rule that worked.
type Delta struct {
	Moved []ProjectDelta
	// Nothing says the day did not move at all.
	Nothing bool
	// Rule says a rule was written. An answer for one stretch of one day is
	// not one, and a line claiming otherwise would teach somebody that
	// --one-off writes rules.
	Rule bool
}

// ProjectDelta is one row of it.
type ProjectDelta struct {
	Project string
	By      time.Duration
}

func deltaBetween(before, after report.Day) Delta {
	was := map[string]time.Duration{}
	for _, p := range before.Projects {
		was[p.Name] = p.Attention + p.Background
	}
	now := map[string]time.Duration{}
	for _, p := range after.Projects {
		now[p.Name] = p.Attention + p.Background
	}
	var d Delta
	for name := range union(was, now) {
		if by := now[name] - was[name]; by != 0 {
			d.Moved = append(d.Moved, ProjectDelta{Project: name, By: by})
		}
	}
	sort.Slice(d.Moved, func(i, j int) bool {
		a, b := d.Moved[i], d.Moved[j]
		if abs(a.By) != abs(b.By) {
			return abs(a.By) > abs(b.By)
		}
		return a.Project < b.Project
	})
	d.Nothing = len(d.Moved) == 0
	return d
}

func union(a, b map[string]time.Duration) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// String is the delta on one line, the way it is printed after a write.
func (d Delta) String() string {
	if d.Nothing && d.Rule {
		return "the rule is written and no time moved"
	}
	if d.Nothing {
		return "nothing moved — is that stretch inside a block of the day?"
	}
	parts := make([]string, 0, len(d.Moved))
	for _, m := range d.Moved {
		name := m.Project
		if name == "" {
			name = "no project"
		}
		sign := "+"
		if m.By < 0 {
			sign = "−"
		}
		parts = append(parts, fmt.Sprintf("%s %s%s", name, sign, hm(abs(m.By))))
	}
	return joinWith(parts, ", ")
}

func joinWith(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
