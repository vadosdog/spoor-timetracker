// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package export writes a confirmed day out of spoor.
//
// One interface, one implementation. The interface is here from the start
// because the second implementation — a work log posted to an issue tracker —
// is a later stage, and adding it should not mean taking the terminal apart.
//
// What is exported is the snapshot of a confirmed day, never a recomputation.
// A file somebody filed last week must not quietly say something else this
// week because a rule changed in between.
package export

import (
	"fmt"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Exporter turns a confirmed day into something outside spoor.
type Exporter interface {
	// Name is what --format takes.
	Name() string
	// Export writes the day. Two calls with the same day produce the same
	// bytes: an export is a document, and a document that changes between two
	// identical runs cannot be diffed, filed or trusted.
	Export(day store.ConfirmedDay, w io.Writer) error
}

// Options are the things a person chooses at the moment of exporting.
type Options struct {
	// Timeline adds the day as a schedule under the tables.
	Timeline bool
	// Unconfirmed marks a day that has not been confirmed, and the mark is not
	// optional: a day that may still move must say so on the page.
	Unconfirmed bool
}

// Markdown writes the day as a markdown file.
type Markdown struct{ Options }

// Name is what --format takes.
func (Markdown) Name() string { return "markdown" }

// Export writes the day.
//
// No trace appears anywhere in it — not a host, not a key, not a directory,
// not a page title. This is the first thing that leaves the tool, and a page
// title is sometimes a search query. The rule is enforced by a test that greps
// the output for a distinctive title, in the same way the two renderers are.
//
// Two exceptions, both deliberate rather than overlooked, and both switched
// off by one line of config.
//
// A project no rule names keeps the name its working directory gave it —
// `fallback: cwd-basename`, on by default — and that name is a directory name.
// It is printed, because it is the name that project has in every other view
// and an export that renamed it would be a different document about the same
// day. Somebody for whom a directory name is too much turns the guess off, or
// names the project; both are the same decision they already made for the
// report.
//
// And a subject with no name of its own takes it from the first capture group
// of its expression, so what is printed is a piece of text the expression
// matched — off a page title, a branch or a path. That is the point of the
// shape and it is why the config offers it, but it is the exception that
// carries the most: an expression loose enough to capture half a title puts
// half a title in the export. Naming the subject removes it, because a named
// subject stores the name rather than the match.
func (m Markdown) Export(day store.ConfirmedDay, w io.Writer) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", day.Day)

	projects, subjects := split(day.Rows)
	var attention, background, padding time.Duration
	for _, r := range projects {
		attention += r.Attention
		background += r.Background
		padding += r.Padding
	}

	if m.Unconfirmed {
		// Said first, because everything under it is provisional. A day that
		// has not been confirmed is still computed from the rules, so it moves
		// the next time one of them does.
		b.WriteString("Not confirmed: these numbers are still computed from the rules " +
			"and will move when a rule does.\n\n")
	} else {
		fmt.Fprintf(&b, "Confirmed %s.\n", day.ConfirmedAt.Local().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(&b, "Attention %s", hm(attention))
	if padding > 0 {
		// Named on its own line rather than folded in: it is the one part of
		// the day nothing measured, and hiding it in a total is how an
		// estimate turns into a number.
		fmt.Fprintf(&b, ", of which %s is writing and reading", hm(padding))
	}
	b.WriteString(".\n")
	// The background line is printed whether or not it was counted. Whether
	// the agent working alone is your working time is a decision, and a
	// decision that leaves no trace on the page cannot be revisited.
	fmt.Fprintf(&b, "Background %s — %s.\n", hm(background), counted(day.CountBackground))
	// Printed whenever it is not zero, and never added in. It is the one line
	// on the page that is somebody's answer rather than a measurement, and
	// saying so is the difference between a report and a claim.
	if day.Claimed > 0 {
		fmt.Fprintf(&b, "Pauses called work %s — between blocks, where nothing was recorded; "+
			"not part of the time above.\n", hm(day.Claimed))
	}
	if span := spanOf(day); span > 0 {
		fmt.Fprintf(&b, "Day covered %.0f%% of %s from the first block to the last.\n",
			100*float64(attention+background)/float64(span), hm(span))
	}
	if day.OneOffCount > 0 {
		fmt.Fprintf(&b, "%s did not become a rule.\n", count(day.OneOffCount, "assignment", "assignments"))
	}
	b.WriteString("\n")

	if len(projects) == 0 {
		b.WriteString("Nothing was recorded on this day.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	// The claimed column appears only when there is one: a page with an empty
	// column on every row teaches the reader to stop looking at it.
	var anyClaimed bool
	for _, r := range projects {
		anyClaimed = anyClaimed || r.Claimed > 0
	}
	if anyClaimed {
		b.WriteString("| Project | Time | Pauses | |\n|---|---|---|---|\n")
		for _, r := range projects {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", name(r.Project),
				hm(r.Attention+r.Background), dashZero(r.Claimed), work(r.Work))
		}
	} else {
		b.WriteString("| Project | Time | |\n|---|---|---|\n")
		for _, r := range projects {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", name(r.Project), hm(r.Attention+r.Background), work(r.Work))
		}
	}

	for _, r := range projects {
		own := subjects[r.Project]
		if len(own) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s — %s\n\n", name(r.Project), hm(r.Attention+r.Background))
		var named time.Duration
		for _, s := range own {
			named += s.Attention + s.Background
		}
		total := r.Attention + r.Background
		// Said out loud, because it is true and surprising: a subject takes
		// its time from its own traces and never inherits, so the subjects of
		// a project do not add up to it.
		fmt.Fprintf(&b, "Subjects do not add up to the project: %s of %s.\n\n", hm(named), hm(total))
		// The claimed column appears only where there is one, for the same
		// reason it does above the project table.
		var anySubjectClaimed bool
		for _, s := range own {
			anySubjectClaimed = anySubjectClaimed || s.Claimed > 0
		}
		if anySubjectClaimed {
			b.WriteString("| Subject | Time | Pauses |\n|---|---|---|\n")
			for _, s := range own {
				fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Subject,
					hm(s.Attention+s.Background), dashZero(s.Claimed))
			}
		} else {
			b.WriteString("| Subject | Time |\n|---|---|\n")
			for _, s := range own {
				fmt.Fprintf(&b, "| %s | %s |\n", s.Subject, hm(s.Attention+s.Background))
			}
		}
		// The last row takes the same number of cells as the header above it.
		// A markdown table with a short row renders as a table with a hole in
		// it, and this row is the one that says the subjects do not add up.
		if rest := total - named; rest > 0 {
			if anySubjectClaimed {
				fmt.Fprintf(&b, "| the rest of the project | %s | %s |\n", hm(rest), dashZero(0))
			} else {
				fmt.Fprintf(&b, "| the rest of the project | %s |\n", hm(rest))
			}
		}
	}

	if m.Timeline {
		b.WriteString("\n## Timeline\n\n| From | To | Project | Subject | On what grounds |\n|---|---|---|---|---|\n")
		for _, s := range day.Stretches {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				s.From.Local().Format("15:04"), s.To.Local().Format("15:04"),
				name(s.Project), dash(s.Subject), dash(s.Ground))
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// split separates the project rows from the subject rows and puts both in the
// order the page shows them: busiest first, and by name where two are equal, so
// that two exports of one day are the same bytes.
func split(rows []store.ConfirmedRow) ([]store.ConfirmedRow, map[string][]store.ConfirmedRow) {
	var projects []store.ConfirmedRow
	subjects := map[string][]store.ConfirmedRow{}
	for _, r := range rows {
		if r.Subject == "" {
			projects = append(projects, r)
			continue
		}
		subjects[r.Project] = append(subjects[r.Project], r)
	}
	byTime := func(list []store.ConfirmedRow) {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			at, bt := a.Attention+a.Background, b.Attention+b.Background
			if at != bt {
				return at > bt
			}
			if a.Project != b.Project {
				return a.Project < b.Project
			}
			return a.Subject < b.Subject
		})
	}
	byTime(projects)
	for k := range subjects {
		byTime(subjects[k])
	}
	return projects, subjects
}

// spanOf is the first block of the day to the last, taken from the partition
// rather than stored: the stretches cover the blocks and nothing else, so their
// two ends are exactly what the coverage number divides by.
func spanOf(day store.ConfirmedDay) time.Duration {
	if len(day.Stretches) == 0 {
		return 0
	}
	first, last := day.Stretches[0].From, day.Stretches[0].To
	for _, s := range day.Stretches {
		if s.From.Before(first) {
			first = s.From
		}
		if s.To.After(last) {
			last = s.To
		}
	}
	return last.Sub(first)
}

func name(project string) string {
	if project == "" {
		return "(no project)"
	}
	return project
}

// dashZero prints a length, or a dash when there is none. Zero in a column of
// times reads as a measured nothing; a dash reads as nothing to say.
func dashZero(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return hm(d)
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func work(w *bool) string {
	switch {
	case w == nil:
		return "not said"
	case *w:
		return "work"
	default:
		return "personal"
	}
}

func counted(yes bool) string {
	if yes {
		return "counted"
	}
	return "not counted"
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// hm is the report's formatter, not a copy of it: a length printed here and
// the same length printed by `report` have to be the same string.
func hm(d time.Duration) string { return report.HM(d) }

// For finds an exporter by name.
func For(format string, opts Options) (Exporter, error) {
	switch format {
	case "", "markdown":
		return Markdown{Options: opts}, nil
	default:
		return nil, fmt.Errorf("no such format as %q; there is markdown", format)
	}
}
