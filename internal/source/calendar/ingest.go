// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package calendar

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Calendar is one feed to read.
//
// Exactly one of URLFile and File is set. The URL itself is not a field: it
// never travels as a plain value through the program's configuration, because
// the configuration is the artefact people paste into issues and commit to
// dotfile repositories. What travels is the path to a file holding it.
type Calendar struct {
	ID string
	// URLFile holds nothing but the URL. It is read at the moment of use and
	// the value is not kept anywhere afterwards.
	URLFile string
	// File is a downloaded .ics on disk, for a machine that does not go to
	// the network at all. The same parser, a different way in.
	File string
	// Me is the addresses that are the user, for deciding which declined
	// meetings are declined by them.
	Me []string
}

// Report is what one import run did.
type Report struct {
	Calendars     int
	CalendarsRead int
	Counts        Counts
	Events        int
	NewEvents     int
	// MovedEvents is meetings that were already stored and are now at another
	// time, of another length, or under another name. RemovedEvents is the
	// ones the feed no longer holds: cancelled, or an occurrence of a series
	// that moved. Both are only possible because this source replaces its
	// window rather than appending to it.
	MovedEvents   int
	RemovedEvents int
	Errors        []string
}

// Ingest reads every calendar given and stores the meetings it finds.
//
// One calendar that cannot be read costs that calendar and nothing else: the
// others are still imported and the failure is reported. A calendar whose
// server is down must not take the day's other meetings with it — and it must
// not pass quietly either, or the day loses hours and nothing says why.
func Ingest(ctx context.Context, st *store.Store, cals []Calendar, opts Options) (Report, error) {
	var rep Report
	rep.Calendars = len(cals)

	for _, c := range cals {
		if err := ingestOne(ctx, st, c, opts, &rep); err != nil {
			// The calendar id, never the address it was read from.
			rep.Errors = append(rep.Errors, fmt.Sprintf("calendar %q: %v", c.ID, err))
		}
	}
	return rep, nil
}

func ingestOne(ctx context.Context, st *store.Store, c Calendar, opts Options, rep *Report) error {
	data, err := read(ctx, c)
	if err != nil {
		return err
	}
	// The raw feed lives in this function and nowhere else. It is never
	// written to a temporary file and never cached: it holds every field this
	// project refuses to store — attendees, description, location, the
	// conference link — so keeping a copy would put more on the machine than
	// the database is allowed to hold.
	opts.Me = c.Me
	meetings, counts, err := Parse(data, opts)
	if err != nil {
		return err
	}
	// Counted here rather than after the fetch: "1 calendars (1 read)" beside
	// a warning reads as "an empty calendar" to anybody skimming, and the
	// README teaches people to read these counts as the signal. A feed that
	// arrived and could not be parsed was not read.
	rep.CalendarsRead++

	named := counts
	named.Notes = nil
	for _, n := range counts.Notes {
		named.Notes = append(named.Notes, fmt.Sprintf("calendar %q: %s", c.ID, n))
	}
	rep.Counts.add(named)

	events := make([]event.Event, 0, len(meetings))
	for _, m := range meetings {
		rep.Events++
		events = append(events, ToEvent(m, c.ID))
	}

	// The window is replaced rather than appended to, and this is the only
	// source that does it. A feed does not append: it restates. The identity
	// of a meeting is calendar + UID + occurrence, and a meeting moved from
	// 10:00 to 14:00 keeps all three — so an insert that ignores conflicts
	// keeps the old hour for ever. A series moved wholesale changes every
	// occurrence, so the new times arrive, the old ones stay, and the day is
	// counted twice. A meeting cancelled after it was imported is skipped at
	// parse time and its row outlives it. All three are "quietly more than
	// there was", which is the failure this tool exists to prevent.
	//
	// The window is the one that was expanded, so a meeting older than
	// --calendar-back is left alone: the feed said nothing about it.
	//
	// A feed holding events, all of them cancelled or all of them all-day, is
	// a feed that answered: there are no meetings here, remove what was. A
	// feed holding no events at all is not — that is also what an expired
	// address and a maintenance page look like, and the difference between
	// the two is the difference between a correct empty day and a year of
	// meetings gone.
	//
	// And **one** event this parser could not read is enough to stop the
	// deleting half. Those are spoor failing to understand the feed, not the
	// feed saying the meetings are gone; the day a provider changes a rule
	// shape is the day a series' whole stored history would otherwise be
	// deleted while `ingest` exits 0.
	//
	// It was written as "were they *all* unreadable", which protects only the
	// case where nothing at all parsed — and the case worth protecting is one
	// series out of fifty. Inserting and updating still happen; the run says
	// what it refused to remove and why, so a genuinely cancelled meeting is
	// one fixed feed away from going.
	sync, err := st.ReplaceWindow(store.Window{
		Source:     SourceName,
		Entrypoint: entrypointOf(c.ID),
		From:       opts.From,
		To:         opts.To,
		Events:     events,
		Whole: counts.Events > 0 &&
			counts.Unreadable == 0 && counts.Unexpanded == 0 && counts.Truncated == 0,
	})
	if err != nil {
		return err
	}
	// Two reasons, two messages. They are the two shapes of "spoor did not
	// understand this feed", and the one diagnostic for the one operation that
	// can lose data has to say which happened: an expired address looks
	// nothing like a changed recurrence rule, and the fix is different.
	if unread := counts.Unreadable + counts.Unexpanded + counts.Truncated; sync.RefusedEmpty {
		switch {
		case unread > 0:
			rep.Counts.Notes = append(rep.Counts.Notes, fmt.Sprintf(
				"calendar %q: %s in this feed could not be read in full, so nothing was "+
					"removed from the window — spoor not understanding a feed is not the "+
					"feed saying a meeting is gone",
				c.ID, count(unread, "event", "events")))
		default:
			rep.Counts.Notes = append(rep.Counts.Notes, fmt.Sprintf(
				"calendar %q: this feed holds no events at all while the database holds "+
					"meetings from it, so nothing was removed — an empty answer is also "+
					"what an expired address and a login page look like", c.ID))
		}
	}
	rep.NewEvents += sync.New
	rep.MovedEvents += sync.Moved
	rep.RemovedEvents += sync.Removed
	return nil
}

// count is a number with its noun.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// read gets the feed, from the network or from a file.
func read(ctx context.Context, c Calendar) ([]byte, error) {
	switch {
	case c.File != "":
		// Bounded like the network path. A local file is the user's own, so
		// this is not about hostility — it is about the parser having one set
		// of limits rather than two.
		f, err := os.Open(c.File)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%s: no such file", c.File)
			}
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return readCapped(f)
	case c.URLFile != "":
		u, err := ReadSecret(c.URLFile)
		if err != nil {
			return nil, err
		}
		return Fetch(ctx, u)
	default:
		return nil, errors.New(`needs either "url_file" or "file"`)
	}
}

// ReadSecret reads the URL out of the file holding it.
//
// The file is the whole secret and nothing else, which is what makes it safe
// to handle: there is no format to parse, nothing to print for context, and
// no other setting that could be quoted alongside it in a message. Blank lines
// and a leading "#" comment are allowed so the file can say what it is.
//
// Errors name the path and never the contents. A path is not a secret; the one
// line inside it is.
func ReadSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: no such file — it holds the calendar URL, one line, and nothing else", path)
		}
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line, nil
	}
	return "", fmt.Errorf("%s: holds no URL", path)
}
