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
	"strconv"
	"strings"
	"time"
)

// Why this file exists at all: a weekly meeting is one VEVENT carrying an
// RRULE, not fifty-two of them. A reader that stores the VEVENT as written
// records one hour a year where an hour a week happened, and the day the
// meeting was actually on shows a hole. Expanding is not an optimisation of
// the format, it is the format.
//
// Two limits below are load-bearing rather than tidy. The file comes off the
// network, and "RRULE:FREQ=SECONDLY" with neither COUNT nor UNTIL is a legal
// rule that never ends. Expansion is therefore bounded twice: by how many
// candidate periods may be examined at all, and by how many occurrences one
// series may produce. Hitting either is reported, never swallowed — a series
// that stopped early is a series whose hours are missing from the day, and a
// number that is quietly too small is the one failure this project can least
// afford.

const (
	// maxPeriods caps how many candidate periods one rule may be walked
	// through. Reached only by a rule that repeats faster than the window is
	// wide, which is exactly the pathological case.
	maxPeriods = 200_000

	// maxOccurrences caps what one series may contribute. A daily meeting over
	// the longest window this source asks for is under five hundred.
	maxOccurrences = 10_000
)

// rrule is the subset of RFC 5545 §3.3.10 that real calendars emit.
//
// What is not here — BYSETPOS, BYWEEKNO, BYYEARDAY, BYHOUR and friends — is
// not silently ignored: parseRRule refuses a rule that carries one, and the
// caller reports the series rather than expanding it wrongly. Half-expanding
// a rule is worse than not expanding it, because the result looks like an
// answer.
type rrule struct {
	freq     string
	interval int
	count    int
	until    time.Time
	hasUntil bool

	byDay      []dayNum
	byMonthDay []int
	byMonth    []time.Month
	wkst       time.Weekday
}

// dayNum is a BYDAY entry: a weekday, optionally the n-th one of the period.
// "1MO" is the first Monday, "-1FR" the last Friday, "MO" every Monday.
type dayNum struct {
	n  int
	wd time.Weekday
}

var weekdays = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

// parseRRule reads a recurrence rule, or says why it will not.
func parseRRule(s string, loc *time.Location) (rrule, error) {
	r := rrule{interval: 1, wkst: time.Monday}
	for _, part := range strings.Split(s, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return r, fmt.Errorf("%q is not NAME=VALUE", part)
		}
		k, v = strings.ToUpper(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "FREQ":
			r.freq = strings.ToUpper(v)
		case "INTERVAL":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return r, fmt.Errorf("INTERVAL=%q is not a positive number", v)
			}
			r.interval = n
		case "COUNT":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return r, fmt.Errorf("COUNT=%q is not a positive number", v)
			}
			r.count = n
		case "UNTIL":
			t, err := parseUntil(v, loc)
			if err != nil {
				return r, err
			}
			r.until, r.hasUntil = t, true
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				dn, err := parseDayNum(strings.TrimSpace(d))
				if err != nil {
					return r, err
				}
				r.byDay = append(r.byDay, dn)
			}
		case "BYMONTHDAY":
			for _, d := range strings.Split(v, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(d))
				if err != nil || n == 0 || n < -31 || n > 31 {
					return r, fmt.Errorf("BYMONTHDAY=%q is not a day of a month", d)
				}
				r.byMonthDay = append(r.byMonthDay, n)
			}
		case "BYMONTH":
			for _, d := range strings.Split(v, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(d))
				if err != nil || n < 1 || n > 12 {
					return r, fmt.Errorf("BYMONTH=%q is not a month", d)
				}
				r.byMonth = append(r.byMonth, time.Month(n))
			}
		case "WKST":
			wd, ok := weekdays[strings.ToUpper(v)]
			if !ok {
				return r, fmt.Errorf("WKST=%q is not a weekday", v)
			}
			r.wkst = wd
		default:
			// The refusal that keeps this honest. BYSETPOS in particular
			// changes which occurrences survive, so ignoring it would move
			// meetings to days they were never on.
			return r, fmt.Errorf("%q is not supported", k)
		}
	}
	switch r.freq {
	case "SECONDLY", "MINUTELY", "HOURLY", "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	case "":
		return r, fmt.Errorf("no FREQ")
	default:
		return r, fmt.Errorf("FREQ=%q is not a frequency", r.freq)
	}
	// "The second Tuesday" only means anything inside a month or a year. RFC
	// 5545 §3.3.10 forbids the ordinal form with WEEKLY, and there is nothing
	// for it to count within a day. Refused rather than read as "of the
	// month", which is a guess that would put a meeting on the wrong week.
	if r.freq != "MONTHLY" && r.freq != "YEARLY" {
		for _, d := range r.byDay {
			if d.n != 0 {
				return r, fmt.Errorf("BYDAY with an ordinal is not allowed with FREQ=%s", r.freq)
			}
		}
	}
	// §3.3.10 again: BYMONTHDAY MUST NOT appear with WEEKLY. It is a limit for
	// DAILY and below, and those are filtered in occurrencesIn.
	if r.freq == "WEEKLY" && len(r.byMonthDay) > 0 {
		return r, fmt.Errorf("BYMONTHDAY is not allowed with FREQ=WEEKLY")
	}
	return r, nil
}

