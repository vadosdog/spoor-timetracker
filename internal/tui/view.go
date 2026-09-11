// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/confirm"
	"github.com/vadosdog/spoor-timetracker/internal/report"
)

// What a screen shows is a proposal with a default, not a list of intervals.
// The evidence is there — where the trace turned up, what was next to it, what
// it counts for now — because the answer is a judgement and a judgement needs
// something to look at.

// View draws whichever screen is on.
func (m Model) View() string {
	var b strings.Builder
	switch m.view {
	case viewEncounter:
		m.drawEncounter(&b)
	case viewScope:
		m.drawScope(&b)
	case viewInput:
		m.drawInput(&b)
	case viewBlocks:
		m.drawBlocks(&b)
	case viewBlock:
		m.drawBlock(&b)
	case viewBackground:
		m.drawBackground(&b)
	case viewWindows:
		m.drawWindows(&b)
	case viewHelp:
		m.drawHelp(&b)
	case viewSummary, viewDone:
		m.drawSummary(&b)
	default:
		m.drawQueue(&b)
	}
	if m.message != "" {
		fmt.Fprintf(&b, "\n  %s\n", warn.Render(m.message))
	}
	return b.String()
}

// header is the budget: what day this is, and how much is left to do.
//
// It counts what has been answered and what is left, not "question 3 of 11".
// An answered question leaves the queue — that is the whole mechanism — so an
// index into a shrinking list stands still while the list gets shorter, and a
// number that does not move while you work reads as a program that has hung.
func (m Model) header(_, _ int) string {
	d := m.doc
	left := subject.Render(d.Date.Format("Monday, 2 January 2006"))
	right := dim.Render(fmt.Sprintf("%d answered  ·  %d left  ·  %s of %s under question",
		m.answered, len(d.Questions)+len(d.Encounters),
		hm(d.Budget.UnderQuestion), hm(d.Budget.InTheDay)))
	return left + "\n" + right + "\n" + hr() + "\n"
}

func (m Model) drawQueue(b *strings.Builder) {
	q, ok := m.question()
	if !ok {
		m.drawSummary(b)
		return
	}
	b.WriteString(m.header(m.at+1, len(m.doc.Questions)+len(m.doc.Encounters)))

	fmt.Fprintf(b, "\n  %s  %s\n", dim.Render(fmt.Sprintf("%-5s", q.Trace.Kind)),
		subject.Render(q.Trace.Value))
	first := ""
	if q.FirstToday {
		first = "  ·  first seen today"
	}
	fmt.Fprintf(b, "  %s\n", dim.Render(fmt.Sprintf("       %d events  ·  %d of them you  ·  %s%s",
		q.Events, q.Touches, hm(q.Time), first)))
	// What the trace counts for as things stand. A key already inherited
	// correctly and a key leaking into the busiest project nearby look
	// identical without this line, and they are not equally urgent — so it is
	// the one line on the screen picked out in a colour of its own.
	fmt.Fprintf(b, "  %s %s\n", dim.Render("       counts for"),
		warn.Render(named(q.CalledNow)+" now"))
	if len(q.Folded) > 0 {
		fmt.Fprintf(b, "  %s\n", dim.Render(fmt.Sprintf(
			"       one question for %d keys of this host: %s",
			len(q.Folded)+1, foldedList(q.Folded))))
	}
	// The pages themselves. On a repository host the key is the host and the
	// account — the repository is a path segment spoor deliberately does not
	// store — so without this the question is "github.com/somebody" and no
	// way of telling which of forty repositories it was. The name is in the
	// title and nowhere else, which is also what [t] writes a rule from.
	if len(q.Titles) > 0 {
		fmt.Fprintf(b, "\n  %s\n", label.Render("SEEN AS"))
		for i, title := range q.Titles {
			if i == 3 {
				fmt.Fprintf(b, "       %s\n", dim.Render(fmt.Sprintf("and %d more",
					len(q.Titles)-i)))
				break
			}
			fmt.Fprintf(b, "       %s\n", cut(title, 66))
		}
	}

	if len(q.Places) > 0 {
		fmt.Fprintf(b, "\n  %s\n", label.Render("WHERE"))
		for _, p := range q.Places {
			fmt.Fprintf(b, "       %s  %s\n",
				dim.Render(p.From.Format("15:04")+"–"+p.To.Format("15:04")),
				dim.Render(placeOf(p)))
		}
	}

	fmt.Fprintf(b, "\n  %s\n", label.Render("PROJECT"))
	m.drawSlot(b, slotProject, candidateNames(q.Project))
	fmt.Fprintf(b, "\n  %s\n", label.Render("SUBJECT"))
	m.drawSlot(b, slotSubject, append([]row{{"(none)", "leave it in the rest of the project"}},
		candidateNames(q.Subject)...))

	// Four answers and a way out, which is what this screen is for. Everything
	// else — the rarer answers, and the other screens — is behind [?]. A row
	// of thirteen shortcuts is a row nobody reads, and the four that matter
	// were in it somewhere.
	b.WriteString("\n" + hr() + "\n")
	b.WriteString(keys(
		[2]string{"enter", "attach it to this"}, [2]string{"tab", "project / subject"},
		[2]string{"N", "new one"}, [2]string{"n", "names nothing"}))
	b.WriteString(keys([2]string{"s", "skip"}, [2]string{"?", "more"},
		[2]string{"c", "confirm the day"}, [2]string{"q", "quit"}))
}

