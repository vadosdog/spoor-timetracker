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

			// The partition. It is what a confirmed day is stored as, so a
			// stretch missing from it is an hour that will be missing from
			// the frozen day for ever — and the frozen day is not recomputed,
			// which is the point of freezing it. Nothing else in this file
			// would notice: the totals are summed on their own path.
			var inStretches, stretchAttention, stretchPadding time.Duration
			var last time.Time
			for _, s := range d.Stretches {
				if !s.To.After(s.From) {
					where("stretch %s–%s is empty or runs backwards", s.From, s.To)
				}
				if s.From.Before(date) || s.To.After(end) {
					where("stretch %s–%s is outside the day", s.From, s.To)
				}
				if !last.IsZero() && s.From.Before(last) {
					where("stretch at %s overlaps the one before it", s.From)
				}
				last = s.To
				inStretches += s.Duration()
				switch s.Kind {
				case KindAttention:
					stretchAttention += s.Duration()
				case KindPadding:
					stretchAttention += s.Duration()
					stretchPadding += s.Duration()
				case KindBackground:
				default:
					where("stretch at %s has no kind", s.From)
				}
			}
			if inStretches != d.Active {
				where("the partition holds %s, the day holds %s", inStretches, d.Active)
			}
			if stretchAttention != d.Attention {
				where("the partition holds %s of attention, the day holds %s",
					stretchAttention, d.Attention)
			}
			if stretchPadding != d.Padding {
				where("the partition holds %s of padding, the day holds %s",
					stretchPadding, d.Padding)
			}
			// And it agrees with the rows, project by project. The totals and
			// the partition are filled on the same pass but from different
			// variables, and an assignment that moved one without the other
			// would show up here and nowhere else.
			perProject := map[string]time.Duration{}
			for _, s := range d.Stretches {
				perProject[s.Project] += s.Duration()
			}
			for _, p := range d.Projects {
				if got := perProject[p.Name]; got != p.Attention+p.Background {
					where("project %q holds %s in the partition and %s in its row",
						p.Name, got, p.Attention+p.Background)
				}
				delete(perProject, p.Name)
			}
			for name, held := range perProject {
				where("the partition holds %s under %q, which has no row", held, name)
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

// A pause somebody called work must never be counted as active time as well.
//
// The claimed line means "between blocks, where nothing was recorded". Block
// boundaries move whenever a setting does, so an answer given yesterday can
// find itself partly inside a block today — and counting it in both places is
// the same hour twice, which is the mistake this whole project is written
// against. The other direction matters too: an answer must not evaporate
// because a threshold moved.
func TestAClaimedPauseIsNeverAlsoActive(t *testing.T) {
	for seed := int64(0); seed < 120; seed++ {
		nextID = 0
		rng := rand.New(rand.NewSource(seed))
		events := randomDay(rng)
		from := day(2026, 5, 4)

		// Find the day's pauses at one setting, answer them all, then read the
		// day back at a different one — which is what a config edit does.
		loose := Options{ClusterGap: 30 * time.Minute}
		var answers []Pause
		for _, d := range Build(events, from, from.AddDate(0, 0, 2), loose).Days {
			for _, r := range d.Runs {
				if r.Gap {
					answers = append(answers, Pause{From: r.From, To: r.To, Worked: true, Project: "claimed"})
				}
			}
		}
		if len(answers) == 0 {
			continue
		}

		tight := Options{ClusterGap: 5 * time.Minute, Head: 4 * time.Minute, Tail: 4 * time.Minute, Pauses: answers}
		rep := Build(events, from, from.AddDate(0, 0, 2), tight)
		for di, d := range rep.Days {
			date := from.AddDate(0, 0, di)
			// Nothing claimed may overlap anything active.
			var active time.Duration
			for _, r := range d.Runs {
				if !r.Gap {
					active += r.To.Sub(r.From)
				}
			}
			// Against the measured time itself, not against the length of a
			// day. "Claimed plus active is under 24 hours" is satisfied by an
			// hour counted as both, which on a generated day it nearly always
			// is — the bound has to be the overlap, and the overlap has to be
			// none. The pauses being claimed here are the gaps of a looser
			// partition, so under a tighter one they may fall inside a block:
			// whatever a block covers is measured time, and measured time is
			// never also claimed.
			var overlap time.Duration
			for _, a := range answers {
				for _, r := range d.Runs {
					if r.Gap {
						continue
					}
					lo, hi := a.From, a.To
					if r.From.After(lo) {
						lo = r.From
					}
					if r.To.Before(hi) {
						hi = r.To
					}
					if hi.After(lo) {
						overlap += hi.Sub(lo)
					}
				}
			}
			if d.Claimed > 0 && d.Claimed+active > dayLength(date)-overlap+time.Second {
				t.Errorf("seed %d, day %s: claimed %s and active %s together exceed "+
					"the day less the %s they share — some of it is counted twice",
					seed, date.Format("01-02"), d.Claimed, active, overlap)
			}
			// And the claimed total is still the projects' claimed added up.
			var byProject time.Duration
			for _, p := range d.Projects {
				byProject += p.Claimed
			}
			if byProject != d.Claimed {
				t.Errorf("seed %d, day %s: rows hold %s of claimed, the day says %s",
					seed, date.Format("01-02"), byProject, d.Claimed)
			}
		}
	}
}

// Two answers that overlap each other are one stretch of a day, not two.
//
// The ordinary way there: answer a pause, then an import splits it, so the two
// halves are asked about again while the whole is still on record. Adding all
// three would count the overlap twice — quietly more than there was, which is
// what this project exists to prevent.
func TestOverlappingAnswersAreCountedOnce(t *testing.T) {
	from := day(2026, 5, 4)
	events := []event.Event{
		prompt("09:00:00", "alpha"), prompt("09:05:00", "alpha"),
		prompt("13:00:00", "alpha"), prompt("13:05:00", "alpha"),
	}
	gap := func() (time.Time, time.Time) {
		for _, r := range Build(events, from, from.AddDate(0, 0, 1), Options{}).Days[0].Runs {
			if r.Gap {
				return r.From, r.To
			}
		}
		t.Fatal("no gap in this fixture")
		return time.Time{}, time.Time{}
	}
	at, until := gap()
	half := at.Add(until.Sub(at) / 2)

	for _, c := range []struct {
		name   string
		pauses []Pause
	}{
		{"the whole, then both halves", []Pause{
			{From: at, To: until, Worked: true, Project: "alpha"},
			{From: at, To: half, Worked: true, Project: "alpha"},
			{From: half, To: until, Worked: true, Project: "alpha"},
		}},
		{"both halves, then the whole", []Pause{
			{From: at, To: half, Worked: true, Project: "alpha"},
			{From: half, To: until, Worked: true, Project: "alpha"},
			{From: at, To: until, Worked: true, Project: "alpha"},
		}},
		{"the same answer twice", []Pause{
			{From: at, To: until, Worked: true, Project: "alpha"},
			{From: at, To: until, Worked: true, Project: "alpha"},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Build(events, from, from.AddDate(0, 0, 1), Options{Pauses: c.pauses})
			if want := until.Sub(at); got.Days[0].Claimed != want {
				t.Errorf("claimed %s, want %s — the overlap was counted twice",
					got.Days[0].Claimed, want)
			}
			var byProject time.Duration
			for _, p := range got.Days[0].Projects {
				byProject += p.Claimed
			}
			if byProject != got.Days[0].Claimed {
				t.Errorf("rows hold %s and the day says %s", byProject, got.Days[0].Claimed)
			}
		})
	}
}

// dayLength is how long a local day is, which is not always 24 hours.
func dayLength(date time.Time) time.Duration {
	return date.AddDate(0, 0, 1).Sub(date)
}
