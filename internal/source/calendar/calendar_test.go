// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package calendar

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every fixture here is synthetic. Nothing in this file came out of a real
// calendar: the repository holds no personal data, and a meeting title is
// exactly the kind that would be.

// wrap puts VEVENT bodies inside a minimal VCALENDAR.
func wrap(bodies ...string) []byte {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n")
	for _, body := range bodies {
		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString(strings.TrimSpace(body))
		b.WriteString("\r\nEND:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return []byte(b.String())
}

func window(from, to string) Options {
	return Options{From: mustTime(from), To: mustTime(to)}
}

func mustTime(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func parse(t *testing.T, data []byte, opts Options) ([]Meeting, Counts) {
	t.Helper()
	ms, counts, err := Parse(data, opts)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return ms, counts
}

func TestOneMeeting(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Weekly sync
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	if got, want := ms[0].Summary, "Weekly sync"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got, want := ms[0].End.Sub(ms[0].Start), time.Hour; got != want {
		t.Errorf("length = %s, want %s", got, want)
	}
	if ms[0].Recurring {
		t.Error("a one-off is marked recurring")
	}
}

// A duration is relative to the start, and the format does not promise it
// comes after it.
func TestDurationBeforeStart(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
DURATION:PT45M
DTSTART:20260901T090000Z
SUMMARY:Standup`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	if got, want := ms[0].End.Sub(ms[0].Start), 45*time.Minute; got != want {
		t.Errorf("length = %s, want %s", got, want)
	}
}

// A folded line is one logical line; the continuation drops exactly one space.
func TestFoldedLine(t *testing.T) {
	data := []byte("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a@example.invalid\r\n" +
		"SUMMARY:A title that was\r\n  folded across lines\r\n" +
		"DTSTART:20260901T090000Z\r\nDTEND:20260901T093000Z\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n")
	ms, _ := parse(t, data, window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	if got, want := ms[0].Summary, "A title that was folded across lines"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestTZIDIsHonoured(t *testing.T) {
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Zoned
DTSTART;TZID=Europe/Moscow:20260901T120000
DTEND;TZID=Europe/Moscow:20260901T130000`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	// Moscow is UTC+3 all year.
	if got, want := ms[0].Start.UTC().Format(time.RFC3339), "2026-09-01T09:00:00Z"; got != want {
		t.Errorf("start = %s, want %s", got, want)
	}
	if len(counts.Notes) != 0 {
		t.Errorf("notes = %v, want none", counts.Notes)
	}
}

// An unknown zone must be reported rather than guessed at silently: reading it
// as UTC would move the meeting by hours and nothing would say so.
func TestUnknownZoneIsReported(t *testing.T) {
	_, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Zoned
DTSTART;TZID=Russian Standard Time:20260901T120000
DTEND;TZID=Russian Standard Time:20260901T130000`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(counts.Notes) == 0 {
		t.Fatal("an unknown time zone produced no note")
	}
	if !strings.Contains(counts.Notes[0], "Russian Standard Time") {
		t.Errorf("note = %q, want it to name the zone", counts.Notes[0])
	}
}

// The four refusals, each counted so that a source dropping most of a file is
// visible rather than looking like a source that works.
func TestRefusals(t *testing.T) {
	opts := window("2026-08-01T00:00:00", "2026-10-01T00:00:00")
	opts.Me = []string{"me@example.invalid"}

	ms, counts := parse(t, wrap(
		`UID:c@example.invalid
SUMMARY:Cancelled
STATUS:CANCELLED
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`,
		`UID:d@example.invalid
SUMMARY:All day
DTSTART;VALUE=DATE:20260901
DTEND;VALUE=DATE:20260902`,
		`UID:f@example.invalid
SUMMARY:Free
TRANSP:TRANSPARENT
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`,
		`UID:x@example.invalid
SUMMARY:Declined by me
ATTENDEE;PARTSTAT=DECLINED:mailto:ME@example.invalid
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`,
		`UID:y@example.invalid
SUMMARY:Declined by somebody else
ATTENDEE;PARTSTAT=DECLINED:mailto:other@example.invalid
ATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.invalid
DTSTART:20260901T110000Z
DTEND:20260901T120000Z`,
		`UID:z@example.invalid
SUMMARY:A reminder
DTSTART:20260901T140000Z
DTEND:20260901T140000Z`,
	), opts)

	if len(ms) != 1 {
		t.Fatalf("kept %d meetings, want 1 (%v)", len(ms), summaries(ms))
	}
	if ms[0].Summary != "Declined by somebody else" {
		t.Errorf("kept %q, want the one somebody else declined", ms[0].Summary)
	}
	want := Counts{Events: 6, Meetings: 1, Cancelled: 1, AllDay: 1, Free: 1, Declined: 1, Instant: 1}
	counts.Notes = nil
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}
}

// Without an address for "me" the file cannot say which declined attendee is
// the reader, so nothing is skipped on that ground.
func TestDeclinedNeedsAnAddress(t *testing.T) {
	ms, counts := parse(t, wrap(`
UID:x@example.invalid
SUMMARY:Declined
ATTENDEE;PARTSTAT=DECLINED:mailto:me@example.invalid
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 || counts.Declined != 0 {
		t.Errorf("kept %d meetings, %d declined; want 1 and 0", len(ms), counts.Declined)
	}
}

func TestDailyRecurrence(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY`), window("2026-09-01T00:00:00", "2026-09-08T00:00:00"))

	if len(ms) != 7 {
		t.Fatalf("got %d occurrences, want 7 (%v)", len(ms), starts(ms))
	}
	for i, m := range ms {
		want := mustTime("2026-09-01T09:00:00").AddDate(0, 0, i)
		if !m.Start.Equal(want) {
			t.Errorf("occurrence %d starts %s, want %s", i, m.Start.UTC(), want)
		}
		if !m.Recurring {
			t.Errorf("occurrence %d is not marked recurring", i)
		}
	}
}

func TestWeeklyByDay(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Twice a week
DTSTART:20260901T090000Z
DTEND:20260901T093000Z
RRULE:FREQ=WEEKLY;BYDAY=TU,TH`), window("2026-09-01T00:00:00", "2026-09-15T00:00:00"))

	// 2026-09-01 is a Tuesday.
	want := []string{
		"2026-09-01T09:00:00Z", "2026-09-03T09:00:00Z",
		"2026-09-08T09:00:00Z", "2026-09-10T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// COUNT is counted from the first occurrence of the series, not from the edge
// of whatever window was asked about. Otherwise the same calendar imports
// differently depending on the day it is imported on.
func TestCountRunsFromTheSeriesStart(t *testing.T) {
	body := `
UID:a@example.invalid
SUMMARY:Five days only
DTSTART:20260901T090000Z
DTEND:20260901T093000Z
RRULE:FREQ=DAILY;COUNT=5`

	all, _ := parse(t, wrap(body), window("2026-09-01T00:00:00", "2026-10-01T00:00:00"))
	if len(all) != 5 {
		t.Fatalf("whole series has %d occurrences, want 5", len(all))
	}
	// A window that starts on the third day must show the last three, not
	// five more.
	late, _ := parse(t, wrap(body), window("2026-09-03T00:00:00", "2026-10-01T00:00:00"))
	if got := starts(late); !reflect.DeepEqual(got, []string{
		"2026-09-03T09:00:00Z", "2026-09-04T09:00:00Z", "2026-09-05T09:00:00Z",
	}) {
		t.Errorf("late window = %v", got)
	}
}

func TestUntilAndExdate(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY;UNTIL=20260905T090000Z
EXDATE:20260903T090000Z`), window("2026-09-01T00:00:00", "2026-10-01T00:00:00"))

	want := []string{
		"2026-09-01T09:00:00Z", "2026-09-02T09:00:00Z",
		"2026-09-04T09:00:00Z", "2026-09-05T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// A moved occurrence is its own VEVENT carrying RECURRENCE-ID. It replaces the
// generated one rather than joining it, or the day holds the meeting twice.
func TestDetachedInstanceReplacesTheGeneratedOne(t *testing.T) {
	ms, _ := parse(t, wrap(
		`UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY;COUNT=3`,
		`UID:a@example.invalid
SUMMARY:Standup, moved
RECURRENCE-ID:20260902T090000Z
DTSTART:20260902T150000Z
DTEND:20260902T151500Z`,
	), window("2026-09-01T00:00:00", "2026-10-01T00:00:00"))

	want := []string{
		"2026-09-01T09:00:00Z", "2026-09-02T15:00:00Z", "2026-09-03T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// A rule this parser does not implement must not be half-expanded. BYSETPOS
// changes which occurrences survive, so ignoring it would put a meeting on a
// day it was never on.
func TestUnsupportedRuleIsRefusedAndReported(t *testing.T) {
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Last working day of the month
DTSTART:20260901T090000Z
DTEND:20260901T093000Z
RRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1`), window("2026-01-01T00:00:00", "2027-01-01T00:00:00"))

	if len(ms) != 0 {
		t.Errorf("expanded %d occurrences of an unsupported rule", len(ms))
	}
	if len(counts.Notes) == 0 || !strings.Contains(counts.Notes[0], "BYSETPOS") {
		t.Errorf("notes = %v, want one naming BYSETPOS", counts.Notes)
	}
}

// The rule that never ends. It is legal, it arrives over the network, and it
// must terminate, stay bounded and say that it was cut short.
func TestUnboundedRuleTerminates(t *testing.T) {
	done := make(chan struct{})
	var (
		ms     []Meeting
		counts Counts
	)
	go func() {
		defer close(done)
		ms, counts, _ = Parse(wrap(`
UID:a@example.invalid
SUMMARY:Every second, forever
DTSTART:20200101T000000Z
DTEND:20200101T000001Z
RRULE:FREQ=SECONDLY`), window("2026-09-01T00:00:00", "2026-09-02T00:00:00"))
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("expanding an unbounded rule did not finish")
	}

	if len(ms) > maxOccurrences {
		t.Errorf("produced %d occurrences, over the cap of %d", len(ms), maxOccurrences)
	}
	if len(counts.Notes) == 0 {
		t.Error("a truncated series produced no note")
	}
}

// The boundary of the whole package: what may reach the database.
func TestNothingPersonalSurvivesParsing(t *testing.T) {
	const secret = "a-secret-conference-link"
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Design review
DESCRIPTION:Join at https://example.invalid/`+secret+`
LOCATION:Room 3, `+secret+`
ORGANIZER;CN=Someone:mailto:someone@example.invalid
ATTENDEE;CN=Someone Else;PARTSTAT=ACCEPTED:mailto:else@example.invalid
X-GOOGLE-CONFERENCE:https://example.invalid/`+secret+`
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	ev := ToEvent(ms[0], "work")
	for name, v := range map[string]string{
		"external_id": ev.ExternalID, "title": ev.Title, "raw_text": ev.RawText,
		"type": ev.Type, "subtype": ev.Subtype, "entrypoint": ev.Entrypoint,
		"project": ev.Project, "host": ev.Host, "path_head": ev.PathHead,
		"cwd": ev.CWD, "git_branch": ev.GitBranch, "session_id": ev.SessionID,
	} {
		if strings.Contains(v, secret) {
			t.Errorf("%s = %q carries what only the description and location said", name, v)
		}
		if strings.Contains(strings.ToLower(v), "example.invalid") && name != "external_id" {
			t.Errorf("%s = %q carries an address", name, v)
		}
	}
}

// The identity has to include the calendar. An invitation keeps the
// organiser's UID in every copy, so two calendars holding the same meeting
// arrive with one UID — and a dedup key of UID alone would drop the second
// silently while reporting success.
func TestIdentityIncludesTheCalendar(t *testing.T) {
	m := Meeting{UID: "shared@example.invalid", Start: mustTime("2026-09-01T09:00:00")}
	a := ToEvent(m, "work")
	b := ToEvent(m, "team")
	if a.ExternalID == b.ExternalID {
		t.Fatalf("both calendars produced %q; the second would be dropped", a.ExternalID)
	}
	if !strings.HasPrefix(a.ExternalID, "work/") || !strings.HasPrefix(b.ExternalID, "team/") {
		t.Errorf("ids = %q and %q, want each to name its calendar", a.ExternalID, b.ExternalID)
	}
}

// Two occurrences of one series must not collide either.
func TestOccurrencesHaveDistinctIdentities(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY;COUNT=3`), window("2026-09-01T00:00:00", "2026-10-01T00:00:00"))

	seen := map[string]bool{}
	for _, m := range ms {
		id := ToEvent(m, "work").ExternalID
		if seen[id] {
			t.Fatalf("two occurrences share the id %q", id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Errorf("got %d distinct ids, want 3", len(seen))
	}
}

// The report merges attention intervals against the last one only, so a feed
// that arrives out of order would silently lose the front of an interval.
func TestMeetingsComeOutSorted(t *testing.T) {
	ms, _ := parse(t, wrap(
		`UID:c@example.invalid
SUMMARY:Third
DTSTART:20260901T150000Z
DTEND:20260901T160000Z`,
		`UID:a@example.invalid
SUMMARY:First
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`,
		`UID:b@example.invalid
SUMMARY:Second
DTSTART:20260901T120000Z
DTEND:20260901T130000Z`,
	), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	for i := 1; i < len(ms); i++ {
		if ms[i].Start.Before(ms[i-1].Start) {
			t.Fatalf("meeting %d starts before its predecessor", i)
		}
	}
}

// A title is chosen by whoever sent the invitation, which makes it the field
// to worry about — the same reason the browser source repairs page titles.
func TestSummaryIsRepaired(t *testing.T) {
	ms, _ := parse(t, wrap("UID:a@example.invalid\r\n"+
		"SUMMARY:before\x00after\u202ereversed\r\n"+
		"DTSTART:20260901T090000Z\r\nDTEND:20260901T100000Z"),
		window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	if strings.ContainsAny(ms[0].Summary, "\x00\u202e") {
		t.Errorf("summary = %q still holds a character that misrepresents it", ms[0].Summary)
	}
}

// A calendar with no VEVENT at all is an empty calendar, not a broken one.
func TestEmptyCalendar(t *testing.T) {
	ms, counts := parse(t, []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"),
		window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
	if len(ms) != 0 || counts.Events != 0 {
		t.Errorf("got %d meetings and %d events, want none", len(ms), counts.Events)
	}
}

// A VALARM inside a VEVENT has its own DURATION. Reading it as the meeting's
// would resize the meeting.
func TestAlarmDurationIsNotTheMeetings(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:With a reminder
DTSTART:20260901T090000Z
DTEND:20260901T100000Z
BEGIN:VALARM
TRIGGER:-PT10M
DURATION:PT5M
ACTION:DISPLAY
END:VALARM`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 1 {
		t.Fatalf("got %d meetings, want 1", len(ms))
	}
	if got, want := ms[0].End.Sub(ms[0].Start), time.Hour; got != want {
		t.Errorf("length = %s, want %s", got, want)
	}
}

// A line with no end is a line with no end. The reader is bounded, and the
// message says which bound was hit — otherwise this test would keep passing
// for the wrong reason the moment another refusal is added above it.
func TestOverlongLineIsRefused(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nSUMMARY:" +
		strings.Repeat("x", maxLineBytes+10) + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	_, _, err := Parse([]byte(data), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
	if err == nil {
		t.Fatal("an overlong line was accepted")
	}
	if !strings.Contains(err.Error(), "longer than") {
		t.Errorf("refused with %q, which is not the length limit", err)
	}
}

// A feed that stops in the middle of an event is a truncated download, not an
// empty calendar. Accepting it would report success while holding whatever
// happened to arrive before the cut.
func TestATruncatedCalendarIsRefused(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a@example.invalid\r\n" +
		"SUMMARY:Cut off here\r\nDTSTART:20260901T090000Z\r\n"
	_, _, err := Parse([]byte(data), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
	if err == nil {
		t.Fatal("a feed that ends inside an event was accepted")
	}
}

// The UID is the identity. Missing or unbounded, it would let two events share
// an external id and drop each other on the unique index with no error — the
// failure the calendar id in the key exists to prevent, one field along.
func TestAnEventWithoutAUsableUIDIsRefused(t *testing.T) {
	for _, c := range []struct{ name, uid string }{
		{"no UID at all", ""},
		{"a UID longer than the bound", "UID:" + strings.Repeat("u", maxUID+1)},
	} {
		body := "SUMMARY:x\nDTSTART:20260901T090000Z\nDTEND:20260901T100000Z"
		if c.uid != "" {
			body = c.uid + "\n" + body
		}
		ms, counts := parse(t, wrap(body), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
		if len(ms) != 0 {
			t.Errorf("%s: kept %d meetings", c.name, len(ms))
		}
		if counts.Unreadable != 1 {
			t.Errorf("%s: unreadable = %d, want 1 — it must be counted, not dropped", c.name, counts.Unreadable)
		}
	}
}

// An ordinal BYDAY means nothing outside a month or a year, and reading it as
// "of the month" would put a weekly meeting on the wrong week.
func TestAnOrdinalBYDAYIsRefusedOffScope(t *testing.T) {
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260105T090000Z
DTEND:20260105T093000Z
RRULE:FREQ=WEEKLY;BYDAY=2MO`), window("2026-01-01T00:00:00", "2026-04-01T00:00:00"))

	if len(ms) != 0 {
		t.Errorf("expanded %d occurrences of a rule that has no meaning", len(ms))
	}
	if len(counts.Notes) == 0 {
		t.Error("nothing was reported about a series that was not expanded")
	}
}

func summaries(ms []Meeting) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Summary
	}
	return out
}

func starts(ms []Meeting) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Start.UTC().Format(time.RFC3339)
	}
	return out
}

// The second Tuesday of every month, which is the ordinal form of BYDAY.
func TestMonthlyByDayOrdinal(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Second Tuesday
DTSTART:20260113T090000Z
DTEND:20260113T100000Z
RRULE:FREQ=MONTHLY;BYDAY=2TU`), window("2026-01-01T00:00:00", "2026-05-01T00:00:00"))

	want := []string{
		"2026-01-13T09:00:00Z", "2026-02-10T09:00:00Z",
		"2026-03-10T09:00:00Z", "2026-04-14T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// The last Friday of the month: the negative ordinal counts from the end.
func TestMonthlyByDayFromTheEnd(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Last Friday
DTSTART:20260130T170000Z
DTEND:20260130T180000Z
RRULE:FREQ=MONTHLY;BYDAY=-1FR`), window("2026-01-01T00:00:00", "2026-04-01T00:00:00"))

	want := []string{"2026-01-30T17:00:00Z", "2026-02-27T17:00:00Z", "2026-03-27T17:00:00Z"}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// A monthly meeting on the 31st simply has no occurrence in a month that has
// no 31st. RFC 5545 §3.3.10 says so, and it is not the same as the 1st of the
// month after.
func TestMonthlyOnTheThirtyFirstSkipsShortMonths(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Month end
DTSTART:20260131T090000Z
DTEND:20260131T100000Z
RRULE:FREQ=MONTHLY`), window("2026-01-01T00:00:00", "2026-06-01T00:00:00"))

	want := []string{"2026-01-31T09:00:00Z", "2026-03-31T09:00:00Z", "2026-05-31T09:00:00Z"}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

func TestYearlyAndInterval(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Every other year
DTSTART:20200601T090000Z
DTEND:20200601T100000Z
RRULE:FREQ=YEARLY;INTERVAL=2`), window("2020-01-01T00:00:00", "2027-01-01T00:00:00"))

	want := []string{
		"2020-06-01T09:00:00Z", "2022-06-01T09:00:00Z",
		"2024-06-01T09:00:00Z", "2026-06-01T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// A daily meeting keeps its wall-clock time across a daylight-saving change.
// Stepping by 24 hours instead would move it an hour for half the year.
func TestDailyKeepsItsClockTimeAcrossDST(t *testing.T) {
	// Europe/Berlin moves to summer time on 2026-03-29.
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Standup
DTSTART;TZID=Europe/Berlin:20260327T090000
DTEND;TZID=Europe/Berlin:20260327T093000
RRULE:FREQ=DAILY;COUNT=4`), window("2026-03-01T00:00:00", "2026-04-05T00:00:00"))

	if len(ms) != 4 {
		t.Fatalf("got %d occurrences, want 4", len(ms))
	}
	for i, m := range ms {
		if got := m.Start.Format("15:04"); got != "09:00" {
			t.Errorf("occurrence %d is at %s local, want 09:00", i, got)
		}
	}
	// And the last one is genuinely an hour earlier in UTC than the first.
	if got, want := ms[3].Start.UTC().Format(time.RFC3339), "2026-03-30T07:00:00Z"; got != want {
		t.Errorf("after the change the meeting is at %s UTC, want %s", got, want)
	}
}

func TestIntervalOnDays(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Every third day
DTSTART:20260901T090000Z
DTEND:20260901T093000Z
RRULE:FREQ=DAILY;INTERVAL=3;COUNT=4`), window("2026-09-01T00:00:00", "2026-10-01T00:00:00"))

	want := []string{
		"2026-09-01T09:00:00Z", "2026-09-04T09:00:00Z",
		"2026-09-07T09:00:00Z", "2026-09-10T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v", got, want)
	}
}

// A meeting that began before the window and runs into it belongs to the day
// being asked about.
func TestOccurrenceStraddlingTheWindowEdge(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Long call
DTSTART:20260901T233000Z
DTEND:20260902T003000Z
RRULE:FREQ=DAILY;COUNT=3`), window("2026-09-02T00:00:00", "2026-09-03T00:00:00"))

	if got := starts(ms); !reflect.DeepEqual(got, []string{"2026-09-01T23:30:00Z", "2026-09-02T23:30:00Z"}) {
		t.Errorf("occurrences = %v", got)
	}
}