type row struct{ name, why string }

func candidateNames(list []confirm.Candidate) []row {
	out := make([]row, 0, len(list))
	for _, c := range list {
		out = append(out, row{c.Name, c.Why})
	}
	return out
}

// drawSlot is one of the two lists. Only the slot the cursor is in shows a
// cursor, so it is always obvious which of the two an arrow key would move.
func (m Model) drawSlot(b *strings.Builder, s slot, rows []row) {
	if len(rows) == 0 {
		fmt.Fprintf(b, "       %s %s\n", dim.Render("nothing to suggest —"),
			shortcut.Render("n")+dim.Render(" names it"))
		return
	}
	// A long list is cut around the cursor rather than printed whole: the
	// dictionary can hold dozens of projects, and a screen that scrolls off
	// the top is a screen where the shortcuts are invisible.
	const window = 6
	from := 0
	if m.pick[s] >= window {
		from = m.pick[s] - window + 1
	}
	to := from + window
	if to > len(rows) {
		to = len(rows)
	}
	for i := from; i < to; i++ {
		name := fmt.Sprintf("%-28s", rows[i].name)
		if m.slot == s && m.pick[s] == i {
			fmt.Fprintf(b, "     %s%s %s\n", chosen.Render("› "), chosen.Render(name),
				dim.Render(rows[i].why))
			continue
		}
		fmt.Fprintf(b, "       %s %s\n", name, dim.Render(rows[i].why))
	}
	if rest := len(rows) - to; rest > 0 {
		fmt.Fprintf(b, "       %s\n", dim.Render(fmt.Sprintf("%d more below", rest)))
	}
}

// drawEncounter is the short question: this visit, which subject.
//
// Nothing here asks about the trace. That was settled once — no rule can name
// the subject on it — and asking again would be the repetition the whole
// mechanism exists to stop. What is asked is a new fact: yesterday's answer
// says nothing about today's visit.
func (m Model) drawEncounter(b *strings.Builder) {
	e, ok := m.meeting()
	if !ok {
		return
	}
	b.WriteString(m.header(len(m.doc.Questions)+m.at+1,
		len(m.doc.Questions)+len(m.doc.Encounters)))
	fmt.Fprintf(b, "\n  %s  %s\n", subject.Render(e.Trace.String()),
		dim.Render(fmt.Sprintf("%s–%s  ·  %s  ·  in %s",
			e.From.Format("15:04"), e.To.Format("15:04"),
			hm(e.To.Sub(e.From)), named(e.Project))))
	fmt.Fprintf(b, "  %s\n\n",
		dim.Render("no rule can name the subject here; this visit only"))
	if len(e.Choices) == 0 {
		fmt.Fprintf(b, "  %s\n", dim.Render("nothing to suggest"))
	}
	for i, c := range e.Choices {
		name := fmt.Sprintf("%-28s", c.Name)
		if m.pick[slotSubject] == i {
			fmt.Fprintf(b, "     %s%s %s\n", chosen.Render("› "), chosen.Render(name), dim.Render(c.Why))
			continue
		}
		fmt.Fprintf(b, "       %s %s\n", name, dim.Render(c.Why))
	}
	b.WriteString("\n" + hr() + "\n")
	b.WriteString(keys([2]string{"enter", "accept"},
		[2]string{"s", "leave it in the rest of the project"},
		[2]string{"c", "confirm"}, [2]string{"q", "quit"}))
}