// parseUntil reads the UNTIL value, which may be a date, a UTC date-time or a
// local one.
func parseUntil(v string, loc *time.Location) (time.Time, error) {
	switch {
	case len(v) == 8:
		t, err := time.ParseInLocation("20060102", v, loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("UNTIL=%q is not a date", v)
		}
		// A date means the whole of it.
		return t.AddDate(0, 0, 1).Add(-time.Nanosecond), nil
	case strings.HasSuffix(v, "Z"):
		t, err := time.ParseInLocation("20060102T150405Z", v, time.UTC)
		if err != nil {
			return time.Time{}, fmt.Errorf("UNTIL=%q is not a UTC date-time", v)
		}
		return t, nil
	default:
		t, err := time.ParseInLocation("20060102T150405", v, loc)
		if err != nil {
			return time.Time{}, fmt.Errorf("UNTIL=%q is not a date-time", v)
		}
		return t, nil
	}
}

func parseDayNum(s string) (dayNum, error) {
	if len(s) < 2 {
		return dayNum{}, fmt.Errorf("BYDAY=%q is not a weekday", s)
	}
	wd, ok := weekdays[strings.ToUpper(s[len(s)-2:])]
	if !ok {
		return dayNum{}, fmt.Errorf("BYDAY=%q is not a weekday", s)
	}
	d := dayNum{wd: wd}
	if prefix := s[:len(s)-2]; prefix != "" {
		n, err := strconv.Atoi(prefix)
		if err != nil || n == 0 {
			return dayNum{}, fmt.Errorf("BYDAY=%q has no such ordinal", s)
		}
		d.n = n
	}
	return d, nil
}

