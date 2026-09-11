// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package tui draws the day and takes the keystrokes.
//
// It holds no rules. Every key turns into an Answer and goes through the same
// session the flags go through, which is what keeps "everything the TUI does is
// available as flags" true by construction rather than by discipline — a key
// with logic of its own would be a key with no flag, and the way that gets
// found out is somebody's day being wrong.
//
// What is drawn is a proposal with a sensible default, never a wall of
// intervals. And a pause is never called work: it is shown, with what is
// around it, and the person decides.
package tui

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vadosdog/spoor-timetracker/internal/confirm"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// view is which screen is on.
type view int

const (
	viewQueue view = iota
	viewEncounter
	viewBlocks
	viewBlock
	viewScope
	viewInput
	viewBackground
	viewWindows
	viewHelp
	viewSummary
	viewDone
)

// slot is which of the two answers the cursor is in. The symmetry is not
// decoration: a project and a subject are answered the same way, and the third
// choice in each — "names nothing", "no rule can name this" — is a decision,
// while the fourth is its absence.
type slot int

const (
	slotProject slot = iota
	slotSubject
)

// what a text prompt is for.
type asking int

const (
	askNothing asking = iota
	askNewProject
	askNewSubject
	askTitle
	askEditRule
)

// Model is the whole interface.
type Model struct {
	session *confirm.Session
	doc     confirm.Document

	view view
	at   int // the question, the encounter, or the block
	slot slot
	pick [2]int // the cursor inside each slot
	prev view

	// pending is the answer waiting for its scope. Every edit is asked what it
	// is — one block, a rule, or a rule from today — and the answer is not
	// derivable from anything, so it is asked every time.
	pending confirm.Answer

	// preview is the text the pending answer would add to the config, as the
	// scope screen shows it. Computed when that screen is opened: it is the
	// last moment before the file is touched, and the only one where seeing it
	// can still change the answer.
	preview []string

	// input is the line being typed, and asking says what it is for.
	input  string
	asking asking
	// warned is the name a "there is already one of those" warning was shown
	// for. A second enter on the same name is the person saying they meant it.
	warned string

	message string

	// pausesOnly is the interface opened on the pauses and nothing else, for a
	// day that is already frozen.
	pausesOnly bool

	// answered counts what has been closed in this session, for a header that
	// moves while somebody works.
	answered int

	confirmed store.ConfirmedDay
	done      bool
	quit      bool
}

// Run opens the interface on a real terminal.
func Run(s *confirm.Session, out io.Writer) (store.ConfirmedDay, bool, error) {
	return RunWith(s, nil, out)
}

// errNoPauses and errAllAnswered are the two reasons the pauses screen has
// nothing to do. They are errors so that the command can say which, rather
// than opening an interface that closes itself.
var (
	errNoPauses    = errors.New("this day has no pauses between blocks")
	errAllAnswered = errors.New("every pause on this day has been answered")
)

// AllAnswered reports whether the reason was the second one.
func AllAnswered(err error) bool { return errors.Is(err, errAllAnswered) }

// NoPauses reports whether the reason was the first.
func NoPauses(err error) bool { return errors.Is(err, errNoPauses) }

// RunPauses opens on the pauses screen and offers nothing else.
//
// It exists so that the one screen that changes no measured number can be
// reached on a day that has been confirmed. The alternative was reopening the
// day — throwing away a snapshot and computing it again — to answer a question
// that does not touch it.
//
// A day with nothing left to answer does not open at all. Opening on a
// question that has already been answered, and leaving the moment it is
// answered again, is what "it exits after the first enter" looked like.
func RunPauses(s *confirm.Session, in io.Reader, out io.Writer) (answered int, err error) {
	m := New(s)
	if len(m.doc.Windows) == 0 {
		return 0, errNoPauses
	}
	if unanswered(m.doc.Windows) == 0 {
		return 0, errAllAnswered
	}
	m.view, m.pausesOnly = viewWindows, true
	m.at, _ = m.firstUnanswered(0)
	m.resetPausePicks()
	opts := []tea.ProgramOption{tea.WithOutput(out)}
	if in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	final, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return 0, err
	}
	if got, ok := final.(Model); ok {
		return got.answered, nil
	}
	return 0, nil
}