// drawHelp is everything the four answers are not.
//
// It is a screen rather than a longer row because the row is read at speed and
// this is read once. What is on it is the rarer answers and the other places to
// be; what is not on it is anything somebody needs in order to close a day.
func (m Model) drawHelp(b *strings.Builder) {
	fmt.Fprintf(b, "%s\n%s\n\n", subject.Render("the rest of it"), hr())
	fmt.Fprintf(b, "  %s\n", label.Render("OTHER ANSWERS"))
	for _, r := range [][2]string{
		{"a", "no rule can name the subject here — decided once, then asked per visit"},
		{"t", "name it by the page title, for a trace whose name is only there"},
		{"e", "edit the rule before it is written"},
	} {
		fmt.Fprintf(b, "     %s  %s\n", shortcut.Render(r[0]), dim.Render(r[1]))
	}
	fmt.Fprintf(b, "\n  %s\n", label.Render("ELSEWHERE"))
	for _, r := range [][2]string{
		{"b", "every block of the day, including the ones spoor named"},
		{"g", "the agent's own time: counted or not"},
		{"w", "the pauses between blocks"},
		{"c", "the day, and confirming it"},
	} {
		fmt.Fprintf(b, "     %s  %s\n", shortcut.Render(r[0]), dim.Render(r[1]))
	}
	fmt.Fprintf(b, "\n%s\n%s", hr(), keys([2]string{"any key", "back"}, [2]string{"q", "quit"}))
}

// drawScope is the question every edit is asked. Three outcomes, no default:
// which one this is cannot be worked out from the edit, so it is asked in one
// keystroke at the moment of the edit.
func (m Model) drawScope(b *strings.Builder) {
	fmt.Fprintf(b, "%s\n%s\n\n", subject.Render("what is this edit?"), hr())
	for i, s := range confirm.Scopes {
		fmt.Fprintf(b, "  %s  %-10s %s\n", shortcut.Render(fmt.Sprintf("%d", i+1)),
			string(s), dim.Render(confirm.Describe(s)))
	}
	// The exact text, before it is written. The config is a file somebody
	// wrote by hand, and a tool that edits it without showing what it is about
	// to add is a tool you stop trusting with it — which is the rule the
	// configfile package opens with, and this is the screen where a person
	// says yes to an edit.
	if len(m.preview) > 0 {
		fmt.Fprintf(b, "\n  %s\n", label.Render("WHICH PUTS THIS IN YOUR CONFIG"))
		for _, line := range m.preview {
			fmt.Fprintf(b, "  %s\n", dim.Render(line))
		}
		fmt.Fprintf(b, "  %s\n", dim.Render("(3 writes the same, with a since: line)"))
	}
	fmt.Fprintf(b, "\n%s\n%s", hr(), keys([2]string{"esc", "back"}))
}

func (m Model) drawInput(b *strings.Builder) {
	switch m.asking {
	case askNewProject:
		b.WriteString("new project — a name, and it is created here\n")
	case askNewSubject:
		b.WriteString("new subject inside this project\n")
	case askTitle:
		b.WriteString("name it by the page title. What is offered is a title that was really\n" +
			"seen, escaped; cut it down to the part that names the project.\n")
	case askEditRule:
		b.WriteString("the rule, as it will be written\n")
	}
	fmt.Fprintf(b, "\n%s\n  %s\n%s\n%s", hr(), subject.Render(m.input+"▏"), hr(),
		keys([2]string{"enter", "accept"}, [2]string{"esc", "back"}))
}

func (m Model) drawBlocks(b *strings.Builder) {
	b.WriteString(m.header(0, 0))
	fmt.Fprintf(b, "\n  %s\n\n", label.Render("EVERY BLOCK OF THE DAY"))
	for i, blk := range m.doc.Blocks {
		line := fmt.Sprintf("%s–%s  %-22s %s",
			blk.From.Format("15:04"), blk.To.Format("15:04"),
			named(blk.Project), hm(blk.Attention+blk.Background))
		if i == m.at {
			fmt.Fprintf(b, "   %s%s %s\n", chosen.Render("› "), chosen.Render(line),
				dim.Render(groundOf(blk.Ground)))
			continue
		}
		fmt.Fprintf(b, "     %s %s\n", line, dim.Render(groundOf(blk.Ground)))
	}
	b.WriteString("\n" + hr() + "\n")
	b.WriteString(keys([2]string{"enter", "open"}, [2]string{"esc", "back"},
		[2]string{"c", "confirm"}, [2]string{"q", "quit"}))
}

