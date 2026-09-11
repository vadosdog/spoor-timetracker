// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package tui

import "github.com/charmbracelet/lipgloss"

// Colour here is structure, not decoration.
//
// A screen of this shape — a trace, its evidence, two lists of candidates and
// a row of shortcuts — reads as one block of grey text without it, and the
// thing somebody has to find first is the one line that says what the trace
// counts for now. So: one accent for what is selected, one for the letters
// that do something, and everything that is context is dimmed away from the
// answer.
//
// Adaptive colours, so the same screen is legible on a light terminal and a
// dark one. lipgloss decides how much colour the terminal can take and writes
// nothing at all when the output is not a terminal, which is also what keeps
// the tests reading plain strings.
var (
	// dim is context: labels, counts, the row of shortcuts.
	dim = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "244"})
	// label is the name of a section — WHERE, PROJECT, SUBJECT.
	label = lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "247"})
	// subject is the thing being decided about: the trace itself.
	subject = lipgloss.NewStyle().Bold(true)
	// chosen is the line under the cursor.
	chosen = lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "81"})
	// shortcut is a letter that does something.
	shortcut = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "114"})
	// warn is something worth reading before pressing anything else.
	warn = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	// rule is the horizontal line between the parts of a screen.
	rule = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "252", Dark: "238"})
)

// hr is the line that separates the header, the body and the shortcuts.
func hr() string {
	return rule.Render("──────────────────────────────────────────────────────────────────────")
}

// keys renders a row of shortcuts, with each letter picked out of its own
// description: "[t] name it by the title" is two things, and the eye should
// find the first without reading the second.
func keys(pairs ...[2]string) string {
	out := ""
	for i, p := range pairs {
		if i > 0 {
			out += dim.Render("   ")
		}
		out += shortcut.Render(p[0]) + dim.Render(" "+p[1])
	}
	return "  " + out + "\n"
}