// RunWith opens it on whatever it is given, which is how every key in here is
// tested: through the keystroke, not through the function under it.
func RunWith(s *confirm.Session, in io.Reader, out io.Writer) (store.ConfirmedDay, bool, error) {
	m := New(s)
	opts := []tea.ProgramOption{tea.WithOutput(out)}
	if in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	final, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return store.ConfirmedDay{}, false, err
	}
	got, ok := final.(Model)
	if !ok {
		return store.ConfirmedDay{}, false, nil
	}
	return got.confirmed, got.done, nil
}

// New builds the model.
func New(s *confirm.Session) Model {
	// The walk: what the rules could not name, then the visits no rule can
	// name, then the pauses, then the day itself. The pauses are part of
	// closing a day rather than an errand beside it — a question reachable
	// only by knowing a key is a question most days will not get asked.
	m := Model{session: s, doc: s.Document()}
	switch {
	case len(m.doc.Questions) > 0:
	case len(m.doc.Encounters) > 0:
		m.view = viewEncounter
	case unanswered(m.doc.Windows) > 0:
		m.view = viewWindows
		m.at, _ = m.firstUnanswered(0)
		m.resetPausePicks()
	default:
		// Nothing left to ask, so the day opens on itself rather than on a
		// list somebody has already been through.
		m.view = viewSummary
	}
	m.resetPicks()
	return m
}

// Init starts nothing: there is no work to do until a key arrives.
func (m Model) Init() tea.Cmd { return nil }

func (m *Model) resetPicks() {
	m.pick = [2]int{0, 0}
	m.slot = slotProject
	q, ok := m.question()
	if !ok {
		return
	}
	for i, c := range q.Project {
		if c.Default {
			m.pick[slotProject] = i
			break
		}
	}
}

func (m Model) question() (confirm.Question, bool) {
	if m.at < 0 || m.at >= len(m.doc.Questions) {
		return confirm.Question{}, false
	}
	return m.doc.Questions[m.at], true
}

func (m Model) meeting() (confirm.Encounter, bool) {
	if m.at < 0 || m.at >= len(m.doc.Encounters) {
		return confirm.Encounter{}, false
	}
	return m.doc.Encounters[m.at], true
}

// encounter is the shortest question there is: one visit to a trace already
// settled as one no rule can name.
//
// It asks nothing about the trace — that was decided once and is never asked
// again — and it does not ask what the edit is either, because there is only
// one thing it can be: this visit, in this day. An answer here cannot become a
// rule by construction, which is the whole of the decision about an ambiguous trace.
func (m Model) encounter(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	e, ok := m.meeting()
	if !ok {
		m.view, m.at = viewSummary, 0
		return m, nil
	}
	switch key(pressed.String()) {
	case "q":
		m.quit = true
		return m, tea.Quit
	case "up", "k":
		if n := len(e.Choices); n > 0 {
			m.pick[slotSubject] = (m.pick[slotSubject] - 1 + n) % n
		}
	case "down", "j":
		if n := len(e.Choices); n > 0 {
			m.pick[slotSubject] = (m.pick[slotSubject] + 1) % n
		}
	case "s":
		m.nextEncounter()
	case "c":
		m.prev, m.view = viewEncounter, viewSummary
	case "enter":
		a := confirm.Answer{
			Scope: confirm.ScopeOnce, Encounter: true,
			From: e.From, To: e.To, Project: e.Project,
			Reason: "one visit to a trace no rule can name",
		}
		if i := m.pick[slotSubject]; i >= 0 && i < len(e.Choices) {
			a.Subject = e.Choices[i].Name
		}
		if _, err := m.session.Apply(a); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.doc = m.session.Document()
		m.answered++
		m.nextEncounter()
	}
	return m, nil
}

func (m *Model) nextEncounter() {
	m.pick[slotSubject] = 0
	if m.at+1 < len(m.doc.Encounters) {
		m.at++
		return
	}
	m.toPauses()
}

func (m Model) block() (confirm.Block, bool) {
	if m.at < 0 || m.at >= len(m.doc.Blocks) {
		return confirm.Block{}, false
	}
	return m.doc.Blocks[m.at], true
}