func (m Model) drawBlock(b *strings.Builder) {
	blk, ok := m.block()
	if !ok {
		return
	}
	fmt.Fprintf(b, "%s  %s\n%s\n\n",
		subject.Render(blk.From.Format("15:04")+"–"+blk.To.Format("15:04")),
		dim.Render(hm(blk.Attention+blk.Background)), hr())
	fmt.Fprintf(b, "  %s  %s  %s\n", dim.Render("project"), named(blk.Project),
		dim.Render("("+groundOf(blk.Ground)+")"))
	fmt.Fprintf(b, "  %s  %s\n", dim.Render("subject"), named(blk.Subject))
	if len(blk.Evidence) > 0 {
		fmt.Fprintf(b, "  %s   %s\n", dim.Render("traces"),
			dim.Render(strings.Join(blk.Evidence, ", ")))
	} else {
		fmt.Fprintf(b, "  %s   %s\n", dim.Render("traces"),
			dim.Render("none that could carry a rule — an answer here is for this day only"))
	}
	fmt.Fprintf(b, "\n  %s\n", label.Render("PROJECT"))
	m.drawSlot(b, slotProject, candidateNames(blk.Choices))
	fmt.Fprintf(b, "\n  %s\n", label.Render("SUBJECT"))
	m.drawSlot(b, slotSubject, append([]row{{"(none)", "the project, and no subject"}},
		candidateNames(blk.Subjects)...))

	b.WriteString("\n" + hr() + "\n")
	b.WriteString(keys(
		[2]string{"enter", "attach it to this"}, [2]string{"tab", "project / subject"},
		[2]string{"N", "new one"}, [2]string{"n", "names nothing"}))
	b.WriteString(keys([2]string{"s", "skip"}, [2]string{"?", "more"},
		[2]string{"esc", "back"}, [2]string{"q", "quit"}))
}

func (m Model) drawBackground(b *strings.Builder) {
	bg := m.doc.Background
	fmt.Fprintf(b, "%s %s\n%s\n\n", subject.Render("background "+hm(bg.Total)),
		dim.Render("— the agent working while nobody was watching"), hr())
	b.WriteString(dim.Render(
		"  Whether that is your working time is your call. spoor does not guess it.") + "\n\n")
	for _, s := range bg.Long {
		fmt.Fprintf(b, "  %s  %s  %s\n",
			dim.Render(s.From.Format("15:04")+"–"+s.To.Format("15:04")),
			hm(s.Duration()), dim.Render(named(s.Project)))
	}
	fmt.Fprintf(b, "\n  %s\n\n%s\n", dim.Render(fmt.Sprintf("counted now: %v", bg.Count)), hr())
	b.WriteString(keys([2]string{"y", "count it"}, [2]string{"n", "leave it out"},
		[2]string{"esc", "back"}))
}

// drawWindows is one pause, and the four things that can be said about it.
//
// One at a time, and shaped like every other question on purpose: a list of
// pauses with a yes/no beside each was a different kind of screen in the
// middle of a walk, and it read as a different program.
//
// There is no "what kind of edit is this" here, and there never will be. A
// pause has no trace — that is what makes it a pause — so there is nothing a
// rule could be written on, and offering the choice would be offering an
// answer that cannot be carried out.
func (m Model) drawWindows(b *strings.Builder) {
	w, ok := m.window()
	if !ok {
		fmt.Fprintf(b, "%s\n%s\n\n  %s\n", subject.Render("pauses between blocks"), hr(),
			dim.Render("this day has none"))
		return
	}
	b.WriteString(m.header(0, 0))

	fmt.Fprintf(b, "\n  %s  %s\n", dim.Render("pause "),
		subject.Render(w.From.Format("15:04")+"–"+w.To.Format("15:04")+"  "+hm(w.Duration())))
	between := w.Left + " → " + w.Right
	if w.Left != "" && w.Left == w.Right {
		between = w.Left + " on both sides"
	}
	fmt.Fprintf(b, "  %s\n", dim.Render("         "+between+
		fmt.Sprintf("  ·  pause %d of %d", m.at+1, len(m.doc.Windows))))
	fmt.Fprintf(b, "  %s\n", dim.Render(
		"         nothing was recorded here — no rule can be written on a pause"))
	if w.Answered {
		said := "not work"
		if w.Worked {
			said = named(w.Project)
			if w.Subject != "" {
				said += " / " + w.Subject
			}
		}
		fmt.Fprintf(b, "  %s %s\n", dim.Render("         answered:"), warn.Render(said))
	}

	fmt.Fprintf(b, "\n  %s\n", label.Render("PROJECT"))
	m.drawSlot(b, slotProject, candidateNames(w.Choices))
	fmt.Fprintf(b, "\n  %s\n", label.Render("SUBJECT"))
	m.drawSlot(b, slotSubject, append([]row{{"(none)", "the project, and no subject"}},
		candidateNames(w.Subjects)...))

	b.WriteString("\n" + hr() + "\n")
	done := "back"
	if m.pausesOnly {
		done = "done"
	}
	b.WriteString(keys(
		[2]string{"enter", "attach it to this"}, [2]string{"tab", "project / subject"},
		[2]string{"N", "new one"}, [2]string{"n", "not work"}))
	b.WriteString(keys([2]string{"s", "skip"}, [2]string{"←→", "another pause"},
		[2]string{"esc", done}, [2]string{"q", "quit"}))
}

