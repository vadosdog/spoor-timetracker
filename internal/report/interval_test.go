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

// A meeting is the one event that knows its own length. Everything in this
// file is about the difference that makes, because every other source
// produces points and the report was built for points alone.

// meeting is a calendar event: an interval, with a title and no project of its
// own unless a rule gives it one.
func meeting(clock string, length time.Duration, summary string) event.Event {
	e := base("calendar", stamp(at(clock)))
	ms := length.Milliseconds()
	e.Type, e.Subtype, e.Title, e.DurationMS = "event", "", summary, &ms
	e.Entrypoint = "work"
	return e
}

// The whole point of the stage: an hour in a call is an hour of the day, and
// before this it was a hole.
func TestAMeetingIsAnHourOfTheDay(t *testing.T) {
	r := buildDayReport([]event.Event{
		meeting("12:00:00", time.Hour, "Design review"),
	}, Options{Head: 0, Tail: 0})

	if got, want := r.Total.Active, time.Hour; got != want {
		t.Errorf("active = %s, want %s", got, want)
	}
	if got, want := r.Total.Span, time.Hour; got != want {
		t.Errorf("span = %s, want %s", got, want)
	}
	// A call is a person in a room, not an agent grinding alone.
	if r.Total.Background != 0 {
		t.Errorf("background = %s, want none", r.Total.Background)
	}
}

// The hole it was added to fill: two blocks of work with a call between them.
// Before intervals the middle hour was a gap; it is not.
func TestAMeetingFillsTheGapBetweenTwoBlocks(t *testing.T) {
	events := []event.Event{
		prompt("10:00:00", "alpha"),
		prompt("14:00:00", "alpha"),
	}
	withoutIt := buildDayReport(events, Options{Head: 0, Tail: 0})

	withIt := buildDayReport(append(events, meeting("11:00:00", 2*time.Hour, "Planning")),
		Options{Head: 0, Tail: 0})

	if withoutIt.Total.Active != 0 {
		t.Fatalf("two lone prompts have %s of active time; the fixture is wrong", withoutIt.Total.Active)
	}
	if got, want := withIt.Total.Active, 2*time.Hour; got != want {
		t.Errorf("active = %s, want %s", got, want)
	}
	// And the day no longer shows a pause where the call was.
	for _, d := range withIt.Days {
		for _, run := range d.Runs {
			if run.Gap && !run.From.After(at("11:00:00")) && run.To.After(at("11:00:00")) {
				t.Errorf("the day still shows a gap from %s to %s, over the meeting",
					run.From.Format("15:04"), run.To.Format("15:04"))
			}
		}
	}
}

// The pause that ends a block runs from the end of what came before, not from
// its start. Measured from the start, an hour-long call would look like an
// hour-long pause and cut the block in half.
func TestTheGapIsMeasuredFromTheEndOfAMeeting(t *testing.T) {
	r := buildDayReport([]event.Event{
		meeting("12:00:00", time.Hour, "Call"),
		prompt("13:05:00", "alpha"),
	}, Options{ClusterGap: 10 * time.Minute, Head: 0, Tail: 0})

	if n := len(r.Days[0].Clusters); n != 1 {
		t.Fatalf("got %d blocks, want 1: the pause after the call is five minutes, not sixty-five", n)
	}
}

// A block ends when the last thing in it ends. A call that was still running
// when the last page was opened decides the end of the block, and with it the
// span the coverage number divides by.
func TestABlockEndsAtTheLastEndNotTheLastTimestamp(t *testing.T) {
	r := buildDayReport([]event.Event{
		meeting("12:00:00", time.Hour, "Call"),
		visit("12:10:00", "example.invalid", "docs"),
	}, Options{Head: 0, Tail: 0})

	c := r.Days[0].Clusters[0]
	if got, want := c.To, at("13:00:00"); !got.Equal(want) {
		t.Errorf("the block ends at %s, want %s", got.Format("15:04"), want.Format("15:04"))
	}
	if got, want := r.Total.Span, time.Hour; got != want {
		t.Errorf("span = %s, want %s", got, want)
	}
}