// Update is every key in the program.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	pressed, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// The line editor first, and without any translation: text is typed in
	// whatever alphabet somebody is writing in, and a project called
	// "модель ЗП" has to be typeable.
	if m.view == viewInput {
		return m.typing(pressed)
	}
	switch key(pressed.String()) {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit
	}
	switch m.view {
	case viewEncounter:
		return m.encounter(pressed)
	case viewScope:
		return m.scope(pressed)
	case viewBlocks:
		return m.blocks(pressed)
	case viewBlock:
		return m.editBlock(pressed)
	case viewBackground:
		return m.background(pressed)
	case viewWindows:
		return m.windows(pressed)
	case viewHelp:
		if key(pressed.String()) == "q" {
			m.quit = true
			return m, tea.Quit
		}
		m.view = m.prev
		return m, nil
	case viewSummary:
		return m.summary(pressed)
	case viewDone:
		return m, tea.Quit
	default:
		return m.queue(pressed)
	}
}

// queue is the question screen: the one thing this interface is for.
func (m Model) queue(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	q, ok := m.question()
	if !ok {
		m.view = viewSummary
		return m, nil
	}
	switch key(pressed.String()) {
	case "q":
		m.quit = true
		return m, tea.Quit
	case "tab":
		m.slot = 1 - m.slot
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "s":
		// Skipped, not decided: it comes back tomorrow. That is the whole
		// difference between the third answer and the fourth.
		m.next()
	case "b":
		m.prev, m.view, m.at = viewQueue, viewBlocks, 0
	case "g":
		m.prev, m.view = viewQueue, viewBackground
	case "w":
		m.prev, m.view, m.at = viewQueue, viewWindows, 0
	case "c":
		m.prev, m.view = viewQueue, viewSummary
	case "N":
		// Capital for "new", lowercase for "nothing", on every screen. One
		// meaning per letter is what makes a row of shortcuts readable at all;
		// the same letter meaning "new project" here and "not work" one screen
		// along is how somebody presses the wrong one at speed.
		if m.slot == slotProject {
			m.ask(askNewProject, "")
		} else {
			m.ask(askNewSubject, "")
		}
	case "?":
		m.prev, m.view = viewQueue, viewHelp
	case "n", "x":
		// One key, because "not a project and not a subject" is an ordinary
		// answer and has to cost what naming costs. Anything dearer and the
		// day stops being worth closing.
		m.pending = confirm.Answer{Trace: q.Trace, Never: true}
		m.prev, m.view = viewQueue, viewScope
		m.preview = m.previewOf(m.pending)
	case "a":
		m.pending = confirm.Answer{Trace: q.Trace, Ambiguous: true}
		m.prev, m.view = viewQueue, viewScope
		m.preview = m.previewOf(m.pending)
	case "t":
		// The answer that exists because a key question does not always have a
		// key answer: on a repository host the name is in the title and
		// nowhere spoor stores. The expression is never invented — a title
		// that was really seen is offered, escaped, and edited from there.
		m.ask(askTitle, proposedTitle(q))
	case "e":
		m.ask(askEditRule, q.Trace.Value)
	case "enter":
		m.pending = m.answerFromSlots(q)
		if m.pending.Project == "" {
			m.message = "nothing is selected to name it"
			return m, nil
		}
		m.prev, m.view = viewQueue, viewScope
		m.preview = m.previewOf(m.pending)
	}
	return m, nil
}

// answerFromSlots turns where the cursor is into an answer.
func (m Model) answerFromSlots(q confirm.Question) confirm.Answer {
	a := confirm.Answer{Trace: q.Trace}
	if i := m.pick[slotProject]; i >= 0 && i < len(q.Project) {
		a.Project = q.Project[i].Name
	}
	if i := m.pick[slotSubject]; i > 0 && i-1 < len(q.Subject) {
		a.Subject = q.Subject[i-1].Name
	}
	return a
}

func (m *Model) move(by int) {
	n := m.slotLen()
	if n == 0 {
		return
	}
	m.pick[m.slot] = (m.pick[m.slot] + by + n) % n
}