// Declining one occurrence of a repeating meeting must remove that occurrence
// and leave the rest of the series alone.
//
// It is the case that looks like it should not work. Google writes the refusal
// as a detached VEVENT — same UID, a RECURRENCE-ID naming the occurrence, and
// PARTSTAT=DECLINED on your own attendee line. Two things have to happen
// together: the generated occurrence has to be suppressed because an override
// exists for it, and the override itself has to be dropped because it was
// declined. Get only the first and the meeting stays; get only the second and
// it appears twice.
//
// Verified against a real feed before it was written down here: one occurrence
// of a sixty-occurrence series disappeared and fifty-nine remained.
func TestDecliningOneOccurrenceLeavesTheRestOfTheSeries(t *testing.T) {
	opts := window("2026-09-01T00:00:00", "2026-09-08T00:00:00")
	opts.Me = []string{"me@example.invalid"}

	feed := wrap(
		`UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY
ATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.invalid`,
		`UID:a@example.invalid
SUMMARY:Standup
RECURRENCE-ID:20260904T090000Z
DTSTART:20260904T090000Z
DTEND:20260904T091500Z
ATTENDEE;PARTSTAT=DECLINED:mailto:me@example.invalid`,
	)

	ms, counts := parse(t, feed, opts)
	want := []string{
		"2026-09-01T09:00:00Z", "2026-09-02T09:00:00Z", "2026-09-03T09:00:00Z",
		"2026-09-05T09:00:00Z", "2026-09-06T09:00:00Z", "2026-09-07T09:00:00Z",
	}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences = %v, want %v — the 4th is the one declined", got, want)
	}
	if counts.Declined != 1 {
		t.Errorf("declined = %d, want 1", counts.Declined)
	}
}

