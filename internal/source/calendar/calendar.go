// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package calendar reads meetings out of an iCalendar feed.
//
// Meetings are the one hole no local trace can fill. Nothing happens on disk
// during a call, so the report shows a gap exactly where an hour was busiest,
// and every other source in this project is silent about it by construction.
//
// Four things shape the package:
//
//   - Start, end and title. Not attendees, not the description, not the
//     conference link. Those are other people's personal data, and "metadata
//     only" is a boundary of the project rather than a setting. Attendees are
//     read in one place, to answer "did I decline this", and go no further.
//   - A meeting is an interval. Every other source produces points and the
//     time between them; a meeting carries its own duration, which is the
//     whole reason it closes the hole.
//   - The bytes come off the network, from a server nobody here controls.
//     Everything is bounded: the response, the line length, the recurrence
//     expansion. A limit that is reached is reported, never swallowed.
//   - Nothing is written to disk on the way through. The raw feed holds all
//     the fields this project refuses to store, so caching it would put more
//     on disk than the database is allowed to hold.
package calendar

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/text"
)

// SourceName is what the events table calls this source.
const SourceName = "calendar"

// maxNotes caps how many *kinds* of complaint one calendar may produce.
//
// Kinds, not messages, and that distinction is the whole of it. Each broken
// event names its own line, so the messages are all different and a feed with
// three hundred malformed events produced three hundred warnings. Capping the
// sorted list instead kept the twenty alphabetically first — which is twenty
// copies of one complaint, and silently dropped the unknown-time-zone note
// telling somebody their meetings were hours out. A cap that hides the note
// that mattered is worse than no cap.
//
// So the line number is stripped to find the kind, one message is kept per
// kind with a count of the rest, and the cap applies to kinds. Twenty distinct
// kinds is not something a real feed reaches.
const maxNotes = 20

// note is one kind of complaint: the first message of its kind, and how many
// there were.
type note struct {
	text string
	n    int
}

// noteKind collapses "line 5: X" and "line 99: X" into one complaint.
func noteKind(msg string) string {
	if !strings.HasPrefix(msg, "line ") {
		return msg
	}
	if i := strings.Index(msg, ": "); i > 0 {
		return msg[i+2:]
	}
	return msg
}

// maxMeeting is the longest a timed event may be and still be treated as a
// meeting. Anything longer is a label on a stretch of days rather than an hour
// somebody sat through, which is the same reason whole-day entries are skipped.
const maxMeeting = 24 * time.Hour

// maxUID bounds an event's own identifier. Real ones are well under a hundred
// characters and the format sets no limit at all, so one arrives from the
// network unbounded and lands in a column the README tells people to read.
//
// An over-long UID is refused rather than cut short. Cutting it would let two
// events sharing a prefix collide on the unique index and drop each other with
// no error — which is the precise failure the calendar id in the key exists to
// prevent, reintroduced one field along.
const maxUID = 512

// tsLayout matches the other sources: UTC, milliseconds, always Z, so that
// string order is time order.
const tsLayout = "2006-01-02T15:04:05.000Z"

// Meeting is what survives: when it started, when it ended, what it was
// called, and enough identity to recognise it again on the next run.
type Meeting struct {
	// UID is the event's own identifier. It is unique within a calendar and
	// emphatically not across calendars: an invitation keeps the organiser's
	// UID in every attendee's copy, so the same meeting carries the same UID
	// in your calendar and in a shared one you also subscribe to.
	UID string
	// Recurrence tells one occurrence of a series from another. Empty for an
	// event that happens once.
	Recurrence string
	Start, End time.Time
	Summary    string
	// Recurring records that this occurrence was generated from a rule rather
	// than written out in the file.
	Recurring bool
}