func (m Model) slotLen() int {
	q, ok := m.question()
	if !ok {
		return 0
	}
	if m.slot == slotProject {
		return len(q.Project)
	}
	// One more than the subjects: the first line is "no subject", which is the
	// commonest answer and must not be reached by scrolling past the end.
	return len(q.Subject) + 1
}

func (m *Model) ask(what asking, prefill string) {
	m.asking = what
	m.input = prefill
	m.prev = m.view
	m.view = viewInput
}

// typing is the line editor: the one place text is entered at all.
func (m Model) typing(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch pressed.Type {
	case tea.KeyEsc:
		m.view, m.asking, m.input = m.prev, askNothing, ""
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyEnter:
		return m.entered()
	case tea.KeyRunes, tea.KeySpace:
		// A space arrives as KeySpace with its rune already in Runes, so
		// appending one on top of the other is how "Widget board" becomes
		// "Widget  board" — a different project from the one that was meant,
		// written into the config and into every row keyed on it.
		m.input += string(pressed.Runes)
		return m, nil
	}
	return m, nil
}

func (m Model) entered() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input)
	if value == "" {
		m.message = "nothing was typed"
		m.view, m.asking = m.prev, askNothing
		return m, nil
	}
	// A name typed on the pauses screen is an answer about that pause and
	// never a rule: a pause has no trace to write one on.
	if m.prev == viewWindows {
		w, ok := m.window()
		if !ok {
			m.view, m.asking, m.input = viewSummary, askNothing, ""
			return m, nil
		}
		if m.asking == askNewProject {
			if similar := m.similarProject(value); similar != "" && m.warned != value {
				m.warned = value
				m.message = fmt.Sprintf(
					"there is already a project called %q. Press enter again to make %q as well.",
					similar, value)
				return m, nil
			}
			m.warned = ""
		}
		project, sub := value, ""
		if m.asking == askNewSubject {
			project, sub = w.Project, value
			if project == "" {
				if i := m.pick[slotProject]; i >= 0 && i < len(w.Choices) {
					project = w.Choices[i].Name
				}
			}
		}
		m.input, m.asking, m.view = "", askNothing, viewWindows
		if err := m.session.AnswerWindow(w, true, project, sub); err != nil {
			// Counted only when it was written. "N of M answered" over a
			// failed write is the shape of lie this whole stage is against.
			m.message = err.Error()
			return m, nil
		}
		m.doc = m.session.Document()
		m.answered++
		// The command matters: nextWindow returns tea.Quit when this was
		// opened for its pauses alone and there is nothing left to ask.
		// Dropping it left the interface sitting on an answered pause.
		return m.nextWindow()
	}
	q, hasQuestion := m.question()
	trace := q.Trace
	if m.prev == viewBlock {
		b, _ := m.block()
		if len(b.Traces) > 0 {
			trace = b.Traces[0]
		} else {
			trace = confirm.Trace{}
		}
	}
	switch m.asking {
	case askNewProject:
		// A project name is a string, so a typo in the second entry is a second
		// project, silently — and the two then look like two different things
		// for ever. Said out loud before it happens, and the screen stays where
		// it is so that a second enter is a decision rather than a keystroke
		// nobody noticed. The answer is still the person's.
		if similar := m.similarProject(value); similar != "" && m.warned != value {
			m.warned = value
			m.message = fmt.Sprintf(
				"there is already a project called %q. Press enter again to make %q as well.",
				similar, value)
			return m, nil
		}
		m.warned = ""
		m.pending = confirm.Answer{Trace: trace, Project: value}
	case askNewSubject:
		project := m.currentProject(q, hasQuestion)
		m.pending = confirm.Answer{Trace: trace, Project: project, Subject: value}
	case askTitle:
		m.pending = confirm.Answer{
			Trace: trace, Project: m.currentProject(q, hasQuestion), Title: value,
		}
	case askEditRule:
		m.pending = confirm.Answer{
			Trace:   confirm.Trace{Kind: trace.Kind, Value: value},
			Project: m.currentProject(q, hasQuestion),
		}
	}
	m.input, m.asking = "", askNothing
	m.view = viewScope
	m.preview = m.previewOf(m.pending)
	return m, nil
}