// And without an address for "me" that same feed keeps everything, because the
// file says which attendee declined and not which attendee is the reader.
// This is the config line whose absence silently changes the answer.
func TestDecliningOneOccurrenceNeedsAnAddress(t *testing.T) {
	feed := wrap(
		`UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY
ATTENDEE;PARTSTAT=ACCEPTED:mailto:me@example.invalid`,
		`UID:a@example.invalid
SUMMARY:Standup
RECURRENCE-ID:20260904T090000Z
DTSTART:20260904T090000Z
DTEND:20260904T091500Z
ATTENDEE;PARTSTAT=DECLINED:mailto:me@example.invalid`,
	)
	ms, _ := parse(t, feed, window("2026-09-01T00:00:00", "2026-09-08T00:00:00"))
	if len(ms) != 7 {
		t.Errorf("got %d occurrences, want all 7: nothing may be skipped without an address", len(ms))
	}
	// And the declined instance must not turn into a second meeting on the 4th.
	seen := map[string]bool{}
	for _, m := range ms {
		k := m.Start.UTC().Format(time.RFC3339)
		if seen[k] {
			t.Errorf("two meetings at %s: the override was added instead of replacing", k)
		}
		seen[k] = true
	}
}

// Somebody with two addresses declines under whichever one the invitation
// used. Both have to count, which is why "me" is a list.
func TestAnyOfMyAddressesCounts(t *testing.T) {
	opts := window("2026-09-01T00:00:00", "2026-09-08T00:00:00")
	opts.Me = []string{"me@example.invalid", "alias@example.invalid"}
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Declined under the alias
DTSTART:20260901T090000Z
DTEND:20260901T100000Z
ATTENDEE;PARTSTAT=DECLINED:mailto:Alias@Example.Invalid`), opts)

	if len(ms) != 0 || counts.Declined != 1 {
		t.Errorf("kept %d meetings, declined %d; want 0 and 1", len(ms), counts.Declined)
	}
}

// An address written with capitals is the same address. People capitalise
// their own address in a config file about half the time, and a setting that
// silently never fires is worse than one that errors.
func TestMyAddressIsMatchedWhateverTheCase(t *testing.T) {
	for _, mine := range []string{
		"me@example.invalid", "Me@Example.Invalid", "ME@EXAMPLE.INVALID", "  me@example.invalid  ",
	} {
		opts := window("2026-09-01T00:00:00", "2026-09-08T00:00:00")
		opts.Me = []string{mine}
		ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Declined
DTSTART:20260901T090000Z
DTEND:20260901T100000Z
ATTENDEE;PARTSTAT=DECLINED:mailto:me@example.invalid`), opts)

		if len(ms) != 0 || counts.Declined != 1 {
			t.Errorf("me=%q kept %d meetings and counted %d declined; want 0 and 1",
				mine, len(ms), counts.Declined)
		}
	}
}

