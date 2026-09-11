// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package report

import (
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

// The ground of a stretch is what a confirmed day is read back with in six
// months: this half hour rests on a rule somebody wrote, on a guess from a
// directory name, on the block next door, or on a hand.
//
// Every one of those values has to be producible. A vocabulary with a member
// nothing can ever emit is worse than a shorter vocabulary: it is written down
// in the schema, a reader looks for it, and its absence means nothing. This
// test is here because a review argued that three of the seven were dead, and
// the only way to settle that is to make each one appear.
func TestEveryGroundCanBeReached(t *testing.T) {
	d := dict{
		byCWD:   map[string]string{"/src/alpha": "Alpha"},
		subject: map[string]string{"about beta": "beta"},
	}
	named := func(clock, cwd string) event.Event {
		e := prompt(clock, "")
		e.CWD = cwd
		return e
	}
	guessed := func(clock, guess string) event.Event {
		e := prompt(clock, guess) // a project the source guessed and no rule names
		e.CWD = "/src/" + guess
		return e
	}

	events := []event.Event{
		// A rule names this block, and the visit inside it inherits.
		named("09:00:00", "/src/alpha"),
		visit("09:03:00", "unnamed.test", "a"),
		named("09:06:00", "/src/alpha"),

		// Nothing but browsing, with the same named block on both sides.
		visit("09:30:00", "unnamed.test", "b"),
		visit("09:33:00", "unnamed.test", "c"),
		named("10:00:00", "/src/alpha"),

		// A directory nothing names: the guess, kept as the fallback.
		guessed("11:00:00", "scratch"),
		guessed("11:04:00", "scratch"),

		// Browsing at the end of the day, with nothing at all after it.
		visit("21:00:00", "unnamed.test", "d"),
		visit("21:04:00", "unnamed.test", "e"),
	}

	// And two answers a person gave, where no rule could have.
	from := day(2026, 5, 4)
	opts := Options{
		Attribution: d,
		Manual: []Assignment{
			{From: from.Add(9*time.Hour + 31*time.Minute), To: from.Add(9*time.Hour + 32*time.Minute),
				Project: "Handmade"},
			{From: from.Add(11*time.Hour + 1*time.Minute), To: from.Add(11*time.Hour + 2*time.Minute),
				Project: "Rescued", OneOff: true},
		},
	}

	rep := Build(events, from, from.AddDate(0, 0, 1), opts)
	seen := map[Ground]bool{}
	for _, s := range rep.Days[0].Stretches {
		seen[s.Ground] = true
	}
	for _, want := range []Ground{
		GroundRule, GroundFallback, GroundInherited,
		GroundNeighbours, GroundOneNeighbour, GroundManual, GroundOneOff,
	} {
		if !seen[want] {
			t.Errorf("no stretch of this day rests on %q, and the schema says one can", want)
		}
	}

	// And a stretch with a kind but no ground is a stretch nothing named,
	// which has to stay expressible too.
	unnamed := Build([]event.Event{
		visit("09:00:00", "unnamed.test", "a"),
		visit("09:03:00", "unnamed.test", "b"),
	}, from, from.AddDate(0, 0, 1), Options{Attribution: dict{}})
	for _, s := range unnamed.Days[0].Stretches {
		if s.Ground != GroundNone {
			t.Errorf("a day nothing names has a stretch resting on %q", s.Ground)
		}
	}
}
