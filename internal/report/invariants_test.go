// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package report

import (
	"math/rand"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

// The report's numbers have to agree with each other, not only with their own
// definitions.
//
// That distinction is the lesson of this stage. Twice a number was correct by
// its own definition and wrong beside the one printed next to it: a project's
// wall time came out shorter than the attention inside it, and later longer
// than the day it was printed on. Both passed every test there was, because
// every test asked one number whether it was right.
//
// So these are cross-checks, run over randomly built days rather than over
// fixtures somebody chose. A fixture tests the case its author thought of;
// meetings crossing midnight, nested in each other and overlapping browsing
// are exactly the cases nobody thinks of.
func TestTheNumbersAgreeWithEachOther(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		// Reset with the seed: external ids come from a package-global
		// counter and are the final tie-break when two events share an
		// instant, so without this a failure reported as "seed 47" would not
		// reproduce under -run.
		nextID = 0
		rng := rand.New(rand.NewSource(seed))
		events := randomDay(rng)

		from := day(2026, 5, 4)
		opts := Options{
			ClusterGap:      time.Duration(5+rng.Intn(30)) * time.Minute,
			AttentionWindow: time.Duration(rng.Intn(15)) * time.Minute,
			Head:            time.Duration(rng.Intn(5)) * time.Minute,
			Tail:            time.Duration(rng.Intn(5)) * time.Minute,
		}
		rep := Build(events, from, from.AddDate(0, 0, 2), opts)

		for di, d := range rep.Days {
			date := from.AddDate(0, 0, di)
			end := date.AddDate(0, 0, 1)
			where := func(f string, a ...any) {
				t.Helper()
				t.Errorf("seed %d, day %s: "+f, append([]any{seed, date.Format("01-02")}, a...)...)
			}

			// A day holds at most a day — and a day is not 24 hours. The one
			// with a daylight-saving change in it is 23 or 25, which
			// nextMidnight is written to honour, so the bound has to be the
			// day's own length. Written as 24h this passed only because the
			// fixture zone has no transitions: a false invariant waiting for
			// somebody to run the suite somewhere real.
			if d.Span > end.Sub(date) {
				where("span %s is longer than the day itself (%s)", d.Span, end.Sub(date))
			}
			if d.Active > d.Span {
				where("active %s exceeds span %s", d.Active, d.Span)
			}
			if d.Attention+d.Background != d.Active {
				where("attention %s + background %s != active %s",
					d.Attention, d.Background, d.Active)
			}
			if d.Padding > d.Attention {
				where("padding %s exceeds attention %s", d.Padding, d.Attention)
			}

			// Nothing may sit outside the day it is printed under.
			if !d.First.IsZero() && (d.First.Before(date) || d.First.After(end)) {
				where("first trace %s is outside the day", d.First)
			}
			if !d.Last.IsZero() && (d.Last.Before(date) || d.Last.After(end)) {
				where("last trace %s is outside the day", d.Last)
			}
			for _, c := range d.Clusters {
				if c.From.Before(date) || c.To.After(end) || c.To.Before(c.From) {
					where("block %s–%s is outside the day or runs backwards", c.From, c.To)
				}
			}

			// The schedule tiles forward and never overlaps itself.
			var prev time.Time
			for _, r := range d.Runs {
				if r.To.Before(r.From) {
					where("run %s–%s runs backwards", r.From, r.To)
				}
				if !prev.IsZero() && r.From.Before(prev) {
					where("run at %s overlaps the one before it", r.From)
				}
				if r.From.Before(date) || r.To.After(end) {
					where("run %s–%s is outside the day", r.From, r.To)
				}
				prev = r.To
			}

			// Blocks and the schedule are filled on a separate path from the
			// day's totals, so these two are the cross-checks that can
			// actually disagree — unlike attention+background, which restates
			// an assignment. A halved cluster or run total survives every
			// other property in this file.
			var inClusters, inRuns, runAttention time.Duration
			for _, c := range d.Clusters {
				inClusters += c.Attention + c.Background
			}
			for _, r := range d.Runs {
				inRuns += r.Attention + r.Background
				runAttention += r.Attention
			}
			if inClusters != d.Active {
				where("blocks hold %s between them, the day holds %s", inClusters, d.Active)
			}
			if inRuns != d.Active {
				where("the schedule holds %s, the day holds %s", inRuns, d.Active)
			}
			if runAttention != d.Attention {
				where("the schedule holds %s of attention, the day holds %s",
					runAttention, d.Attention)
			}

			// Every second counted for a project is counted for the day, and
			// no project claims more of the clock than the day itself has.
			var sum time.Duration
			for _, p := range d.Projects {
				sum += p.Attention + p.Background
				if p.Wall > d.Span {
					where("project %q has %s of wall on a day spanning %s",
						p.Name, p.Wall, d.Span)
				}
				if p.Attention < 0 || p.Background < 0 || p.Wall < 0 {
					where("project %q has a negative number", p.Name)
				}
			}
			if sum != d.Active {
				where("projects hold %s between them, the day holds %s", sum, d.Active)
			}
		}

		// And the range's totals are the days added up.
		var active, span time.Duration
		for _, d := range rep.Days {
			active += d.Active
			span += d.Span
		}
		if rep.Total.Active != active || rep.Total.Span != span {
			t.Errorf("seed %d: totals say %s/%s, the days add up to %s/%s",
				seed, rep.Total.Active, rep.Total.Span, active, span)
		}

		// And the whole thing is a function of its input.
		again := Build(events, from, from.AddDate(0, 0, 2), opts)
		if again.Total.Active != rep.Total.Active || again.Total.Span != rep.Total.Span {
			t.Errorf("seed %d: two runs over the same events disagree", seed)
		}
	}
}