// Six ways the expansion was wrong, each found by review and each reproduced
// before it was fixed. Every one of them produced a plausible number rather
// than an error, and every existing test happened to start its series on a day
// where the bug did not show.
func TestExpansionCasesFoundByReview(t *testing.T) {
	for _, c := range []struct {
		name, body, from, to string
		want                 []string
	}{
		{
			// A period reaches back before DTSTART — the week containing it —
			// and a series does not exist before it starts.
			name: "weekly BYDAY starting mid-week invents nothing earlier",
			body: "DTSTART:20260107T090000Z\nDTEND:20260107T093000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR",
			from: "2026-01-01T00:00:00", to: "2026-01-15T00:00:00",
			want: []string{"2026-01-07T09:00:00Z", "2026-01-09T09:00:00Z",
				"2026-01-12T09:00:00Z", "2026-01-14T09:00:00Z"},
		},
		{
			// And an invented occurrence must not eat one of the real ones.
			name: "an occurrence before DTSTART does not consume COUNT",
			body: "DTSTART:20260104T090000Z\nDTEND:20260104T093000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO;COUNT=3",
			from: "2025-12-01T00:00:00", to: "2026-03-01T00:00:00",
			want: []string{"2026-01-05T09:00:00Z", "2026-01-12T09:00:00Z", "2026-01-19T09:00:00Z"},
		},
		{
			// The walk stopped on the period's base, and a weekly occurrence
			// can sit six days before it. The series ended early, silently.
			name: "a weekly occurrence at the far edge of the window is not lost",
			body: "DTSTART:20260104T090000Z\nDTEND:20260104T093000Z\nRRULE:FREQ=WEEKLY;BYDAY=MO",
			from: "2026-01-01T00:00:00", to: "2026-01-06T00:00:00",
			want: []string{"2026-01-05T09:00:00Z"},
		},
		{
			// "Every January" meant every day of every January.
			name: "BYMONTH alone takes the day from DTSTART",
			body: "DTSTART:20260115T090000Z\nDTEND:20260115T093000Z\nRRULE:FREQ=MONTHLY;BYMONTH=1,7",
			from: "2026-01-01T00:00:00", to: "2027-01-01T00:00:00",
			want: []string{"2026-01-15T09:00:00Z", "2026-07-15T09:00:00Z"},
		},
		{
			// The ordinal was counted per month, so "the first Monday of the
			// year" was the first Monday of all twelve.
			name: "a yearly ordinal BYDAY is counted over the year",
			body: "DTSTART:20260105T090000Z\nDTEND:20260105T093000Z\nRRULE:FREQ=YEARLY;BYDAY=1MO",
			from: "2026-01-01T00:00:00", to: "2027-01-01T00:00:00",
			want: []string{"2026-01-05T09:00:00Z"},
		},
		{
			// Skipping ahead counted months that hold no occurrence against
			// COUNT, so the answer depended on where the window began.
			name: "COUNT survives months with no 31st however late the window starts",
			body: "DTSTART:20260131T090000Z\nDTEND:20260131T093000Z\nRRULE:FREQ=MONTHLY;COUNT=6",
			from: "2026-07-01T00:00:00", to: "2027-01-01T00:00:00",
			want: []string{"2026-07-31T09:00:00Z", "2026-08-31T09:00:00Z", "2026-10-31T09:00:00Z"},
		},
		{
			// AddDate normalises 29 February to 1 March. RFC 5545 says a date
			// that does not exist has no occurrence.
			name: "a yearly rule on 29 February skips non-leap years",
			body: "DTSTART:20240229T090000Z\nDTEND:20240229T093000Z\nRRULE:FREQ=YEARLY",
			from: "2024-01-01T00:00:00", to: "2029-01-01T00:00:00",
			want: []string{"2024-02-29T09:00:00Z", "2028-02-29T09:00:00Z"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			ms, _ := parse(t, wrap("UID:a@example.invalid\nSUMMARY:x\n"+c.body),
				window(c.from, c.to))
			if got := starts(ms); !reflect.DeepEqual(got, c.want) {
				t.Errorf("occurrences =\n  %v\nwant\n  %v", got, c.want)
			}
		})
	}
}

