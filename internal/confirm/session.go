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
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/configfile"
	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"github.com/vadosdog/spoor-timetracker/internal/rules"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Session is one day open for confirming.
//
// Everything that changes a day goes through here: the terminal, and every
// flag. That is not tidiness — it is the only way the promise "everything the
// TUI does is available as flags" can be kept without two implementations that
// slowly stop agreeing. A key that did something this did not have would be a
// key with no flag, and it would be found out by somebody's day being wrong.
type Session struct {
	st         *store.Store
	configPath string
	file       *configfile.File
	rules      *rules.Rules
	attr       config.Attribution
	opts       report.Options
	date       time.Time
	events     []event.Event
	seen       map[string]bool
	doc        Document
	version    string

	// answered counts the questions closed in this session, for the line the
	// confirmed day carries. Hand work has to be countable.
	answered int
	// total is how many there were when the day was opened, so that "9 of 11"
	// means the same thing at the end as it did at the start.
	total int
	// reopen says this day's snapshot is on its way out: it is read as an open
	// day, and the frozen numbers stay in the database until a confirmation
	// replaces them.
	reopen bool
}

// Open reads a day and everything needed to change it.
func Open(st *store.Store, configPath, version string, date time.Time, opts report.Options) (*Session, error) {
	return open(st, configPath, version, date, opts, false)
}

// Reopen is Open for a day that has already been confirmed and is to be
// answered again. The snapshot is left exactly where it is: it is thrown away
// by the confirmation that replaces it, in the same transaction, and not a
// moment earlier. Deleting it up front would mean that quitting without
// confirming — or a crash, or a second thought — destroys numbers that cannot
// be computed again, since the rules having moved is the reason the day was
// frozen in the first place.
func Reopen(st *store.Store, configPath, version string, date time.Time, opts report.Options) (*Session, error) {
	return open(st, configPath, version, date, opts, true)
}