func (m Model) drawSummary(b *strings.Builder) {
	d := m.doc
	fmt.Fprintf(b, "%s\n%s\n\n", subject.Render(d.Date.Format("Monday, 2 January 2006")), hr())
	fmt.Fprintf(b, "  %s %s   %s %s   %s\n",
		dim.Render("attention"), hm(d.Day.Attention),
		dim.Render("background"), hm(d.Day.Background),
		dim.Render(countedNote(d.Background.Count)))
	if d.Day.Padding > 0 {
		fmt.Fprintf(b, "  %s\n", dim.Render(fmt.Sprintf(
			"of which %s is writing and reading — the one part nothing measured",
			hm(d.Day.Padding))))
	}
	b.WriteString("\n")
	for _, p := range d.Day.Projects {
		fmt.Fprintf(b, "  %-24s %s\n", named(p.Name), hm(p.Attention+p.Background))
	}
	if left := len(d.Questions) + len(d.Encounters); left > 0 {
		fmt.Fprintf(b, "\n  %s\n", warn.Render(fmt.Sprintf(
			"%d questions left unanswered; what they cover keeps the name the rules gave it",
			left)))
	}
	if d.OneOffs > 0 {
		fmt.Fprintf(b, "  %s\n", dim.Render(fmt.Sprintf(
			"%d answers did not become rules", d.OneOffs)))
	}
	if n, done := 0, 0; true {
		for _, w := range d.Windows {
			n++
			if w.Answered {
				done++
			}
		}
		if n > 0 {
			fmt.Fprintf(b, "  %s\n", dim.Render(fmt.Sprintf(
				"%d of %d pauses answered — no measured number moves for them", done, n)))
		}
	}
	if m.view == viewDone {
		b.WriteString("\n" + chosen.Render("confirmed.") + "\n")
		return
	}
	b.WriteString("\n" + hr() + "\n")
	b.WriteString(keys([2]string{"c", "confirm the day"}, [2]string{"b", "blocks"},
		[2]string{"g", "background"}, [2]string{"w", "pauses"},
		[2]string{"esc", "back to the questions"}, [2]string{"q", "quit"}))
}

func countedNote(counted bool) string {
	if counted {
		return "counted"
	}
	return "not counted"
}

func groundOf(g report.Ground) string {
	if g == report.GroundNone {
		return "nothing names it"
	}
	return string(g)
}

func placeOf(p confirm.Place) string {
	switch {
	case p.Project != "":
		return "in " + p.Project
	case p.Left != "" && p.Right != "":
		return fmt.Sprintf("no name; %s on the left, %s on the right", p.Left, p.Right)
	case p.Left != "":
		return fmt.Sprintf("no name; %s on the left, nothing on the right", p.Left)
	case p.Right != "":
		return fmt.Sprintf("no name; nothing on the left, %s on the right", p.Right)
	}
	return "no name, and nothing named near it"
}

// cut shortens a line to fit beside the rest of the screen. A title is
// whatever a page called itself and some of them are a paragraph long.
func cut(s string, at int) string {
	r := []rune(s)
	if len(r) <= at {
		return s
	}
	return string(r[:at-1]) + "…"
}

func foldedList(list []confirm.Trace) string {
	parts := make([]string, 0, len(list))
	for _, t := range list {
		parts = append(parts, t.Value)
	}
	if len(parts) > 4 {
		return strings.Join(parts[:4], ", ") + fmt.Sprintf(" and %d more", len(parts)-4)
	}
	return strings.Join(parts, ", ")
}

func named(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// proposedTitle is a title that was really seen, escaped so that it is a
// literal. Never a pattern worked out from several of them: an expression
// nobody wrote is an expression nobody can check afterwards, and this one is
// meant to be cut down by hand before it is written.
func proposedTitle(q confirm.Question) string {
	if len(q.Titles) == 0 {
		return ""
	}
	return regexp.QuoteMeta(q.Titles[0])
}

// hm is the report's formatter, not a copy of it: a length printed here and
// the same length printed by `report` have to be the same string.
func hm(d time.Duration) string { return report.HM(d) }