// Counts is what one calendar's worth of parsing did, including everything it
// deliberately threw away. Every number here is printed: a source that
// silently drops two thirds of a file is indistinguishable from a source that
// works.
type Counts struct {
	Events    int // VEVENTs in the file
	Meetings  int // occurrences that survived, after expansion
	Cancelled int
	AllDay    int
	Free      int
	Declined  int
	Instant   int // zero length: a reminder, not an interval
	Overlong  int // longer than a day: not a meeting, whatever it is
	// Unexpanded is a repeating event whose rule this parser will not
	// half-apply. The event is not unreadable — it is a rule that would have
	// to be guessed at — and a series that quietly contributes nothing is the
	// one shape of loss this source exists to stop, so it gets a counter
	// rather than only a warning.
	Unexpanded int
	// Unreadable is a VEVENT this parser could not use: a time or duration it
	// could not read, a rule that would remove or move occurrences rather
	// than produce them (EXRULE, a second RRULE, RANGE=THISANDFUTURE), a
	// missing or absurd UID, an end before its start. Each costs that event
	// and nothing else — and none of them is Unexpanded above, which is a
	// readable event whose repetition rule is simply not implemented.
	Unreadable int
	// Truncated is a series this parser stopped expanding because it hit a
	// cap. Like Unreadable and Unexpanded it is spoor failing to understand
	// the feed rather than the feed saying the meetings are gone, and the one
	// operation in the program that deletes rows has to be able to tell the
	// two apart.
	Truncated int
	Notes     []string
}

// add sums one calendar's counts into a running total.
//
// It lives here, next to the fields, because the last two reviews each caught a
// counter that was collected and then never added up — a refusal that happened,
// was correct, and printed as zero. Adding a field to Counts and forgetting it
// here is the same failure again, so TestCountsAddCoversEveryField fails when
// a field is missing rather than waiting for somebody to notice a zero.
func (c *Counts) add(o Counts) {
	c.Events += o.Events
	c.Meetings += o.Meetings
	c.Cancelled += o.Cancelled
	c.AllDay += o.AllDay
	c.Free += o.Free
	c.Declined += o.Declined
	c.Instant += o.Instant
	c.Overlong += o.Overlong
	c.Unexpanded += o.Unexpanded
	c.Truncated += o.Truncated
	c.Unreadable += o.Unreadable
	c.Notes = append(c.Notes, o.Notes...)
}

// Options is what the caller knows and the file does not.
type Options struct {
	// Me is the addresses that are the user. Case does not matter: an
	// address is compared case-insensitively, because somebody writing their
	// own address in a config file capitalises it about half the time and a
	// setting that silently never fires is the worst thing this file can do.
	//
	// Without it a
	// declined meeting cannot be told from an accepted one — the file says
	// which attendee declined, not which attendee is reading it — and nothing
	// is skipped on that ground.
	Me []string
	// From and To bound the occurrences produced. A feed holds years; a run
	// only ever asks about a window of it.
	From, To time.Time
}

// Parse reads a feed and returns the meetings inside the window, in start
// order.
//
// Order is not cosmetic. The report merges attention intervals by comparing
// each against the last one only, so intervals must arrive sorted by start or
// the merge silently loses the front of one.
func Parse(data []byte, opts Options) ([]Meeting, Counts, error) {
	var counts Counts
	events, err := parseICS(bytes.NewReader(data))
	if err != nil {
		return nil, counts, err
	}
	counts.Events = len(events)

	// A detached instance — "the standup on the 14th moved to 15:00" — is a
	// VEVENT of its own carrying RECURRENCE-ID. It replaces the occurrence the
	// rule would have generated, so that one must not also be produced.
	overridden := map[string]map[string]bool{}
	for _, v := range events {
		// Broken ones are skipped: an override that could not be read must
		// not silently cancel the occurrence it was meant to replace, which
		// would take an hour out of a day and blame it on nothing.
		if v.broken == nil && v.hasRecur && v.uid != "" {
			if overridden[v.uid] == nil {
				overridden[v.uid] = map[string]bool{}
			}
			overridden[v.uid][recurrenceKey(v.recurID)] = true
		}
	}

	var (
		out   []Meeting
		notes = map[string]*note{}
	)
	for _, v := range events {
		ms, err := meetingsOf(v, opts, overridden, &counts, notes)
		if err != nil {
			counts.Unreadable++
			addNote(notes, err.Error())
			continue
		}
		out = append(out, ms...)
	}

	for _, n := range notes {
		text := n.text
		if n.n > 1 {
			text = fmt.Sprintf("%s (%d events like this)", text, n.n)
		}
		counts.Notes = append(counts.Notes, text)
	}
	sort.Strings(counts.Notes)
	// The note that says notes were dropped belongs at the end of the list it
	// is about, not filed under "m".
	for i, n := range counts.Notes {
		if n == dropped {
			counts.Notes = append(append(counts.Notes[:i:i], counts.Notes[i+1:]...), dropped)
			break
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		if out[i].UID != out[j].UID {
			return out[i].UID < out[j].UID
		}
		return out[i].Recurrence < out[j].Recurrence
	})
	counts.Meetings = len(out)
	return out, counts, nil
}

