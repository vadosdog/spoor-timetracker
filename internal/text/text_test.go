// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package text

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// This package was extracted from the browser source on the argument that
// there has to be exactly one of it. It arrived without tests of its own,
// covered only through its first caller — which is fine until the second
// caller is the one that breaks it.

func TestSanitiseRepairsAndReplaces(t *testing.T) {
	for _, c := range []struct {
		name, in, want string
		with           rune
	}{
		{"a NUL would shorten SQLite's own length()", "a\x00b", "a b", ' '},
		{"a newline turns one row of sqlite3 -line into two", "a\nb", "a b", ' '},
		{"U+2028 is a line break to most things that render text", "a\u2028b", "a b", ' '},
		{"U+202E reverses everything after it", "safe\u202etxt.exe", "safe txt.exe", ' '},
		{"a zero-width joiner is a format character", "a\u200db", "a b", ' '},
		{"an identifier gets U+FFFD rather than a space", "a\x00b", "a�b", Replacement},
		{"ordinary text is untouched", "Weekly sync — 1:1", "Weekly sync — 1:1", ' '},
		{"an invisible letter is not a format control and stays", "a\u3164b", "a\u3164b", ' '},
	} {
		if got := Sanitise(c.in, c.with); got != c.want {
			t.Errorf("%s: Sanitise(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// A Latin-1 path or a directory name is bytes, not text. SQLite stores what it
// cannot decode and then refuses to return that column at all, which fails the
// whole query rather than answering it wrongly.
func TestSanitiseRepairsInvalidUTF8(t *testing.T) {
	got := Sanitise("caf\xe9", Replacement)
	if !utf8.ValidString(got) {
		t.Errorf("Sanitise left invalid UTF-8: %q", got)
	}
	if got != "caf�" {
		t.Errorf("Sanitise(%q) = %q", "caf\xe9", got)
	}
}

func TestTruncateCountsRunesAndNeverDeletes(t *testing.T) {
	if got := Truncate("абвгд", 3); got != "абв" {
		t.Errorf("Truncate cut bytes rather than runes: %q", got)
	}
	if got := Truncate("ab", 5); got != "ab" {
		t.Errorf("Truncate shortened a string under the limit: %q", got)
	}
	// Cut, never delete: deleting the offending part of "a%0d%0ab" would make
	// "ab", a value that never existed.
	if got := Sanitise("a\r\nb", ' '); got != "a  b" {
		t.Errorf("Sanitise removed characters instead of replacing them: %q", got)
	}
}

func TestPrintableCapsTitles(t *testing.T) {
	long := strings.Repeat("x", MaxTitle+100)
	if n := len([]rune(Printable(long))); n != MaxTitle {
		t.Errorf("Printable produced %d runes, want it capped at %d", n, MaxTitle)
	}
}

// Every caller relies on this predicate agreeing with what Sanitise does, and
// two sources now do.
func TestUnsafeAgreesWithSanitise(t *testing.T) {
	for _, r := range []rune{'\x00', '\n', '\r', '\u2028', '\u2029', '\u202e', '\u200d'} {
		if !Unsafe(r) {
			t.Errorf("Unsafe(%q) = false", r)
		}
		if strings.ContainsRune(Sanitise(string(r), ' '), r) {
			t.Errorf("Sanitise kept %q although Unsafe says it is not safe", r)
		}
	}
	for _, r := range []rune{'a', 'я', ' ', '—', '\u3164'} {
		if Unsafe(r) {
			t.Errorf("Unsafe(%q) = true", r)
		}
	}
}
