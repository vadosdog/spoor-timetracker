// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package calendar

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	// The IANA time zone database, compiled into the binary.
	//
	// A calendar writes "TZID=Europe/Moscow" and expects the reader to know
	// what that means. time.LoadLocation asks the operating system, which on a
	// cross-compiled binary — Windows, a scratch container, a machine without
	// tzdata installed — has nothing to answer with. The failure is not a
	// crash: every meeting silently lands hours away from when it happened,
	// which is the worst thing a source of time can do. Half a megabyte of
	// binary is the right price for that not being possible.
	_ "time/tzdata"
)

// maxLineBytes caps one unfolded content line.
//
// The file arrives over the network from a server nobody here controls, and a
// single line of it has no length limit in the format. Everything real is
// well under this; a line longer than this is not a calendar entry, it is
// something else arriving in the shape of one.
const maxLineBytes = 1 << 20

// property is one content line: a name, its parameters, and its value.
type property struct {
	name   string
	params map[string][]string
	value  string
}

// param returns the first value of a parameter, upper-cased names only.
func (p property) param(name string) string {
	if v := p.params[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// vevent is one VEVENT exactly as the file wrote it, before recurrence is
// expanded and before anything is filtered.
//
// The fields that are not here are the point: no ATTENDEE addresses beyond
// the yes-or-no this file needs to answer, no DESCRIPTION, no LOCATION, no
// ORGANIZER, no conference link. Those are other people's personal data and
// the project stores metadata only. They are read where a decision needs them
// and never leave this struct.
type vevent struct {
	uid     string
	summary string

	start, end time.Time
	// duration is DTEND expressed the other way. Kept in its own field with
	// its own flag rather than parked in `end` as an offset from the zero
	// time: that trick read "is the year 1 or earlier" as "this came from a
	// DURATION", and P365D lands in year 2. From there upwards the length came
	// out hugely negative and the meeting was counted as having no length at
	// all — under a counter that says "with no length", which is not what
	// happened.
	duration    time.Duration
	hasDuration bool
	allDay      bool
	// zone names a TZID the time zone database did not recognise. The times
	// above were then read as local, which is a guess, so the caller reports
	// it rather than letting a silent hours-long shift through.
	zone string

	status      string
	transparent bool

	rrule    string
	exdates  []time.Time
	rdates   []time.Time
	recurID  time.Time
	hasRecur bool

	// badDates counts EXDATE or RDATE values this parser could not read. Each
	// one is an occurrence added or removed that will not be.
	badDates int

	// broken records a property this parser could not use. The event is
	// dropped and counted, and the rest of the feed is still imported: one
	// event that cannot be read costs that event, the same way one row of a
	// browser history costs that row. Aborting the feed instead would let a
	// single unsupported property cost a day of meetings.
	broken error

	// declined records that some attendee of this event said no. Which
	// attendee is compared against the addresses the config calls "me" and
	// then thrown away: the list of who else was invited is not stored, and
	// this bool is all that survives the function that reads it.
	declinedBy []string
}

// parseICS reads a calendar and returns its VEVENTs in file order.
//
// It is deliberately forgiving about everything that does not change a time:
// unknown components, unknown properties and unknown parameters are skipped
// rather than refused, because a calendar server may add any of them and a
// source that fails on the unexpected would stop importing for a week over a
// property nobody reads.
//
// It is not forgiving about size. The reader is bounded by the caller and the
// line length by maxLineBytes: this parses bytes off the network.
func parseICS(r io.Reader) ([]vevent, error) {
	var (
		events  []vevent
		cur     *vevent
		depth   int // components entered that are not VEVENT
		lineNum int
		// A download cut between two events loses every meeting after the cut
		// and used to report success — and a login page served with 200 was
		// indistinguishable from an empty calendar. Both are answered by
		// insisting the envelope is there and closed.
		calendars, closed int
	)

	err := eachContentLine(r, func(line string) error {
		lineNum++
		prop, ok := parseProperty(line)
		if !ok {
			// A line with no colon is not a content line. Nothing in a
			// calendar depends on it and refusing the file over it would
			// throw away every meeting in it.
			return nil
		}
		switch prop.name {
		case "BEGIN":
			if strings.EqualFold(prop.value, "VEVENT") && depth == 0 {
				if cur != nil {
					// A second BEGIN:VEVENT before the first ended used to
					// overwrite it, losing an event with no counter and no
					// note.
					return fmt.Errorf("line %d: an event begins inside another one", lineNum)
				}
				cur = &vevent{}
				return nil
			}
			if strings.EqualFold(prop.value, "VCALENDAR") && cur == nil && depth == 0 {
				calendars++
				return nil
			}
			// VTIMEZONE, VALARM, VTODO and whatever else: entered so that
			// their properties cannot be mistaken for the event's own. A
			// VALARM inside a VEVENT has its own TRIGGER and DURATION, and
			// reading that DURATION as the meeting's would resize it.
			if cur != nil || depth > 0 || !strings.EqualFold(prop.value, "VCALENDAR") {
				depth++
			}
			return nil
		case "END":
			if strings.EqualFold(prop.value, "VEVENT") && depth == 0 && cur != nil {
				// Only now: DURATION is relative to DTSTART and the format
				// does not promise which of the two comes first.
				cur.resolve()
				events = append(events, *cur)
				cur = nil
				return nil
			}
			if strings.EqualFold(prop.value, "VCALENDAR") && cur == nil && depth == 0 {
				if closed >= calendars {
					// Otherwise `closed` could run ahead and hide a genuine
					// truncation later in the file, which is the one thing
					// this counter exists to catch.
					return fmt.Errorf("line %d: END:VCALENDAR with nothing open", lineNum)
				}
				closed++
				return nil
			}
			if depth > 0 {
				depth--
			}
			return nil
		}
		if cur == nil || depth > 0 {
			return nil
		}
		// Recorded on the event, not returned: see vevent.broken. Structural
		// damage — an event inside an event, a file that stops early — is
		// still fatal, because that is the file being wrong rather than one
		// entry in it.
		if err := cur.set(prop, lineNum); err != nil && cur.broken == nil {
			cur.broken = err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Refused rather than dropped, in all three shapes: the meetings after a
	// cut are missing either way, and a short calendar that reports success is
	// the failure this package is most careful about.
	switch {
	case cur != nil:
		return nil, fmt.Errorf("the calendar ends in the middle of an event")
	case depth != 0:
		return nil, fmt.Errorf("the calendar ends in the middle of a component")
	case calendars == 0:
		return nil, fmt.Errorf("this is not an iCalendar feed: it has no BEGIN:VCALENDAR")
	case closed < calendars:
		return nil, fmt.Errorf("the calendar is not closed: the download stopped early")
	}
	return events, nil
}

// set records one property of the event being read.
func (v *vevent) set(p property, line int) error {
	switch p.name {
	case "UID":
		v.uid = unescapeText(p.value)
	case "SUMMARY":
		v.summary = unescapeText(p.value)
	case "STATUS":
		v.status = strings.ToUpper(strings.TrimSpace(p.value))
	case "TRANSP":
		v.transparent = strings.EqualFold(strings.TrimSpace(p.value), "TRANSPARENT")
	case "RRULE":
		if v.rrule != "" {
			return fmt.Errorf("line %d: a second RRULE; this parser expands one", line)
		}
		v.rrule = strings.TrimSpace(p.value)
	case "EXRULE":
		// Removed from the standard but still emitted. It takes occurrences
		// away, so ignoring it leaves meetings in that should not be there.
		return fmt.Errorf("line %d: EXRULE is not supported", line)
	case "DTSTART":
		t, allDay, zone, err := parseTime(p)
		if err != nil {
			return fmt.Errorf("line %d: DTSTART: %w", line, err)
		}
		v.start, v.allDay, v.zone = t, allDay, firstNonEmpty(v.zone, zone)
	case "DTEND":
		t, _, zone, err := parseTime(p)
		if err != nil {
			return fmt.Errorf("line %d: DTEND: %w", line, err)
		}
		v.end, v.zone = t, firstNonEmpty(v.zone, zone)
	case "DURATION":
		d, err := parseDuration(p.value)
		if err != nil {
			return fmt.Errorf("line %d: DURATION: %w", line, err)
		}
		// Held until the whole event is read: DURATION may arrive before
		// DTSTART, and it is relative to it.
		v.duration, v.hasDuration = d, true
	case "EXDATE":
		ts, bad := parseTimeList(p)
		v.exdates, v.badDates = append(v.exdates, ts...), v.badDates+bad
	case "RDATE":
		ts, bad := parseTimeList(p)
		v.rdates, v.badDates = append(v.rdates, ts...), v.badDates+bad
	case "RECURRENCE-ID":
		if strings.EqualFold(p.param("RANGE"), "THISANDFUTURE") {
			return fmt.Errorf("line %d: RECURRENCE-ID;RANGE=THISANDFUTURE moves every later "+
				"occurrence and this parser moves one", line)
		}
		t, _, _, err := parseTime(p)
		if err != nil {
			return fmt.Errorf("line %d: RECURRENCE-ID: %w", line, err)
		}
		v.recurID, v.hasRecur = t, true
	case "ATTENDEE":
		// The one place attendees are read. What is kept is the address of
		// whoever said no, so that the caller can ask "was that me"; every
		// other attendee of every other event goes no further than this line.
		if strings.EqualFold(p.param("PARTSTAT"), "DECLINED") {
			v.declinedBy = append(v.declinedBy, address(p.value))
		}
	}
	return nil
}

// resolve finishes an event once every property has been seen: it turns a
// DURATION into an end time and supplies the end the format leaves implicit.
func (v *vevent) resolve() {
	if v.hasDuration && v.end.IsZero() {
		v.end = v.start.Add(v.duration)
		return
	}
	if !v.end.IsZero() {
		return
	}
	// RFC 5545 §3.6.1: with neither DTEND nor DURATION, a date-time event
	// lasts no time at all and a whole-day event lasts the day.
	if v.allDay {
		v.end = v.start.AddDate(0, 0, 1)
		return
	}
	v.end = v.start
}

// address reduces a CAL-ADDRESS to the address itself: "mailto:a@b" is "a@b".
func address(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ":"); i >= 0 && strings.EqualFold(s[:i], "mailto") {
		s = s[i+1:]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// eachContentLine unfolds the file and hands over one logical line at a time.
//
// RFC 5545 §3.1 folds a long line by inserting a line break and one space or
// tab; unfolding removes exactly that one character. Both CRLF and bare LF are
// accepted, because exports written by hand and files that have been through a
// text editor use the latter.
func eachContentLine(r io.Reader, fn func(string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var (
		b     strings.Builder
		first = true
	)
	flush := func() error {
		if b.Len() == 0 {
			return nil
		}
		line := b.String()
		b.Reset()
		return fn(line)
	}
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if first {
			// A byte order mark is common on .ics files that have been
			// through a Windows editor or a mail client export, and the
			// file: input exists for exactly those. Left in place it makes
			// the first property name unrecognisable, and the envelope check
			// then refuses a perfectly good calendar.
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
		}
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if b.Len()+len(line) > maxLineBytes {
				return fmt.Errorf("a content line is longer than %d bytes", maxLineBytes)
			}
			b.WriteString(line[1:])
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		b.WriteString(line)
	}
	if err := sc.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return fmt.Errorf("a line is longer than %d bytes", maxLineBytes)
		}
		return err
	}
	return flush()
}

// parseProperty splits "NAME;PARAM=value:the value" into its three parts.
//
// The colon that ends the parameters is the first one outside double quotes:
// a quoted parameter may contain colons, which is how "TZID" values and URIs
// are written.
func parseProperty(line string) (property, bool) {
	var (
		quoted bool
		colon  = -1
		semi   = -1
	)
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				colon = i
			}
		case ';':
			if !quoted && semi < 0 {
				semi = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return property{}, false
	}
	head, value := line[:colon], line[colon+1:]
	name := head
	params := map[string][]string{}
	if semi >= 0 && semi < colon {
		name = head[:semi]
		for _, part := range splitOutsideQuotes(head[semi+1:], ';') {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			k = strings.ToUpper(strings.TrimSpace(k))
			for _, one := range splitOutsideQuotes(v, ',') {
				params[k] = append(params[k], strings.Trim(strings.TrimSpace(one), `"`))
			}
		}
	}
	return property{
		name:   strings.ToUpper(strings.TrimSpace(name)),
		params: params,
		value:  value,
	}, true
}

// splitOutsideQuotes splits on sep, ignoring separators inside double quotes.
func splitOutsideQuotes(s string, sep byte) []string {
	var (
		out    []string
		quoted bool
		start  int
	)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			quoted = !quoted
		case sep:
			if !quoted {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// unescapeText undoes the escaping RFC 5545 §3.3.11 puts on a TEXT value.
func unescapeText(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		case '\\', ';', ',':
			b.WriteByte(s[i])
		default:
			// Not an escape this format defines. Kept as written rather than
			// swallowed, so a summary containing a Windows path still reads
			// as the path it was.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// parseTime reads a DATE or DATE-TIME value with its TZID.
//
// The three shapes of a date-time, and what each means:
//
//	20260901T090000Z            UTC, said outright
//	TZID=Europe/Moscow:...      that zone
//	20260901T090000             "floating": whatever the clock says where the
//	                            reader is, which is this machine's zone
//
// A zone name the database does not know is returned rather than guessed at,
// and the value is read as local so that the event still exists. Silently
// treating an unknown zone as UTC would move a meeting by hours.
func parseTime(p property) (t time.Time, allDay bool, unknownZone string, err error) {
	v := strings.TrimSpace(p.value)
	// Quoted back only in part: the value is a slice of the feed, bounded by
	// maxLineBytes and nothing smaller, and the message goes to a terminal.
	q := clip(v)
	if strings.EqualFold(p.param("VALUE"), "DATE") || len(v) == 8 {
		d, err := time.ParseInLocation("20060102", v, time.Local)
		if err != nil {
			return time.Time{}, false, "", fmt.Errorf("%q is not a date", q)
		}
		return d, true, "", nil
	}
	if strings.HasSuffix(v, "Z") {
		d, err := time.ParseInLocation("20060102T150405Z", v, time.UTC)
		if err != nil {
			return time.Time{}, false, "", fmt.Errorf("%q is not a UTC date-time", q)
		}
		return d, false, "", nil
	}

	loc := time.Local
	tzid := p.param("TZID")
	if tzid != "" {
		l, lerr := time.LoadLocation(tzid)
		if lerr != nil {
			unknownZone = tzid
		} else {
			loc = l
		}
	}
	d, perr := time.ParseInLocation("20060102T150405", v, loc)
	if perr != nil {
		return time.Time{}, false, "", fmt.Errorf("%q is not a date-time", q)
	}
	return d, false, unknownZone, nil
}

// clip shortens a value from the feed to something a message can carry.
func clip(s string) string {
	const limit = 60
	if len([]rune(s)) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

// parseTimeList reads the comma separated values of EXDATE or RDATE, and
// reports how many of them it could not read.
//
// One unreadable exception must not take the whole series with it, so a bad
// value is skipped — but not silently. RDATE;VALUE=PERIOD is legal and some
// servers emit it; skipping it drops an occurrence, and every other refusal in
// this package is counted and printed.
func parseTimeList(p property) (out []time.Time, unreadable int) {
	for _, one := range splitOutsideQuotes(p.value, ',') {
		q := property{name: p.name, params: p.params, value: one}
		t, _, _, err := parseTime(q)
		if err != nil {
			unreadable++
			continue
		}
		out = append(out, t)
	}
	return out, unreadable
}

// parseDuration reads an RFC 5545 duration: "PT1H", "P1DT2H30M", "-PT15M".
//
// Weeks and days are counted as fixed lengths, which is what the format says
// for a duration (§3.3.6) and is not the same as adding a calendar day.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	q := clip(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("%q is not a duration", q)
	}
	s = s[1:]

	var (
		total  time.Duration
		inTime bool
		num    strings.Builder
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			num.WriteByte(c)
			continue
		}
		if c == 'T' {
			inTime = true
			continue
		}
		n, err := strconv.Atoi(num.String())
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration", q)
		}
		num.Reset()
		var unit time.Duration
		switch c {
		case 'W':
			unit = 7 * 24 * time.Hour
		case 'D':
			unit = 24 * time.Hour
		case 'H':
			if !inTime {
				return 0, fmt.Errorf("%q puts hours before T", q)
			}
			unit = time.Hour
		case 'M':
			if !inTime {
				return 0, fmt.Errorf("%q has no months in a duration", q)
			}
			unit = time.Minute
		case 'S':
			if !inTime {
				return 0, fmt.Errorf("%q puts seconds before T", q)
			}
			unit = time.Second
		default:
			return 0, fmt.Errorf("%q is not a duration", q)
		}
		total += time.Duration(n) * unit
	}
	if num.Len() > 0 {
		return 0, fmt.Errorf("%q ends in a number with no unit", q)
	}
	if neg {
		total = -total
	}
	return total, nil
}