// The merge that only ever compared against the last interval.
//
// Intervals reach windows.add in entry order, and entries are ordered by when
// they start. A meeting starting at 12:00 therefore arrives before a prompt at
// 12:02 — whose window opens at 11:57, three minutes *earlier*. add only ever
// compares against the last interval, so it extended the meeting's window
// forwards and dropped those three minutes on the floor. Nothing failed; the
// day was quietly three minutes less attentive.
//
// It takes a third event before the meeting for the loss to be visible at all,
// because the minutes are only counted where some stretch covers them.
func TestAttentionBeforeAMeetingIsNotLost(t *testing.T) {
	r := buildDayReport([]event.Event{
		visit("11:50:00", "example.invalid", "docs"), // window 11:45–11:55
		meeting("12:00:00", time.Hour, "Call"),       // 12:00–13:00
		prompt("12:02:00", "alpha"),                  // window 11:57–12:07
	}, Options{AttentionWindow: 5 * time.Minute, Head: 0, Tail: 0})

	// One block, 11:50 to 13:00. Everything in it is attention except the two
	// minutes between the end of the visit's window and the start of the
	// prompt's — 11:55 to 11:57.
	if got, want := r.Total.Active, 70*time.Minute; got != want {
		t.Fatalf("active = %s, want %s", got, want)
	}
	if got, want := r.Total.Attention, 68*time.Minute; got != want {
		t.Errorf("attention = %s, want %s", got, want)
	}
	if got, want := r.Total.Background, 2*time.Minute; got != want {
		t.Errorf("background = %s, want %s", got, want)
	}
}

// A meeting owns its own hour. Time inside a call is not charged to whatever
// page happened to be open when it started.
func TestAMeetingOwnsItsOwnStretch(t *testing.T) {
	m := meeting("12:00:00", time.Hour, "Call")
	m.Project = "meetings"
	r := buildDayReport([]event.Event{
		prompt("11:55:00", "alpha"),
		m,
	}, Options{AttentionWindow: 5 * time.Minute, Head: 0, Tail: 0})

	p := projectOf(t, r, "meetings")
	if got, want := p.Attention, time.Hour; got != want {
		t.Errorf("the meeting's project holds %s, want %s", got, want)
	}
}

// Claude Code's turn_duration is a real duration and is deliberately not an
// interval. The turn already has events at both ends, so the block spans it
// either way; reading it here would count the same seconds twice and would
// quietly move every number measured before this stage.
func TestTurnDurationIsNotAnInterval(t *testing.T) {
	e := machine("12:00:00", "alpha")
	e.Type, e.Subtype = "system", "turn_duration"
	ms := (30 * time.Minute).Milliseconds()
	e.DurationMS = &ms

	r := buildDayReport([]event.Event{
		prompt("11:59:00", "alpha"),
		e,
	}, Options{Head: 0, Tail: 0})

	if got := r.Total.Span; got != time.Minute {
		t.Errorf("span = %s, want one minute: a turn's own duration is not a span", got)
	}
}

// Two calendars holding the same meeting are two rows, and the report must
// count the hour once rather than twice.
func TestTheSameMeetingFromTwoCalendarsIsCountedOnce(t *testing.T) {
	a := meeting("12:00:00", time.Hour, "Shared call")
	b := meeting("12:00:00", time.Hour, "Shared call")
	b.Entrypoint = "team"

	r := buildDayReport([]event.Event{a, b}, Options{Head: 0, Tail: 0})
	if got, want := r.Total.Active, time.Hour; got != want {
		t.Errorf("active = %s, want %s — the same hour counted twice", got, want)
	}
}