// The second review pass, in the same shape as the first: each of these
// returned a plausible number rather than an error.
func TestSecondPassCases(t *testing.T) {
	w := window("2026-01-01T00:00:00", "2026-03-01T00:00:00")

	// BYMONTHDAY is a limit for DAILY and below, and it was applied only in
	// the monthly branch — so "the 15th of each month" was every day of it.
	// This is the BYMONTH bug of the first pass, one BY-rule along.
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260101T090000Z
DTEND:20260101T093000Z
RRULE:FREQ=DAILY;BYMONTHDAY=15`), w)
	if got := starts(ms); !reflect.DeepEqual(got, []string{
		"2026-01-15T09:00:00Z", "2026-02-15T09:00:00Z"}) {
		t.Errorf("DAILY;BYMONTHDAY=15 gave %v", got)
	}

	// And RFC 5545 forbids it with WEEKLY, so that is refused rather than
	// filtered.
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260101T090000Z
DTEND:20260101T093000Z
RRULE:FREQ=WEEKLY;BYDAY=TH;BYMONTHDAY=15`), w)
	if len(ms) != 0 || len(counts.Notes) == 0 {
		t.Errorf("WEEKLY;BYMONTHDAY expanded %d occurrences, notes %v", len(ms), counts.Notes)
	}
}