// addNote records one complaint under its kind, keeping the first message and
// counting the rest. Past maxNotes kinds it stops adding kinds, so the bound
// is on how many different things can be wrong rather than on how loud any one
// of them is.
func addNote(notes map[string]*note, msg string) {
	k := noteKind(msg)
	if n, ok := notes[k]; ok {
		n.n++
		return
	}
	if len(notes) >= maxNotes {
		// The cap itself must not become a silent drop. Counted under a key
		// that cannot collide with a real message.
		if n, ok := notes[dropped]; ok {
			n.n++
		} else {
			// n stays 1: the count on a note means "events like this", and
			// this one is about kinds, so it must not grow a suffix that
			// counts the wrong thing.
			notes[dropped] = &note{text: dropped, n: 1}
		}
		return
	}
	notes[k] = &note{text: msg, n: 1}
}

// dropped is the note that says notes were dropped.
const dropped = "more kinds of problem than can be listed; fix these and run again to see the rest"

// meetingsOf turns one VEVENT into the occurrences of it that count.
func meetingsOf(v vevent, opts Options, overridden map[string]map[string]bool, counts *Counts, notes map[string]*note) ([]Meeting, error) {
	if v.badDates > 0 {
		addNote(notes, "some EXDATE or RDATE values could not be read; those occurrences are wrong")
	}
	if v.zone != "" {
		addNote(notes, fmt.Sprintf("time zone %q is not in the time zone database; those events were read as local time", clip(v.zone)))
	}
	if v.broken != nil {
		return nil, v.broken
	}
	if v.start.IsZero() {
		return nil, fmt.Errorf("an event has no start time and was skipped")
	}
	// The UID is the identity. Without one, two events would produce the same
	// external id and the second would be dropped as a duplicate; over-long,
	// see maxUID.
	if v.uid == "" {
		return nil, fmt.Errorf("an event has no UID and was skipped: there would be nothing to tell it from another")
	}
	if len(v.uid) > maxUID {
		return nil, fmt.Errorf("an event's UID is longer than %d characters and was skipped", maxUID)
	}
	// The refusals, in the order that makes the counts add up: a cancelled
	// all-day event is counted once, as cancelled.
	switch {
	case v.status == "CANCELLED":
		counts.Cancelled++
		return nil, nil
	case v.allDay:
		// A whole-day entry is a label on the day — a holiday, a birthday, an
		// "on leave" — not an hour anybody sat through. Importing it as a
		// twenty-four hour interval would claim the entire day as attention
		// and make the coverage number meaningless.
		counts.AllDay++
		return nil, nil
	case v.transparent:
		// The calendar's own word for "this does not make me busy".
		counts.Free++
		return nil, nil
	case declinedByMe(v.declinedBy, opts.Me):
		counts.Declined++
		return nil, nil
	}

	length := v.end.Sub(v.start)
	if length < 0 {
		// Not a reminder. An end before its start is a malformed event, and
		// filing it under "with no length" would describe it wrongly in the
		// one line anybody reads about it.
		return nil, fmt.Errorf("an event ends before it starts and was skipped")
	}
	if length > maxMeeting {
		// Bounded at the top as well as the bottom. A DTEND days or years
		// after DTSTART — a malformed feed, a DURATION a server got wrong —
		// otherwise makes one event claim every remaining hour of its day as
		// attention. The report's midnight clamp keeps that to one day, which
		// is still a day of somebody's time invented from one bad line, and
		// nothing would have said so.
		counts.Overlong++
		return nil, nil
	}
	if length <= 0 {
		// A reminder, not an interval. Nothing in the report can use it: it
		// contributes no time and it is not a moment of attention either.
		counts.Instant++
		return nil, nil
	}

	summary := text.Printable(strings.TrimSpace(v.summary))

	// An event with no rule happens once, and a detached instance is that
	// too — it carries the time it was moved to.
	if v.rrule == "" {
		if !v.start.Before(opts.To) || !v.end.After(opts.From) {
			return nil, nil
		}
		m := Meeting{UID: v.uid, Start: v.start, End: v.end, Summary: summary}
		if v.hasRecur {
			m.Recurrence = recurrenceKey(v.recurID)
			m.Recurring = true
		}
		return []Meeting{m}, nil
	}

	rule, err := parseRRule(v.rrule, v.start.Location())
	if err != nil {
		// Not expanded, and said so. A rule this parser does not understand
		// is expanded wrongly or not at all, and "not at all, loudly" is the
		// only one of those that cannot invent a meeting.
		addNote(notes, fmt.Sprintf("recurrence rule not expanded (%v); one series is missing from the totals", err))
		counts.Unexpanded++
		return nil, nil
	}

	// The window is widened by the length of the meeting so that an occurrence
	// beginning before it and running into it is inside the day being asked
	// about — and by one instant less than the length, so that an occurrence
	// ending exactly at From is excluded, as it is for an event with no rule.
	starts, truncated := rule.expand(v.start, opts.From.Add(-length).Add(time.Nanosecond), opts.To)
	if truncated {
		counts.Truncated++
		addNote(notes, "a recurrence rule hit an expansion limit; that series is incomplete")
	}
	starts = append(starts, inWindow(v.rdates, opts.From.Add(-length).Add(time.Nanosecond), opts.To)...)

	skip := map[string]bool{}
	for _, ex := range v.exdates {
		skip[recurrenceKey(ex)] = true
	}

	out := make([]Meeting, 0, len(starts))
	seen := map[string]bool{}
	for _, s := range starts {
		key := recurrenceKey(s)
		if skip[key] || overridden[v.uid][key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Meeting{
			UID: v.uid, Recurrence: key,
			Start: s, End: s.Add(length),
			Summary: summary, Recurring: true,
		})
	}
	return out, nil
}