func (m Model) currentProject(q confirm.Question, has bool) string {
	if m.prev == viewBlock {
		if b, ok := m.block(); ok {
			return b.Project
		}
	}
	if has {
		if i := m.pick[slotProject]; i >= 0 && i < len(q.Project) {
			return q.Project[i].Name
		}
	}
	return ""
}

// similarProject looks for a name that differs only in case, spaces or
// hyphens — anywhere a project name appears in this day, not only in the
// question queue. A name typed on the blocks screen or on the pauses screen is
// as easy to mistype as one typed here.
func (m Model) similarProject(name string) string {
	want := normalise(name)
	look := func(candidate string) string {
		if candidate != "" && candidate != name && normalise(candidate) == want {
			return candidate
		}
		return ""
	}
	for _, q := range m.doc.Questions {
		for _, c := range q.Project {
			if got := look(c.Name); got != "" {
				return got
			}
		}
	}
	for _, w := range m.doc.Windows {
		for _, c := range w.Choices {
			if got := look(c.Name); got != "" {
				return got
			}
		}
	}
	for _, p := range m.doc.Day.Projects {
		if got := look(p.Name); got != "" {
			return got
		}
	}
	for _, b := range m.doc.Blocks {
		if got := look(b.Project); got != "" {
			return got
		}
	}
	return ""
}

// normalise is the comparison a "did you mean" is made on: case, spaces and
// hyphens. Not a similarity score — a rule, so that it answers the same way
// twice.
func normalise(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return strings.ReplaceAll(s, "_", "")
}

// previewOf is the lines an answer would add to the config, as a rule. A
// preview that cannot be produced is shown as nothing rather than as an error:
// the scope screen is not where a broken config is reported, and pressing a
// key there will report it a moment later, in full.
func (m Model) previewOf(a confirm.Answer) []string {
	a.Scope = confirm.ScopeRule
	effect, err := m.session.Plan(a)
	if err != nil {
		return nil
	}
	var out []string
	for _, text := range effect.Preview {
		out = append(out, strings.Split(text, "\n")...)
	}
	return out
}

// scope is the question every edit is asked. Three keys, no default, and it
// cannot be skipped: what an edit is cannot be worked out from the edit.
func (m Model) scope(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	var chosen confirm.Scope
	switch key(pressed.String()) {
	case "esc":
		m.view, m.pending, m.preview = m.prev, confirm.Answer{}, nil
		return m, nil
	case "1":
		chosen = confirm.ScopeOnce
	case "2":
		chosen = confirm.ScopeRule
	case "3":
		chosen = confirm.ScopeSince
	default:
		return m, nil
	}
	m.pending.Scope = chosen
	if chosen == confirm.ScopeOnce {
		from, to := m.stretch()
		if to.IsZero() {
			m.message = "there is no stretch of the day here to change"
			m.view = m.prev
			return m, nil
		}
		m.pending.From, m.pending.To = from, to
		m.pending.Reason = "answered for this block only"
	}
	res, err := m.session.Apply(m.pending)
	if err != nil {
		m.message = err.Error()
		m.view = m.prev
		return m, nil
	}
	m.doc = m.session.Document()
	m.answered++
	m.message = res.Delta.String()
	for _, w := range res.Effect.Warnings {
		m.message += "\n  " + w
	}
	m.pending, m.preview = confirm.Answer{}, nil
	if m.prev == viewBlock || m.prev == viewBlocks {
		m.view = viewBlocks
		return m, nil
	}
	m.view = viewQueue
	// The question is gone from the queue, because the rule that answers it is
	// in the config and the day was built again. That is what "the second time
	// nobody asks" means here: a mechanism, not a promise.
	if m.at >= len(m.doc.Questions) {
		m.at = len(m.doc.Questions) - 1
	}
	if m.at < 0 {
		m.at = 0
		if len(m.doc.Encounters) > 0 {
			m.view = viewEncounter
		} else {
			m.view = viewSummary
		}
	}
	m.resetPicks()
	return m, nil
}