// A DURATION was parked as an offset from the zero time and recognised by
// "is the year 1 or earlier". P365D lands in year 2, so from there upwards the
// length came out negative and the meeting was filed as having no length —
// under a counter that says "with no length", which is not what happened.
func TestALongDurationIsNotMistakenForNoLength(t *testing.T) {
	for _, c := range []struct {
		dur  string
		want time.Duration
	}{
		{"PT30M", 30 * time.Minute},
		{"P1DT2H", 26 * time.Hour},
		{"P364D", 364 * 24 * time.Hour},
		{"P400D", 400 * 24 * time.Hour},
	} {
		evs, err := parseICS(strings.NewReader(string(wrap(
			"UID:a@example.invalid\nSUMMARY:x\nDTSTART:20260101T090000Z\nDURATION:" + c.dur))))
		if err != nil {
			t.Fatalf("%s: %v", c.dur, err)
		}
		if got := evs[0].end.Sub(evs[0].start); got != c.want {
			t.Errorf("DURATION:%s gave a length of %s, want %s", c.dur, got, c.want)
		}
	}
}

// A timed event longer than a day is not a meeting, and left uncapped one bad
// DTEND claims every remaining hour of its day as attention.
func TestAnOverlongMeetingIsRefusedAndCounted(t *testing.T) {
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260101T090000Z
DTEND:20260201T090000Z`), window("2026-01-01T00:00:00", "2026-03-01T00:00:00"))
	if len(ms) != 0 || counts.Overlong != 1 {
		t.Errorf("kept %d meetings, overlong = %d; want 0 and 1", len(ms), counts.Overlong)
	}
}

// A feed has to be a whole feed. Cut between events it used to lose every
// meeting after the cut and report success; a login page served with 200 was
// indistinguishable from an empty calendar.
func TestAFeedMustBeWholeAndBeACalendar(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"cut after an event, no END:VCALENDAR",
			"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a@example.invalid\r\nSUMMARY:x\r\n" +
				"DTSTART:20260901T090000Z\r\nDTEND:20260901T100000Z\r\nEND:VEVENT\r\n"},
		{"cut inside a component after an event",
			"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a@example.invalid\r\nSUMMARY:x\r\n" +
				"DTSTART:20260901T090000Z\r\nDTEND:20260901T100000Z\r\nEND:VEVENT\r\n" +
				"BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\n"},
		{"a login page served with 200", "<html><body>Sign in</body></html>"},
		{"an event begun inside another",
			"BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a@example.invalid\r\n" +
				"BEGIN:VEVENT\r\nUID:b@example.invalid\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"},
	} {
		if _, _, err := Parse([]byte(c.body), window("2026-08-01T00:00:00", "2026-10-01T00:00:00")); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
}

// Rules that take occurrences away, or move more than one, are refused rather
// than half-applied — and the refusal costs that event, not the feed.
//
// Getting this wrong is worse than not refusing at all: one unsupported
// property in one invitation would take a day of meetings with it.
func TestRulesThatWouldBeHalfAppliedCostOnlyTheirOwnEvent(t *testing.T) {
	for _, c := range []struct{ name, prop string }{
		{"EXRULE removes occurrences", "EXRULE:FREQ=WEEKLY;BYDAY=MO"},
		{"a second RRULE", "RRULE:FREQ=DAILY\nRRULE:FREQ=WEEKLY"},
		{"RANGE=THISANDFUTURE moves every later occurrence",
			"RECURRENCE-ID;RANGE=THISANDFUTURE:20260902T090000Z"},
		{"a date-time that is not one", "DTSTART:not-a-time"},
	} {
		ms, counts := parse(t, wrap(
			"UID:bad@example.invalid\nSUMMARY:x\nDTSTART:20260901T090000Z\n"+
				"DTEND:20260901T093000Z\n"+c.prop,
			`UID:good@example.invalid
SUMMARY:The meeting either side of it
DTSTART:20260903T090000Z
DTEND:20260903T100000Z`,
		), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

		if counts.Unreadable != 1 {
			t.Errorf("%s: unreadable = %d, want 1", c.name, counts.Unreadable)
		}
		if len(counts.Notes) == 0 {
			t.Errorf("%s: refused with no note", c.name)
		}
		if len(ms) != 1 || ms[0].UID != "good@example.invalid" {
			t.Errorf("%s: the rest of the feed was lost (%d meetings)", c.name, len(ms))
		}
	}
}

// Counts.add has to cover every field it has.
//
// Twice now a counter has been collected correctly and then never summed, so
// a refusal that really happened printed as zero — which is indistinguishable
// from a refusal that never fired, and worse than not counting at all. This
// walks the struct rather than listing the fields, so adding one and
// forgetting `add` fails here instead of in somebody's report.
func TestCountsAddCoversEveryField(t *testing.T) {
	var one Counts
	v := reflect.ValueOf(&one).Elem()
	for i := 0; i < v.NumField(); i++ {
		switch v.Field(i).Kind() {
		case reflect.Int:
			v.Field(i).SetInt(1)
		case reflect.Slice:
			v.Field(i).Set(reflect.ValueOf([]string{"a note"}))
		default:
			// Not "skip it": a counter typed int64 or a duration would then be
			// invisible to this guard, which is exactly the hole it exists to
			// close. Teach it the kind, or do not add the field.
			t.Fatalf("%s is a %s, which this test does not know how to check",
				v.Type().Field(i).Name, v.Field(i).Kind())
		}
	}

	var total Counts
	total.add(one)
	total.add(one)

	tv := reflect.ValueOf(total)
	for i := 0; i < tv.NumField(); i++ {
		name := tv.Type().Field(i).Name
		switch tv.Field(i).Kind() {
		case reflect.Int:
			if got := tv.Field(i).Int(); got != 2 {
				t.Errorf("%s = %d after adding 1 twice: add does not cover it", name, got)
			}
		case reflect.Slice:
			if got := tv.Field(i).Len(); got != 2 {
				t.Errorf("%s has %d entries after adding 1 twice: add does not cover it", name, got)
			}
		}
	}
}

// The third pass, and the third frequency to lose the same argument: a rule
// whose BY parts say which month but not which day takes the day from DTSTART.
// It was BYMONTH under MONTHLY in pass one, BYMONTHDAY under DAILY in pass two,
// and BYMONTH under WEEKLY here.
func TestWeeklyWithAMonthButNoDayKeepsItsWeekday(t *testing.T) {
	ms, _ := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260907T100000Z
DTEND:20260907T103000Z
RRULE:FREQ=WEEKLY;BYMONTH=9;COUNT=4`), window("2026-09-01T00:00:00", "2026-11-01T00:00:00"))

	want := []string{"2026-09-07T10:00:00Z", "2026-09-14T10:00:00Z",
		"2026-09-21T10:00:00Z", "2026-09-28T10:00:00Z"}
	if got := starts(ms); !reflect.DeepEqual(got, want) {
		t.Errorf("occurrences =\n  %v\nwant\n  %v", got, want)
	}
}