// expand walks the rule from start and returns the occurrence start times that
// fall inside [from, to).
//
// truncated says the walk stopped at a limit rather than at the end of the
// series, which means occurrences are missing and the caller has to say so.
//
// Occurrences before `from` are still counted, never emitted: COUNT is counted
// from the first occurrence of the series, not from the edge of the window
// somebody happened to ask about, and a window that changed the number of
// occurrences would make the same calendar import differently on Tuesday.
func (r rrule) expand(start, from, to time.Time) (out []time.Time, truncated bool) {
	if r.hasUntil && r.until.Before(to) {
		to = r.until.Add(time.Nanosecond)
	}

	seen := 0
	k := 0
	// Only the fixed-length frequencies may skip ahead. A month is not a fixed
	// length and a monthly rule can hold no occurrence at all — the 31st of
	// February — so counting skipped periods as occurrences would spend COUNT
	// on months that produced nothing and end the series early. Which months
	// were skipped depends on where the window starts, so the same calendar
	// would import differently depending on the day it was imported on: the
	// exact thing the paragraph above promises does not happen.
	if r.simple() && r.unit() > 0 {
		if skip := r.periodsBefore(start, from); skip > 0 {
			k, seen = skip, skip
		}
	}

	for periods := 0; periods < maxPeriods; periods++ {
		if r.count > 0 && seen >= r.count {
			return out, false
		}
		base := r.advance(start, k)
		k++
		// The earliest moment this period could hold, which is not the period's
		// base: a weekly period is expanded across the whole week its base
		// falls in, and that week begins up to six days earlier. Stopping on
		// the base itself ended a series one period early at the far edge of
		// the window and said nothing about it.
		if !r.periodFloor(base).Before(to) {
			return out, false
		}

		for _, t := range r.occurrencesIn(base, start) {
			// A period may reach back before DTSTART — the week containing it,
			// the first of its month — and a series does not exist before it
			// starts. Filtered before COUNT is charged, or an invented
			// occurrence would consume one of the real ones.
			if t.Before(start) {
				continue
			}
			if r.count > 0 && seen >= r.count {
				return out, false
			}
			seen++
			if r.hasUntil && t.After(r.until) {
				return out, false
			}
			if t.Before(from) || !t.Before(to) {
				continue
			}
			if len(out) >= maxOccurrences {
				return out, true
			}
			out = append(out, t)
		}
	}
	return out, true
}

// periodFloor is the earliest moment an occurrence of this period could fall
// on. For the fixed-length frequencies that is the base itself; for the
// calendar-shaped ones the period is anchored to the start of a week, a month
// or a year, and occurrences may sit anywhere inside it.
func (r rrule) periodFloor(base time.Time) time.Time {
	switch r.freq {
	case "WEEKLY":
		return startOfWeek(base, r.wkst)
	case "MONTHLY":
		return startOfMonth(base)
	case "YEARLY":
		return time.Date(base.Year(), time.January, 1, 0, 0, 0, 0, base.Location())
	}
	return base
}

// simple reports whether every period holds exactly one occurrence, at the
// same clock time as DTSTART.
func (r rrule) simple() bool {
	return len(r.byDay) == 0 && len(r.byMonthDay) == 0 && len(r.byMonth) == 0
}

// periodsBefore is how many whole periods fit between start and from. It is
// deliberately an under-estimate near the boundary: advance() is the authority
// on where a period lands, and starting one or two periods early costs a
// handful of iterations and cannot skip an occurrence.
func (r rrule) periodsBefore(start, from time.Time) int {
	if !start.Before(from) {
		return 0
	}
	// Only the fixed-length frequencies get here: expand guards on unit() > 0,
	// because a month is not a fixed length and a monthly period can hold no
	// occurrence at all. A branch for months here would read as a supported
	// path and never run.
	unit := r.unit()
	if unit <= 0 {
		return 0
	}
	n := int64(from.Sub(start) / (unit * time.Duration(r.interval)))
	if n < 2 {
		return 0
	}
	// Two periods of slack rather than one: a daylight-saving change shortens
	// or lengthens a day, so a division by a fixed unit can land just past the
	// boundary.
	return int(n - 2)
}

// unit is the fixed length of one step, or zero for the calendar-shaped
// frequencies where a step is a month or a year and has no fixed length.
func (r rrule) unit() time.Duration {
	switch r.freq {
	case "SECONDLY":
		return time.Second
	case "MINUTELY":
		return time.Minute
	case "HOURLY":
		return time.Hour
	case "DAILY":
		return 24 * time.Hour
	case "WEEKLY":
		return 7 * 24 * time.Hour
	}
	return 0
}