// randomDay builds a plausible-looking day: prompts, agent messages, browsing
// and meetings, some of which cross midnight, nest inside each other or start
// at the same instant.
func randomDay(rng *rand.Rand) []event.Event {
	var out []event.Event
	for i := 0; i < 3+rng.Intn(25); i++ {
		clock := time.Duration(rng.Intn(48*60)) * time.Minute
		at := day(2026, 5, 4).Add(clock)
		project := []string{"alpha", "beta", ""}[rng.Intn(3)]

		switch rng.Intn(4) {
		case 0:
			e := prompt("00:00:00", project)
			e.TS = stamp(at)
			out = append(out, e)
		case 1:
			e := machine("00:00:00", project)
			e.TS = stamp(at)
			out = append(out, e)
		case 2:
			e := visit("00:00:00", "example.invalid", "docs")
			e.TS = stamp(at)
			out = append(out, e)
		default:
			// Lengths chosen to straddle midnight often, and to include the
			// exact bound the source refuses above.
			mins := []int{5, 30, 60, 90, 240, 8 * 60, 20 * 60}[rng.Intn(7)]
			e := meeting("00:00:00", time.Duration(mins)*time.Minute, "call")
			e.TS = stamp(at)
			e.Project = project
			out = append(out, e)
		}
	}
	return out
}

// Widening the attention window can never produce less attention.
//
// A weaker check than it looks, and worth saying so. It was written to catch
// the other kind of wrong number — the merge that dropped the front of an
// interval kept attention + background = active, so every sum above still
// balanced while both halves were quietly too small — and it does not catch
// it: removing the sort in attentionWindows leaves this green. Only
// TestAttentionBeforeAMeetingIsNotLost sees that one.
//
// It stays because the property is real and a future breakage of the merge
// may well violate it, and because it pins the one setting whose zero does
// not mean zero.
func TestMoreWindowIsNeverLessAttention(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		nextID = 0
		rng := rand.New(rand.NewSource(seed))
		events := randomDay(rng)
		from := day(2026, 5, 4)

		var prev time.Duration
		// Not starting at zero: an unset window means "half the gap", not "no
		// window at all", which is the one value of this setting that does
		// not mean itself.
		for _, w := range []time.Duration{time.Minute, 3 * time.Minute, 5 * time.Minute,
			10 * time.Minute, 20 * time.Minute, 40 * time.Minute} {
			rep := Build(events, from, from.AddDate(0, 0, 2), Options{
				ClusterGap: time.Hour, AttentionWindow: w, Head: 0, Tail: 0,
			})
			if rep.Total.Attention < prev {
				t.Fatalf("seed %d: a %s window gives %s of attention, less than the window before it gave (%s)",
					seed, w, rep.Total.Attention, prev)
			}
			prev = rep.Total.Attention
		}
	}
}