// A byte order mark is what a Windows editor leaves on a saved .ics, and the
// file: input exists for exactly those files. Left in place it makes the first
// property unrecognisable and the envelope check then refuses a good calendar.
func TestALeadingByteOrderMarkIsIgnored(t *testing.T) {
	ms, _ := parse(t, []byte("\ufeff"+string(wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`))), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
	if len(ms) != 1 {
		t.Errorf("got %d meetings, want 1: the BOM was not skipped", len(ms))
	}
}

// The reporting channel is bounded like everything else here. Each broken
// event names its own line, so the deduplicating map cannot collapse them and
// a feed full of them printed one warning each.
func TestNotesAreBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:u%d@e.invalid\r\nDTSTART:%s\r\nEND:VEVENT\r\n",
			i, strings.Repeat("z", 400))
	}
	b.WriteString("END:VCALENDAR\r\n")

	_, counts, err := Parse([]byte(b.String()), window("2026-01-01T00:00:00", "2027-01-01T00:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(counts.Notes) > maxNotes+1 {
		t.Errorf("got %d notes, want at most %d plus a summary", len(counts.Notes), maxNotes)
	}
	if counts.Unreadable != 200 {
		t.Errorf("unreadable = %d, want 200: the count must survive the cap on the notes", counts.Unreadable)
	}
	// And no single note carries an unbounded slice of the feed back to a
	// terminal.
	for _, n := range counts.Notes {
		if len(n) > 400 {
			t.Errorf("a note is %d bytes long", len(n))
		}
	}
}

// An override that could not be read must not cancel the occurrence it was
// meant to replace: that takes an hour out of a day and blames it on nothing.
func TestABrokenOverrideDoesNotCancelItsOccurrence(t *testing.T) {
	ms, counts := parse(t, wrap(
		`UID:a@example.invalid
SUMMARY:Standup
DTSTART:20260901T090000Z
DTEND:20260901T091500Z
RRULE:FREQ=DAILY;COUNT=3`,
		`UID:a@example.invalid
SUMMARY:Standup, moved
RECURRENCE-ID:20260902T090000Z
DTSTART:nonsense
DTEND:20260902T151500Z`,
	), window("2026-09-01T00:00:00", "2026-10-01T00:00:00"))

	if len(ms) != 3 {
		t.Errorf("got %d occurrences, want all 3: %v", len(ms), starts(ms))
	}
	if counts.Unreadable != 1 {
		t.Errorf("unreadable = %d, want 1", counts.Unreadable)
	}
}

// An end before its start is a malformed event, not a reminder, and must not
// be described as "with no length" in the one line anybody reads about it.
func TestANegativeLengthIsNotAReminder(t *testing.T) {
	_, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:x
DTSTART:20260901T100000Z
DTEND:20260901T090000Z`), window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))

	if counts.Instant != 0 || counts.Unreadable != 1 {
		t.Errorf("instant = %d, unreadable = %d; want 0 and 1", counts.Instant, counts.Unreadable)
	}
}