// stretch is the piece of the day an answer about "this block only" is about.
func (m Model) stretch() (time.Time, time.Time) {
	if m.prev == viewBlock || m.prev == viewBlocks {
		if b, ok := m.block(); ok {
			return b.From, b.To
		}
		return time.Time{}, time.Time{}
	}
	q, ok := m.question()
	if !ok || len(q.Places) == 0 {
		return time.Time{}, time.Time{}
	}
	return q.Places[0].From, q.Places[0].To
}

func (m *Model) next() {
	if m.at+1 < len(m.doc.Questions) {
		m.at++
		m.resetPicks()
		return
	}
	// The encounters come after every other question. They are cheaper, and
	// skipping one breaks nothing: the time stays where it was.
	if len(m.doc.Encounters) > 0 {
		m.at, m.view = 0, viewEncounter
		m.pick[slotSubject] = 0
		return
	}
	m.toPauses()
}

// toPauses is the last stop before the day itself. A day with no pauses left
// to answer goes straight to the summary.
func (m *Model) toPauses() {
	if at, ok := m.firstUnanswered(0); ok {
		m.at, m.prev, m.view = at, viewSummary, viewWindows
		m.resetPausePicks()
		return
	}
	m.at, m.view = 0, viewSummary
}

// unanswered counts the pauses nobody has said anything about yet.
func unanswered(list []confirm.Window) int {
	n := 0
	for _, w := range list {
		if !w.Answered {
			n++
		}
	}
	return n
}

// blocks is the list of every block of the day. The queue is the fast way
// through what spoor could not name; this is the way in to everything else,
// including a block it named with complete confidence.
func (m Model) blocks(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key(pressed.String()) {
	case "esc", "b":
		m.view, m.at = viewQueue, 0
		m.resetPicks()
	case "q":
		m.quit = true
		return m, tea.Quit
	case "up", "k":
		if m.at > 0 {
			m.at--
		}
	case "down", "j":
		if m.at+1 < len(m.doc.Blocks) {
			m.at++
		}
	case "?":
		m.prev, m.view = viewBlocks, viewHelp
	case "c":
		m.prev, m.view = viewBlocks, viewSummary
	case "enter":
		if len(m.doc.Blocks) > 0 {
			m.view, m.slot = viewBlock, slotProject
			m.pick = [2]int{0, 0}
		}
	}
	return m, nil
}

// editBlock is one block open for changing. Its project and its subject, by
// the same two keys as a question, whether or not a rule already named it.
func (m Model) editBlock(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	b, ok := m.block()
	if !ok {
		m.view = viewBlocks
		return m, nil
	}
	switch key(pressed.String()) {
	case "esc":
		m.view = viewBlocks
	case "q":
		m.quit = true
		return m, tea.Quit
	case "?":
		m.prev, m.view = viewBlock, viewHelp
	case "s":
		m.view = viewBlocks
	case "tab":
		m.slot = 1 - m.slot
	case "up", "k":
		m.moveBlock(b, -1)
	case "down", "j":
		m.moveBlock(b, 1)
	case "enter":
		// The same answer as everywhere else: attach it to what the cursor is
		// on. A screen that could only be answered by typing a name was a
		// screen where attaching a block to a project you already have meant
		// spelling it out again.
		a := confirm.Answer{}
		if i := m.pick[slotProject]; i >= 0 && i < len(b.Choices) {
			a.Project = b.Choices[i].Name
		}
		if i := m.pick[slotSubject]; i > 0 && i-1 < len(b.Subjects) {
			a.Subject = b.Subjects[i-1].Name
		}
		if a.Project == "" {
			m.message = "nothing is selected to name it"
			return m, nil
		}
		if len(b.Traces) > 0 {
			a.Trace = b.Traces[0]
		}
		m.pending = a
		m.prev, m.view = viewBlock, viewScope
		m.preview = m.previewOf(m.pending)
	case "N":
		// Empty, not prefilled with what is there. This is "call it
		// something", and starting from the old name means everybody's first
		// edit is the old name with the new one stuck on the end of it.
		if m.slot == slotProject {
			m.ask(askNewProject, "")
		} else {
			m.ask(askNewSubject, "")
		}
		m.prev = viewBlock
	case "n", "x":
		if len(b.Traces) == 0 {
			m.message = "there is no trace here to refuse"
			return m, nil
		}
		m.pending = confirm.Answer{Trace: b.Traces[0], Never: true}
		m.prev, m.view = viewBlock, viewScope
		m.preview = m.previewOf(m.pending)
	case "a":
		if len(b.Traces) == 0 {
			m.message = "there is no trace here to decide about"
			return m, nil
		}
		m.pending = confirm.Answer{Trace: b.Traces[0], Ambiguous: true}
		m.prev, m.view = viewBlock, viewScope
		m.preview = m.previewOf(m.pending)
	}
	return m, nil
}