func open(st *store.Store, configPath, version string, date time.Time, opts report.Options, reopen bool) (*Session, error) {
	file, err := configfile.Load(configPath)
	if err != nil {
		return nil, err
	}
	s := &Session{
		st: st, configPath: configPath, file: file,
		opts: opts, date: date, version: version, reopen: reopen,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	s.total = s.doc.Budget.Questions
	return s, nil
}

// load reads the config, the events and what has already been said, and builds
// the document. It runs again after every write, because a rule that changed
// the day has to change the day on screen.
func (s *Session) load() error {
	cfg, err := config.Load(s.configPath)
	if err != nil {
		return err
	}
	// A frozen day is computed with the settings it was frozen under, not
	// with today's. Answering one pause on it is allowed and moves no measured
	// number — but rebuilding the day under a threshold that has changed since
	// would move every claimed number, and the snapshot is supposed to be the
	// one thing that does not drift.
	if frozen, ok, err := s.st.Confirmed(s.date.Format(time.DateOnly)); err != nil {
		return err
	} else if ok && !s.reopen {
		s.opts.ClusterGap = frozen.ClusterGap
		s.opts.AttentionWindow = frozen.AttentionWindow
		s.opts.Head, s.opts.Tail = frozen.Head, frozen.Tail
		s.opts.CountBackground = frozen.CountBackground
	}
	s.attr = cfg.Attribution
	dictionary, _ := rules.New(cfg.Attribution)
	s.rules = dictionary
	s.opts.Attribution = dictionary

	end := s.date.AddDate(0, 0, 1)
	if s.events == nil {
		if s.events, err = s.st.EventsBetween(s.date, end); err != nil {
			return err
		}
		if s.seen, err = s.seenBefore(); err != nil {
			return err
		}
	}

	saved, err := s.st.Assignments(s.date.Format(time.DateOnly))
	if err != nil {
		return err
	}
	s.opts.Manual = nil
	for _, a := range saved {
		s.opts.Manual = append(s.opts.Manual, report.Assignment{
			From: a.From.In(s.date.Location()), To: a.To.In(s.date.Location()),
			Project: a.Project, Subject: a.Subject,
			ClearSubject: a.ClearSubject, OneOff: a.OneOff,
		})
	}

	answered, err := s.st.WindowAnswers(s.date.Format(time.DateOnly))
	if err != nil {
		return err
	}

	// Answered pauses reach the report as their own line and change nothing
	// else. See Options.Pauses.
	s.opts.Pauses = nil
	for _, a := range answered {
		s.opts.Pauses = append(s.opts.Pauses, report.Pause{
			From: a.From.In(s.date.Location()), To: a.To.In(s.date.Location()),
			Worked: a.Worked, Project: a.Project, Subject: a.Subject,
		})
	}

	s.doc = Build(Input{
		Date:        s.date,
		Answered:    answered,
		Events:      s.events,
		Rules:       dictionary,
		Attribution: cfg.Attribution,
		Options:     s.opts,
		Seen:        s.seen,
		Projects:    projectNames(cfg.Attribution),
		Subjects:    subjectNames(cfg.Attribution),
	})

	confirmed, ok, err := s.st.Confirmed(s.date.Format(time.DateOnly))
	if err != nil {
		return err
	}
	s.doc.Confirmed = ok && !s.reopen
	if s.doc.Confirmed {
		hash, err := configfile.AttributionHash(s.configPath)
		if err != nil {
			return err
		}
		s.doc.RulesMoved = hash != confirmed.ConfigHash
	}
	return nil
}

// seenBefore is the traces the database held before this day, so that "first
// time today" can be said. It reads one column of one query rather than
// building a report: the answer is a set of strings, not a day.
func (s *Session) seenBefore() (map[string]bool, error) {
	return s.st.TracesBefore(s.date)
}

func projectNames(a config.Attribution) []string {
	out := make([]string, 0, len(a.Projects))
	for _, p := range a.Projects {
		out = append(out, p.Name)
	}
	return out
}

func subjectNames(a config.Attribution) map[string][]string {
	out := map[string][]string{}
	for _, p := range a.Projects {
		for _, sub := range p.Subjects {
			if sub.Name != "" {
				out[p.Name] = append(out[p.Name], sub.Name)
			}
		}
	}
	return out
}

// Document is the day as it stands.
func (s *Session) Document() Document { return s.doc }

// Date is the day being confirmed.
func (s *Session) Date() time.Time { return s.date }

// ConfigPath is the file rules are written to.
func (s *Session) ConfigPath() string { return s.configPath }

// Result is what one answer did.
type Result struct {
	Effect Effect
	Delta  Delta
	// Wrote says the config file was changed on disk.
	Wrote bool
}

// Apply carries an answer out: the rule into the config, or the assignment into
// the database, and then the day is built again so that what is on screen is
// what will be frozen.
func (s *Session) Apply(a Answer) (Result, error) {
	effect, err := s.Plan(a)
	if err != nil {
		return Result{}, err
	}
	before := s.doc.Day

	var res Result
	res.Effect = effect
	// One answer is one edit, even when it writes two rules — a project and a
	// subject inside it. If the second is refused, the first must not be left
	// sitting in memory for the next answer's Save to commit: the person was
	// told their answer failed, and a rule they never agreed to would appear
	// in their config a minute later, under a different question.
	for i, add := range effect.Additions {
		if err := s.file.Add(add); err != nil {
			if i > 0 {
				return res, errors.Join(err, s.forget())
			}
			return res, err
		}
	}
	if len(effect.Additions) > 0 {
		if err := s.file.Save(); err != nil {
			// The same reason Add rolls back: an answer that was refused must
			// not be sitting in memory for the next answer's Save to commit.
			return res, errors.Join(err, s.forget())
		}
		res.Wrote = true
	}
	if effect.Assignment != nil {
		if err := s.st.SaveAssignment(*effect.Assignment); err != nil {
			return res, err
		}
	}
	if err := s.reload(); err != nil {
		return res, err
	}
	s.answered++
	res.Delta = deltaBetween(before, s.doc.Day)
	res.Delta.Rule = len(effect.Additions) > 0
	return res, nil
}

// forget throws the in-memory file away and reads the one on disk again, so
// that a part-applied answer leaves nothing behind. The day is not rebuilt:
// nothing was written, so nothing about it has moved.
func (s *Session) forget() error {
	file, err := configfile.Load(s.configPath)
	if err != nil {
		return err
	}
	file.KeepBackup(s.file)
	s.file = file
	return nil
}

// reload re-reads the config file from disk and rebuilds the day.
func (s *Session) reload() error {
	file, err := configfile.Load(s.configPath)
	if err != nil {
		return err
	}
	// The backup is once per session, not once per write: it is a copy of what
	// the file looked like before spoor touched it, and re-reading must not
	// make the next write overwrite that copy with spoor's own output.
	file.KeepBackup(s.file)
	s.file = file
	return s.load()
}

// AnswerWindow records whether one pause between blocks was work, and what it
// was.
//
// It goes through the session like every other answer, and unlike every other
// answer it moves no measured number: no rule, no assignment, nothing summed
// into the day. See the window_answer table for why it is here at all.
// It never writes a rule and is never asked what kind of edit it is. A pause
// has no trace — that is what makes it a pause — so there is nothing a rule
// could be written on, and offering the choice would be offering an answer
// that cannot be carried out.
func (s *Session) AnswerWindow(w Window, worked bool, project, subject string) error {
	if !worked {
		project, subject = "", ""
	}
	if err := s.st.SaveWindowAnswer(store.WindowAnswer{
		Day:     s.date.Format(time.DateOnly),
		From:    w.From,
		To:      w.To,
		Worked:  worked,
		Project: project,
		Subject: subject,
		Left:    w.Left,
		Right:   w.Right,
	}); err != nil {
		return err
	}
	if err := s.load(); err != nil {
		return err
	}
	// A day that is already frozen keeps its numbers and gains this one line.
	// See store.SetClaimed for why that is not the day moving.
	if s.doc.Confirmed {
		return s.refreshClaims()
	}
	return nil
}

// refreshClaims recomputes a frozen day's claimed pauses from the answers and
// the frozen day, and writes them back.
//
// From the *frozen* day, not from today's rebuild of it. Every claim is stored
// beside the numbers it belongs to, so answering a second pause rewrites what
// the first one recorded — and a rebuild is a different day: an import since
// then may have grown a block over the earlier pause, which would subtract
// that claim to nothing and quietly delete it from a snapshot nobody meant to
// touch. The stretches are what the day was when it was filed, so applying the
// answers to them gives the only total that can be defended afterwards.
func (s *Session) refreshClaims() error {
	day := s.date.Format(time.DateOnly)
	frozen, ok, err := s.st.Confirmed(day)
	if err != nil || !ok {
		return err
	}
	answers, err := s.st.WindowAnswers(day)
	if err != nil {
		return err
	}

	// Overlapping answers are one claim, not two. Somebody answering about
	// time they have already answered about has changed their mind, and adding
	// the two together claims hours the day never had.
	var claims []store.WindowAnswer
	for _, a := range answers {
		if a.Worked && a.To.After(a.From) {
			claims = append(claims, a)
		}
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].From.Before(claims[j].From) })

	var total time.Duration
	byRow := map[[2]string]time.Duration{}
	var until time.Time
	for _, a := range claims {
		from := a.From
		if !until.IsZero() && from.Before(until) {
			from = until
		}
		if !from.Before(a.To) {
			continue
		}
		until = a.To
		held := a.To.Sub(from)
		// A pause is the time between blocks. Anything the frozen day already
		// counted as a block is time that was measured, and measured time is
		// never also claimed.
		// Every stretch, padding included: the stretches are the partition of
		// the day's active time, and the head and the tail of a block are time
		// that was measured like the rest of it.
		for _, st := range frozen.Stretches {
			lo, hi := from, a.To
			if st.From.After(lo) {
				lo = st.From
			}
			if st.To.Before(hi) {
				hi = st.To
			}
			if hi.After(lo) {
				held -= hi.Sub(lo)
			}
		}
		if held <= 0 {
			continue
		}
		total += held
		// Including a pause attached to nothing, which is a supported answer:
		// it lands on the project row whose name is empty, the one the report
		// prints as "no project". Skipping it here while still adding it to
		// the total is how the snapshot stops adding up to itself — the header
		// keeps saying two hours and every row says nothing.
		byRow[[2]string{a.Project, ""}] += held
		if a.Subject != "" {
			byRow[[2]string{a.Project, a.Subject}] += held
		}
	}
	return s.st.SetClaimed(day, total, byRow)
}