// A rule that cannot be expanded loses a whole series, so it needs a number
// and not only a warning. It was counted nowhere: the event was not
// "unreadable" — the rule was fine, this parser just will not guess at it —
// and the series simply contributed nothing.
func TestAnUnexpandedSeriesIsCounted(t *testing.T) {
	ms, counts := parse(t, wrap(`
UID:a@example.invalid
SUMMARY:Last working day of the month
DTSTART:20260901T090000Z
DTEND:20260901T093000Z
RRULE:FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1`), window("2026-01-01T00:00:00", "2027-01-01T00:00:00"))

	if len(ms) != 0 {
		t.Errorf("expanded %d occurrences of a rule this parser refuses", len(ms))
	}
	if counts.Unexpanded != 1 {
		t.Errorf("unexpanded = %d, want 1", counts.Unexpanded)
	}
	if counts.Unreadable != 0 {
		t.Errorf("unreadable = %d: the event was readable, the rule was refused", counts.Unreadable)
	}
}

// The cap on notes must bound how many *kinds* of complaint there are, not
// keep the alphabetically first twenty. Sorting and truncating kept twenty
// copies of one complaint and dropped the unknown-time-zone note telling
// somebody their meetings were hours out.
func TestTheLoudestComplaintDoesNotCrowdOutTheRest(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:u%d@e.invalid\r\nDTSTART:20260901T090000Z\r\n"+
			"DTEND:20260901T093000Z\r\nEXRULE:FREQ=DAILY\r\nEND:VEVENT\r\n", i)
	}
	b.WriteString("BEGIN:VEVENT\r\nUID:z@e.invalid\r\nSUMMARY:x\r\n" +
		"DTSTART;TZID=Mars/Olympus:20260901T090000\r\n" +
		"DTEND;TZID=Mars/Olympus:20260901T100000\r\nEND:VEVENT\r\n")
	b.WriteString("END:VCALENDAR\r\n")

	_, counts, err := Parse([]byte(b.String()), window("2026-01-01T00:00:00", "2027-01-01T00:00:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(counts.Notes) > maxNotes {
		t.Errorf("got %d notes, want at most %d", len(counts.Notes), maxNotes)
	}
	var zone bool
	for _, n := range counts.Notes {
		if strings.Contains(n, "Mars/Olympus") {
			zone = true
		}
	}
	if !zone {
		t.Errorf("the time zone note was crowded out by three hundred of one kind:\n%v", counts.Notes)
	}
	// And the loud one is reported once, with a count rather than three
	// hundred lines.
	if counts.Unreadable != 300 {
		t.Errorf("unreadable = %d, want 300", counts.Unreadable)
	}
}