// A meeting nested inside a longer one must not pull the end of the block
// backwards.
func TestANestedMeetingDoesNotShortenTheBlock(t *testing.T) {
	r := buildDayReport([]event.Event{
		meeting("12:00:00", 2*time.Hour, "Workshop"),
		meeting("12:30:00", 15*time.Minute, "A slot inside it"),
	}, Options{Head: 0, Tail: 0})

	if got, want := r.Total.Span, 2*time.Hour; got != want {
		t.Errorf("span = %s, want %s", got, want)
	}
	if got, want := r.Total.Active, 2*time.Hour; got != want {
		t.Errorf("active = %s, want %s", got, want)
	}
}

// Two runs over the same events give the same report. The stretch tiling was
// rewritten for this stage and it sorts; a sort that is not stable would show
// up here.
func TestIntervalsAreDeterministic(t *testing.T) {
	events := []event.Event{
		prompt("09:00:00", "alpha"),
		meeting("09:30:00", 45*time.Minute, "Call"),
		visit("10:20:00", "example.invalid", "docs"),
		meeting("11:00:00", time.Hour, "Another"),
		prompt("12:30:00", "beta"),
	}
	first := buildDayReport(events, Options{})
	second := buildDayReport(events, Options{})
	if first.Total.Active != second.Total.Active || first.Total.Span != second.Total.Span {
		t.Errorf("two runs disagree: %v then %v", first.Total, second.Total)
	}
}

// A day holds at most a day. A meeting starting at 23:00 and running three
// hours belongs to the day it started on, and the hour past midnight is
// dropped rather than counted twice or added to a span longer than a day.
//
// The tail extension was always clamped this way; the block's own end was not
// once a meeting could push it, which made a day of 25 hours 55 minutes and a
// row labelled the 4th reporting a last trace dated the 5th.
func TestAMeetingDoesNotCarryTheDayPastMidnight(t *testing.T) {
	from := day(2026, 5, 4)
	r := Build([]event.Event{
		prompt("00:05:00", "alpha"),
		meeting("23:00:00", 3*time.Hour, "Long call"),
	}, from, from.AddDate(0, 0, 2), Options{Head: 0, Tail: 0})

	d := r.Days[0]
	if d.Span > 24*time.Hour {
		t.Errorf("span = %s, which is longer than a day", d.Span)
	}
	if !d.Last.Before(from.AddDate(0, 0, 1).Add(time.Nanosecond)) {
		t.Errorf("last trace of the 4th is %s", d.Last)
	}
	if got, want := r.Total.Active, time.Hour; got != want {
		t.Errorf("active = %s, want %s — only the part before midnight", got, want)
	}
	// And the hour after midnight is not also counted on the next day.
	if r.Days[1].Active != 0 {
		t.Errorf("the next day picked up %s from a meeting that started before it", r.Days[1].Active)
	}

	// Every number on the row, not only the ones the day struct holds: a
	// project's wall time is printed beside them and must describe the same
	// day. It read three hours here while the day's last trace was midnight.
	for _, p := range r.Days[0].Projects {
		if p.Wall > 24*time.Hour || p.Wall > d.Span {
			t.Errorf("project %q has %s of wall on a day spanning %s", p.Name, p.Wall, d.Span)
		}
	}
}

// A project's wall time is first trace to last, and a meeting is the one event
// that lasts. Reading only timestamps made wall come out shorter than the
// attention inside it — half an hour of wall against an hour of attention, for
// a day that was one long call.
func TestWallReachesTheEndOfAMeeting(t *testing.T) {
	m := meeting("12:00:00", time.Hour, "Call")
	m.Project = "meetings"
	r := buildDayReport([]event.Event{m}, Options{Head: 0, Tail: 0})

	p := projectOf(t, r, "meetings")
	if p.Wall < p.Attention {
		t.Errorf("wall %s is shorter than attention %s", p.Wall, p.Attention)
	}
	if got, want := p.Wall, time.Hour; got != want {
		t.Errorf("wall = %s, want %s", got, want)
	}
}
