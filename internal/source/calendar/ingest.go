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

// batchSize is how many events go into one transaction.
const batchSize = 2000

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

	batch := make([]event.Event, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := st.InsertEvents(batch)
		if err != nil {
			return err
		}
		rep.NewEvents += n
		batch = batch[:0]
		return nil
	}
	for _, m := range meetings {
		rep.Events++
		batch = append(batch, ToEvent(m, c.ID))
		if len(batch) >= batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
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