// windows walks the pauses between blocks and asks the one thing no trace can
// answer: was that work.
//
// Nothing here changes a number, and the screen says so. It is a measuring
// instrument: a pause is invisible on disk — a cigarette and a meeting are the
// same absence of traces — so whether pauses are worth storing as records of
// their own can only be settled by asking somebody on the evening of the day,
// while they still know. If the answer turns out to be no, this screen goes.
//
// There is no default and no "same as last time". That is the whole point:
// guessing whether a pause was work is the one thing the concept forbids by
// name.
func (m Model) windows(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.at >= len(m.doc.Windows) {
		m.at, m.view = 0, m.prev
		return m, nil
	}
	w := m.doc.Windows[m.at]
	answer := func(worked bool) (tea.Model, tea.Cmd) {
		project, sub := "", ""
		if worked {
			if i := m.pick[slotProject]; i >= 0 && i < len(w.Choices) {
				project = w.Choices[i].Name
			}
			if i := m.pick[slotSubject]; i > 0 && i-1 < len(w.Subjects) {
				sub = w.Subjects[i-1].Name
			}
		}
		// Straight to the answer. A pause is never asked what kind of edit it
		// is, because it has no trace for a rule to be written on — offering
		// the choice would be offering something that cannot be carried out.
		if err := m.session.AnswerWindow(w, worked, project, sub); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.doc = m.session.Document()
		m.answered++
		return m.nextWindow()
	}
	switch key(pressed.String()) {
	case "q":
		m.quit = true
		return m, tea.Quit
	case "enter":
		return answer(true)
	case "n", "x":
		// Not work: a break, something watched, an errand. It is one of the
		// four things this screen can say and it costs one key, like every
		// other "this is nothing" answer in the program.
		return answer(false)
	case "s":
		// Skipped, not answered: it comes back tomorrow. This walk moves on
		// by one either way — forwards only, so the skip key cannot hand back
		// the same pause three keystrokes later.
		if m.at+1 < len(m.doc.Windows) {
			m.at++
			m.resetPausePicks()
			return m, nil
		}
		if m.pausesOnly {
			m.quit = true
			return m, tea.Quit
		}
		m.at, m.view = 0, viewSummary
		return m, nil
	case "left", "h", "[":
		// Backwards, for changing an answer that was given too fast. The walk
		// is forwards; this is the way to look at one again.
		if m.at > 0 {
			m.at--
			m.resetPausePicks()
		}
	case "right", "l", "]":
		if m.at+1 < len(m.doc.Windows) {
			m.at++
			m.resetPausePicks()
		}
	case "tab":
		m.slot = 1 - m.slot
	case "up", "k":
		m.movePause(w, -1)
	case "down", "j":
		m.movePause(w, 1)
	case "N":
		// New project or subject, from here, without leaving. The capital is
		// the only shift in the program and it is here because "n" on this
		// screen has to stay the answer everybody reaches for.
		if m.slot == slotProject {
			m.ask(askNewProject, "")
		} else {
			m.ask(askNewSubject, "")
		}
		m.prev = viewWindows
	case "?":
		m.prev, m.view = viewWindows, viewHelp
	case "esc", "w":
		if m.pausesOnly {
			m.quit = true
			return m, tea.Quit
		}
		m.at, m.view = 0, m.prev
	}
	return m, nil
}

// movePause walks whichever of the two lists the cursor is in.
// window is the pause the cursor is on.
func (m Model) window() (confirm.Window, bool) {
	if m.at < 0 || m.at >= len(m.doc.Windows) {
		return confirm.Window{}, false
	}
	return m.doc.Windows[m.at], true
}