// advance moves start by k whole periods.
//
// Days and weeks move by AddDate rather than by adding hours: a day with a
// daylight-saving change is 23 or 25 hours long, and a daily meeting at 10:00
// stays at 10:00 across it rather than drifting to 09:00 for half the year.
func (r rrule) advance(start time.Time, k int) time.Time {
	n := k * r.interval
	switch r.freq {
	case "SECONDLY":
		return start.Add(time.Duration(n) * time.Second)
	case "MINUTELY":
		return start.Add(time.Duration(n) * time.Minute)
	case "HOURLY":
		return start.Add(time.Duration(n) * time.Hour)
	case "DAILY":
		return start.AddDate(0, 0, n)
	case "WEEKLY":
		return start.AddDate(0, 0, 7*n)
	case "MONTHLY":
		return startOfMonth(start).AddDate(0, n, 0)
	case "YEARLY":
		return start.AddDate(n, 0, 0)
	}
	return start
}

// occurrencesIn returns the occurrence times of one period, in order.
func (r rrule) occurrencesIn(base, start time.Time) []time.Time {
	if r.simple() {
		switch r.freq {
		case "MONTHLY":
			// The period is anchored to the first of the month; the occurrence
			// keeps DTSTART's day. A 31st in a 30-day month has no occurrence
			// at all, which is what RFC 5545 says and not the same as the 1st
			// of the next one.
			if start.Day() > daysIn(base.Year(), base.Month()) {
				return nil
			}
			return []time.Time{atClock(base.Year(), base.Month(), start.Day(), start)}
		case "YEARLY":
			// Same rule a year at a time, and the reason it cannot just use
			// base: AddDate normalises 29 February in a non-leap year to
			// 1 March, so a birthday-shaped rule would land on a date it never
			// falls on in three years out of four.
			if start.Day() > daysIn(base.Year(), start.Month()) {
				return nil
			}
			return []time.Time{atClock(base.Year(), start.Month(), start.Day(), start)}
		}
		return []time.Time{base}
	}

	switch r.freq {
	case "WEEKLY":
		// Nothing here says which day of the week, so DTSTART says it — the
		// same defaulting monthlyLike does for a month. Without it, "every
		// week in September" walked all seven days of every week and kept
		// each one, which is the BYMONTH bug of the first pass and the
		// BYMONTHDAY bug of the second, a third frequency along.
		if len(r.byDay) == 0 {
			if r.matchesMonth(base) {
				return []time.Time{base}
			}
			return nil
		}
		var out []time.Time
		weekStart := startOfWeek(base, r.wkst)
		for i := 0; i < 7; i++ {
			d := weekStart.AddDate(0, 0, i)
			if r.matchesDay(d, monthScope) && r.matchesMonth(d) {
				out = append(out, atClock(d.Year(), d.Month(), d.Day(), start))
			}
		}
		return out
	case "MONTHLY", "YEARLY":
		return r.monthlyLike(base, start)
	}
	// DAILY and the sub-day frequencies with a filter: the period is the
	// occurrence, kept or dropped. BYMONTHDAY belongs here too — it is a limit
	// for these frequencies, and leaving it out of this one condition meant
	// "the 15th of each month" expanded to every day of every month.
	if r.matchesDay(base, monthScope) && r.matchesMonth(base) &&
		r.matchesMonthDay(base, daysIn(base.Year(), base.Month())) {
		return []time.Time{base}
	}
	return nil
}