// SetBackground records the answer to the one question about the agent's own
// time: count it or not. It changes what is summed and never what is measured.
func (s *Session) SetBackground(count bool) error {
	s.opts.CountBackground = count
	return s.load()
}

// Confirm freezes the day.
//
// What is frozen is the result, not the events. Rules keep running for every
// day that has not been confirmed, so one edit still renames a year of history
// — and stops at the days somebody has already read numbers off.
func (s *Session) Confirm() (store.ConfirmedDay, error) {
	out, err := s.snapshot()
	if err != nil {
		return store.ConfirmedDay{}, err
	}
	if err := s.st.Confirm(out); err != nil {
		return store.ConfirmedDay{}, err
	}
	if err := s.load(); err != nil {
		return out, err
	}
	return out, nil
}

// Snapshot is the day shaped the way a confirmed one is, without freezing it.
//
// It is what --unconfirmed exports. One shape rather than two means the
// warning on the page is the only difference between a provisional export and
// a real one, which is the only difference there should be.
func (s *Session) Snapshot() (store.ConfirmedDay, error) { return s.snapshot() }

func (s *Session) snapshot() (store.ConfirmedDay, error) {
	hash, err := configfile.AttributionHash(s.configPath)
	if err != nil {
		return store.ConfirmedDay{}, err
	}
	day := s.doc.Day
	out := store.ConfirmedDay{
		Day:               s.date.Format(time.DateOnly),
		ConfirmedAt:       time.Now(),
		ConfigHash:        hash,
		Version:           s.version,
		ClusterGap:        s.opts.ClusterGap,
		AttentionWindow:   s.opts.AttentionWindow,
		Head:              s.opts.Head,
		Tail:              s.opts.Tail,
		CountBackground:   s.opts.CountBackground,
		OneNeighbour:      day.OneNeighbour,
		Claimed:           day.Claimed,
		QuestionsTotal:    s.total,
		QuestionsAnswered: s.answered,
	}
	for _, st := range day.Stretches {
		out.Stretches = append(out.Stretches, store.ConfirmedStretch{
			From: st.From, To: st.To, Project: st.Project, Subject: st.Subject,
			Kind: string(st.Kind), Ground: string(st.Ground),
		})
	}
	for _, p := range day.Projects {
		out.Rows = append(out.Rows, store.ConfirmedRow{
			Project: p.Name, Work: p.Work, Events: p.Events,
			Attention: p.Attention, Background: p.Background, Claimed: p.Claimed,
			Padding: paddingOf(day, p.Name), Agent: p.Agent, Wall: p.Wall,
		})
		for _, sub := range p.Subjects {
			out.Rows = append(out.Rows, store.ConfirmedRow{
				Project: p.Name, Subject: sub.Name, Work: p.Work, Events: sub.Events,
				Attention: sub.Attention, Background: sub.Background, Claimed: sub.Claimed,
			})
		}
	}
	saved, err := s.st.Assignments(out.Day)
	if err != nil {
		return store.ConfirmedDay{}, err
	}
	for _, a := range saved {
		if !a.OneOff {
			continue
		}
		out.OneOffs = append(out.OneOffs, store.ConfirmedOneOff{
			From: a.From, To: a.To, Project: a.Project, Subject: a.Subject, Reason: a.Reason,
		})
	}
	out.OneOffCount = len(out.OneOffs)
	return out, nil
}

// paddingOf is how much of a project's time is the head and the tail: the one
// part of a day that was not measured at all, which is why it is carried
// rather than folded in.
func paddingOf(day report.Day, project string) time.Duration {
	var total time.Duration
	for _, s := range day.Stretches {
		if s.Project == project && s.Kind == report.KindPadding {
			total += s.Duration()
		}
	}
	return total
}

// Summary is the line printed when a day is confirmed. The count of answers
// that could not become rules is in it on purpose: hand marking that nobody
// can see is hand marking nobody will ever revisit.
func Summary(d store.ConfirmedDay) string {
	var b strings.Builder
	fmt.Fprintf(&b, "confirmed %s: %d of %d questions answered",
		d.Day, d.QuestionsAnswered, d.QuestionsTotal)
	if d.OneOffCount > 0 {
		fmt.Fprintf(&b, ", %d of them could not become rules", d.OneOffCount)
	}
	return b.String()
}
