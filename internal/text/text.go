// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package text repairs strings that arrive from somewhere else before they
// are stored.
//
// Every source reads bytes somebody else wrote: a browser's history database,
// a directory name, an iCalendar feed off the network. None of them promises
// valid UTF-8, and none of them promises the text displays as what it says.
// SQLite stores bytes it cannot decode and then refuses to return that column
// at all — so one bad row fails the whole query rather than answering it
// wrongly, and "SELECT *" is what the README calls the whole record.
//
// This lives in its own package rather than inside one source because there
// has to be exactly one of it. The browser source got it wrong twice, each
// time in a column the previous fix did not reach; a second copy in a second
// source is the same mistake with more room.
package text

import (
	"strings"
	"unicode"
)

// MaxTitle caps a title. A title is the one field an arbitrary web page — or
// an arbitrary meeting invitation — fills in entirely, and an unbounded value
// out of somebody else's data has no business in a column a person is expected
// to read. Chrome already stores at most this many characters, so on real
// browser data nothing is lost.
const MaxTitle = 4096

// Replacement stands in for anything taken out of a value that is an
// identifier rather than prose — a host, a profile name, a calendar id. A
// space would be wrong in those, and U+FFFD is the honest signal that
// something was removed. Prose gets a space instead, because two words split
// by a newline must not become one word.
const Replacement = '\uFFFD'

// Truncate cuts s to at most n runes.
//
// Cut, never delete: deleting the offending part of a value turns "a%0d%0ab"
// into "ab", a name that never existed.
func Truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// Sanitise makes a string safe to store and safe to read back: bytes that are
// not UTF-8 at all are repaired, and runes that would make the text display as
// something it is not are replaced by with.
//
// The repair loses fidelity, knowingly: ToValidUTF8 collapses a run of invalid
// bytes into one replacement, so "caf\xE9" and "caf\xFF\xFE\xFD" both come
// back as "caf\uFFFD". Two different values become one. Nothing that could
// leak is lost, and the alternative is a column that cannot be read.
func Sanitise(s string, with rune) string {
	return strings.Map(func(r rune) rune {
		if Unsafe(r) {
			return with
		}
		return r
	}, strings.ToValidUTF8(s, string(Replacement)))
}

// Printable prepares prose for storage: a page title, a meeting summary.
// What is taken out becomes a space rather than a replacement character, and
// the result is capped at MaxTitle.
//
// This is the column the README tells a person to read with their own eyes,
// and whoever wrote the page or the invitation chose its text. A NUL makes
// SQLite's length() report a value shorter than it is. A newline turns one row
// of `sqlite3 -line` into two, and so do U+2028 and U+0085 in most of what
// renders text. U+202E reverses everything after it, so a title that reads
// "safe<U+202E>txt.exe" on screen is not the title that was stored.
func Printable(s string) string {
	return Truncate(Sanitise(s, ' '), MaxTitle)
}

// Unsafe reports whether a rune would misrepresent the text it sits in:
// the C0 and C1 controls, the line and paragraph separators, and the format
// characters — which is the category the direction overrides belong to.
//
// Runes that are invisible but are letters rather than format controls
// (U+3164 HANGUL FILLER, U+2800 BRAILLE PATTERN BLANK) are deliberately left
// alone. They can pad a title; they cannot misrepresent one, and excluding
// them would mean an allow-list of scripts.
func Unsafe(r rune) bool {
	return unicode.IsControl(r) ||
		r == '\u2028' || r == '\u2029' ||
		unicode.Is(unicode.Cf, r)
}