func (m *Model) movePause(w confirm.Window, by int) {
	n := len(w.Choices)
	if m.slot == slotSubject {
		n = len(w.Subjects) + 1 // the first line is "no subject"
	}
	if n == 0 {
		return
	}
	m.pick[m.slot] = (m.pick[m.slot] + by + n) % n
}

// resetPausePicks puts the cursor back on whatever the pause suggests.
func (m *Model) resetPausePicks() {
	m.pick, m.slot = [2]int{0, 0}, slotProject
	if m.at >= len(m.doc.Windows) {
		return
	}
	for i, c := range m.doc.Windows[m.at].Choices {
		if c.Default {
			m.pick[slotProject] = i
			break
		}
	}
}

// nextWindow moves to the next pause nobody has answered yet.
//
// The next *unanswered* one, not the next one along. Walking by index sat on
// the last pause once it was reached and re-answered it on every keystroke —
// which from the outside is a program going round in a circle, and was.
//
// Nothing left to ask means the walk is over: the day if there is one to go
// to, and the door if this was opened for its pauses alone.
// The value, never the pointer: bubbletea keeps whatever a handler hands back
// as the model, and a *Model here made the final model a different type from
// the one every other handler returns. The program still ran; what broke was
// reading anything off it afterwards, silently and only at the end.
func (m *Model) nextWindow() (tea.Model, tea.Cmd) {
	if next, ok := m.firstUnanswered(m.at + 1); ok {
		m.at = next
		m.resetPausePicks()
		return *m, nil
	}
	if next, ok := m.firstUnanswered(0); ok && next < m.at {
		// Something skipped earlier. Coming back to it beats stopping with a
		// question still open and the header saying so.
		m.at = next
		m.resetPausePicks()
		return *m, nil
	}
	if m.pausesOnly {
		return *m, tea.Quit
	}
	m.at, m.view = 0, viewSummary
	return *m, nil
}

// firstUnanswered is the first pause at or after i that nobody has answered.
func (m Model) firstUnanswered(i int) (int, bool) {
	for ; i < len(m.doc.Windows); i++ {
		if !m.doc.Windows[i].Answered {
			return i, true
		}
	}
	return 0, false
}

// moveBlock walks whichever of the block's two lists the cursor is in.
func (m *Model) moveBlock(b confirm.Block, by int) {
	n := len(b.Choices)
	if m.slot == slotSubject {
		n = len(b.Subjects) + 1 // the first line is "no subject"
	}
	if n == 0 {
		return
	}
	m.pick[m.slot] = (m.pick[m.slot] + by + n) % n
}

// background is the one question about the agent's own time, and it is about
// whether to count it — never about whether it was work. Nothing on this
// machine can answer the second one, and guessing at it is the thing the
// concept forbids by name.
func (m Model) background(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key(pressed.String()) {
	case "y":
		if err := m.session.SetBackground(true); err != nil {
			m.message = err.Error()
		}
		m.doc = m.session.Document()
		m.view = m.prev
	case "n":
		if err := m.session.SetBackground(false); err != nil {
			m.message = err.Error()
		}
		m.doc = m.session.Document()
		m.view = m.prev
	case "esc", "g":
		m.view = m.prev
	case "q":
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}

// summary is the day, and the place it is agreed with.
func (m Model) summary(pressed tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key(pressed.String()) {
	case "c", "enter":
		confirmed, err := m.session.Confirm()
		if err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.confirmed, m.done = confirmed, true
		m.view = viewDone
		return m, tea.Quit
	case "b":
		m.prev, m.view, m.at = viewSummary, viewBlocks, 0
	case "g":
		m.prev, m.view = viewSummary, viewBackground
	case "w":
		m.prev, m.view, m.at = viewSummary, viewWindows, 0
	case "?":
		m.prev, m.view = viewSummary, viewHelp
	case "esc":
		if len(m.doc.Questions) > 0 {
			m.view, m.at = viewQueue, 0
			m.resetPicks()
		}
	case "q":
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}

var _ = report.KindPadding // the partition is what the summary counts