func inWindow(ts []time.Time, from, to time.Time) []time.Time {
	var out []time.Time
	for _, t := range ts {
		if !t.Before(from) && t.Before(to) {
			out = append(out, t)
		}
	}
	return out
}

// declinedByMe answers the one question the attendee list is read for.
//
// The comparison folds case on both sides here rather than trusting the caller
// to have done it. The feed's side was already folded, the config's side was
// not, and "You@example.com" in a config file therefore matched nothing and
// said nothing about it — one capital letter between a working setting and a
// silent no-op.
func declinedByMe(declined, me []string) bool {
	if len(me) == 0 {
		return false
	}
	for _, d := range declined {
		if d == "" {
			continue
		}
		for _, m := range me {
			if strings.EqualFold(d, strings.TrimSpace(m)) {
				return true
			}
		}
	}
	return false
}

// recurrenceKey names one occurrence of a series, stably across runs and
// across time zones.
func recurrenceKey(t time.Time) string {
	return t.UTC().Format(tsLayout)
}

// entrypointOf is how a calendar's id is written into the entrypoint column —
// the column that already meant "which instance of a source wrote this".
//
// There is one of it because two places need the same answer and they must not
// each have their own: ToEvent writes the value, and the window sweep selects
// on it. A sweep looking for the unsanitised id would find nothing, delete
// nothing, and leave the double counting exactly where it was, silently.
func entrypointOf(calendarID string) string {
	return text.Sanitise(calendarID, text.Replacement)
}

// ToEvent turns a meeting into the record the store keeps.
//
// The identity is the calendar, the UID and the occurrence — all three. A UID
// alone would be wrong the moment a second calendar is added: an invitation
// keeps the organiser's UID in every copy of it, so the same meeting arrives
// from two calendars carrying one identifier, and the unique index would drop
// the second silently, importing nothing and reporting success.
//
// It is not, however, enough to make a *moved* meeting arrive: rescheduling
// changes none of the three, so the row has to be updated rather than
// inserted, and a whole series moved changes every occurrence, so the rows it
// left behind have to be deleted. See store.ReplaceWindow.
func ToEvent(m Meeting, calendarID string) event.Event {
	subtype := ""
	if m.Recurring {
		subtype = "recurring"
	}
	ms := m.End.Sub(m.Start).Milliseconds()
	return event.Event{
		Source: SourceName,
		ExternalID: text.Sanitise(
			strings.Join([]string{calendarID, m.UID, m.Recurrence}, "/"),
			text.Replacement),
		TS:         m.Start.UTC().Format(tsLayout),
		DurationMS: &ms,
		Type:       "event",
		Subtype:    subtype,
		Title:      m.Summary,
		Entrypoint: entrypointOf(calendarID),
	}
}