// monthlyLike expands a MONTHLY or YEARLY period that carries at least one BY
// rule.
func (r rrule) monthlyLike(base, start time.Time) []time.Time {
	loc := base.Location()

	months := []time.Time{startOfMonth(base)}
	if r.freq == "YEARLY" {
		months = months[:0]
		for m := time.January; m <= time.December; m++ {
			months = append(months, time.Date(base.Year(), m, 1, 0, 0, 0, 0, loc))
		}
	}

	// Nothing here says which day of the month, so DTSTART says it — the same
	// defaulting the simple branch above does. Without this, "every January"
	// meant every day of every January: sixty-two meetings where two belong.
	noDayRule := len(r.byMonthDay) == 0 && len(r.byDay) == 0

	// An ordinal BYDAY counts over the year when the rule is yearly and does
	// not name its months, and over the month otherwise. RFC 5545 §3.3.10.
	// Counted per month regardless, "the first Monday of the year" was the
	// first Monday of every month: twelve occurrences instead of one.
	scope := monthScope
	if r.freq == "YEARLY" && len(r.byMonth) == 0 {
		scope = yearScope
	}

	var out []time.Time
	for _, month := range months {
		if !r.matchesMonth(month) {
			continue
		}
		n := daysIn(month.Year(), month.Month())
		if noDayRule {
			if start.Day() <= n {
				out = append(out, atClock(month.Year(), month.Month(), start.Day(), start))
			}
			continue
		}
		for day := 1; day <= n; day++ {
			d := time.Date(month.Year(), month.Month(), day, 0, 0, 0, 0, loc)
			if !r.matchesMonthDay(d, n) || !r.matchesDay(d, scope) {
				continue
			}
			out = append(out, atClock(d.Year(), d.Month(), d.Day(), start))
		}
	}
	return out
}

// ordinalScope says what "the second Tuesday" is counted within.
type ordinalScope int

const (
	monthScope ordinalScope = iota
	yearScope
)

// matchesDay applies BYDAY. An entry with no ordinal matches every such
// weekday; one with an ordinal matches the n-th of its kind within scope,
// counted from the front for a positive number and from the back for a
// negative one.
func (r rrule) matchesDay(d time.Time, scope ordinalScope) bool {
	if len(r.byDay) == 0 {
		return true
	}
	for _, want := range r.byDay {
		if want.wd != d.Weekday() {
			continue
		}
		if want.n == 0 {
			return true
		}
		nth, fromEnd := ordinalOf(d, scope)
		if want.n > 0 && nth == want.n {
			return true
		}
		if want.n < 0 && fromEnd == -want.n {
			return true
		}
	}
	return false
}

// ordinalOf is which one of its weekday d is within its scope, counted both
// ways.
func ordinalOf(d time.Time, scope ordinalScope) (nth, fromEnd int) {
	if scope == yearScope {
		first := time.Date(d.Year(), time.January, 1, 0, 0, 0, 0, d.Location())
		first = first.AddDate(0, 0, (int(d.Weekday())-int(first.Weekday())+7)%7)
		last := time.Date(d.Year(), time.December, 31, 0, 0, 0, 0, d.Location())
		last = last.AddDate(0, 0, -((int(last.Weekday()) - int(d.Weekday()) + 7) % 7))
		return (d.YearDay()-first.YearDay())/7 + 1, (last.YearDay()-d.YearDay())/7 + 1
	}
	n := daysIn(d.Year(), d.Month())
	return (d.Day()-1)/7 + 1, (n-d.Day())/7 + 1
}

func (r rrule) matchesMonthDay(d time.Time, daysInMonth int) bool {
	if len(r.byMonthDay) == 0 {
		return true
	}
	for _, n := range r.byMonthDay {
		if n > 0 && d.Day() == n {
			return true
		}
		if n < 0 && d.Day() == daysInMonth+1+n {
			return true
		}
	}
	return false
}

func (r rrule) matchesMonth(d time.Time) bool {
	if len(r.byMonth) == 0 {
		return true
	}
	for _, m := range r.byMonth {
		if d.Month() == m {
			return true
		}
	}
	return false
}

// atClock puts DTSTART's wall-clock time on another date, in DTSTART's zone.
func atClock(year int, month time.Month, day int, start time.Time) time.Time {
	return time.Date(year, month, day,
		start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
}

func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func startOfWeek(t time.Time, wkst time.Weekday) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	back := (int(day.Weekday()) - int(wkst) + 7) % 7
	return day.AddDate(0, 0, -back)
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
