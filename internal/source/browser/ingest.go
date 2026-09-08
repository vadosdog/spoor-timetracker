// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package browser reads browsing history — Firefox's places.sqlite and
// Chrome's History — as a source of time.
//
// It is the second source by weight and the first by nuisance. On the survey
// it accounted for about two fifths of the reconstructed hours and for all of
// the time no other source could put a name to: half the clusters of a day
// contain nothing but browsing.
//
// Three things shape everything in this package:
//
//   - Metadata only, and less of it than a URL. Host, port, first path
//     segment, title, time. Never the query string: that is where session
//     tokens and search terms are.
//   - The database belongs to a running program. Both browsers hold their
//     history open and lock it, so spoor reads a copy and never the original,
//     and a browser being open is not a reason for the import to fail.
//   - Browsers forget. Chrome keeps 90 days, Firefox longer, so what has been
//     imported has to outlive the file it came from — the same reason the
//     Claude Code source is incremental, and the same conclusion.
package browser

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure Go driver: no cgo, ever

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// batchSize is how many events go into one transaction.
const batchSize = 2000

// cursorLayout is how the watermark is written in source_files. Microseconds,
// because that is the resolution both browsers record visits at; rounding it
// to milliseconds would re-read or skip the visits inside the rounded slice.
const cursorLayout = "2006-01-02T15:04:05.000000Z"

// cursorOverlap is how far behind the newest imported visit each run starts
// reading again.
//
// A watermark with no overlap assumes a history only ever grows forward in
// time, and it does not: a clock corrected backwards, a resume from suspend,
// a restored profile, and above all browser sync — which writes another
// device's visits carrying their original timestamps — all insert rows behind
// the newest one. Those rows would be read never, and nothing would say so,
// and the database is the only copy of anything past the browser's own
// retention.
//
// A week of overlap is a few thousand rows to re-read and throw away on the
// dedup key, which costs milliseconds. Anything backdated further than this
// is still missed; that is the residue of choosing to be incremental at all.
const cursorOverlap = 7 * 24 * time.Hour

// sidecars are the files SQLite keeps beside a database. They have to be
// copied along with it: with a rollback journal (which is what Chrome uses)
// the main file can hold pages of a transaction that was never committed, and
// only the journal says so.
var sidecars = []string{"-journal", "-wal", "-shm"}

// reader is what a browser flavour has to provide: a query from a watermark,
// and how to read one row of its answer.
type reader interface {
	flavour() string
	query(db *sql.DB, since time.Time) (*sql.Rows, error)
	scan(rows *sql.Rows) (visit, error)
}

// Report is what one import run did.
type Report struct {
	Profiles        int
	ProfilesRead    int
	ProfilesSkipped int // unchanged since the previous run
	Visits          int // rows read out of the history databases
	Events          int // visits that became events
	NewEvents       int // of those, rows that were not already in the database
	NotWeb          int // not http or https, so not this source's business
	Ignored         int // dropped by the ignore list, before the insert
	Unparseable     int // not a URL at all
	Errors          []string
}

// Ingest reads every profile given and writes the visits it finds.
//
// Reading is incremental: each profile remembers the time of the newest visit
// already imported and asks only for what is at or after it. A profile whose
// file has not changed at all since the last run is not even copied.
//
// One profile that cannot be read costs that profile and nothing else. A
// browser running with its history locked is the normal case, not a failure.
func Ingest(st *store.Store, profiles []Profile, ignore Ignore) (Report, error) {
	var rep Report
	if !Supported() {
		return rep, nil
	}
	rep.Profiles = len(profiles)

	for _, p := range profiles {
		if err := ingestProfile(st, p, ignore, &rep); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", p.Path, err))
		}
	}
	return rep, nil
}

func ingestProfile(st *store.Store, p Profile, ignore Ignore, rep *Report) error {
	rd, err := readerFor(p.Flavour)
	if err != nil {
		return err
	}

	size, mtime, err := stampOf(p.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // the profile went away between listing and reading
		}
		return err
	}

	prev, known, err := st.FileState(SourceName, p.Path)
	if err != nil {
		return err
	}
	if known && prev.Size == size && prev.MTime.Equal(mtime) {
		rep.ProfilesSkipped++
		return nil
	}

	var since time.Time
	if known && prev.Cursor != "" {
		if t, err := time.Parse(cursorLayout, prev.Cursor); err == nil {
			since = t.Add(-cursorOverlap)
		}
		// An unreadable watermark is not a reason to lose visits: leaving
		// since at zero re-reads the profile from the start, and the dedup
		// key throws away what is already stored.
	}

	copyPath, cleanup, err := copyDatabase(p.Path)
	if err != nil {
		return err
	}
	defer cleanup()

	rep.ProfilesRead++
	newest, err := readVisits(st, copyPath, rd, p, since, ignore, rep)
	if err != nil {
		return err
	}

	// The watermark moves only over visits that are committed: crash before
	// this and the next run reads the same tail again, which dedup makes free.
	cursor := prev.Cursor
	if !newest.IsZero() {
		cursor = newest.UTC().Format(cursorLayout)
	}
	return st.SaveFileState(SourceName, p.Path, store.FileState{
		Size:   size,
		MTime:  mtime,
		Cursor: cursor,
	})
}

func readerFor(flavour string) (reader, error) {
	switch flavour {
	case "chrome":
		return chromeReader{}, nil
	case "firefox":
		return firefoxReader{}, nil
	default:
		return nil, fmt.Errorf("unknown browser flavour %q", flavour)
	}
}

// readVisits imports everything at or after since and returns the time of the
// newest visit it saw.
func readVisits(st *store.Store, path string, rd reader, p Profile, since time.Time, ignore Ignore, rep *Report) (time.Time, error) {
	db, err := openCopy(path)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = db.Close() }()

	rows, err := rd.query(db, since)
	if err != nil {
		return time.Time{}, fmt.Errorf("read history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		newest time.Time
		// A visit cannot honestly happen later than now, but a machine whose
		// clock was wrong records one: a restored snapshot, a dual-boot RTC
		// read as local time, sync from a device running fast. Such a row is
		// imported like any other — it did happen — but it must not move the
		// watermark, or the profile stops importing anything until the date
		// on it arrives, silently, and deleting the row afterwards does not
		// undo it. The overlap guards against skew backwards; this is the
		// same guard forwards, and without it the two are not symmetric.
		now   = time.Now()
		batch = make([]event.Event, 0, batchSize)
	)
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

	// Whatever has been read stays read. A failure part-way through must not
	// throw away the rows already in hand: the watermark does not move on an
	// error, so committing them early costs one deduplicated re-read next
	// time and saves everything before the bad row.
	defer func() { _ = flush() }()

	for rows.Next() {
		v, err := rd.scan(rows)
		if err != nil {
			return time.Time{}, err
		}
		rep.Visits++
		if v.Time.After(newest) && !v.Time.After(now) {
			newest = v.Time
		}

		ev, outcome := toEvent(v, rd.flavour(), p.Name, ignore)
		switch outcome {
		case Kept:
			rep.Events++
			batch = append(batch, ev)
			if len(batch) >= batchSize {
				if err := flush(); err != nil {
					return time.Time{}, err
				}
			}
		case NotWeb:
			rep.NotWeb++
		case Ignored:
			rep.Ignored++
		case Unparseable:
			rep.Unparseable++
		}
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, err
	}
	if err := flush(); err != nil {
		return time.Time{}, err
	}
	return newest, nil // the deferred flush above is a no-op by now
}

// openCopy opens a copied history database.
//
// The copy is opened read-write on purpose: if the original was copied while
// a transaction was in flight, SQLite has to roll the journal back before the
// file makes sense, and it cannot do that to a database it may not write.
// Nothing here issues anything but SELECT, and the file is deleted afterwards.
func openCopy(path string) (*sql.DB, error) {
	// Built through url.URL rather than by concatenation, for the same reason
	// as in the store: SQLite parses a file: DSN as a URI.
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "_pragma=busy_timeout(5000)",
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open history copy: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// copyDatabase copies a history database, and whatever SQLite keeps beside
// it, into a private temporary directory.
//
// This is the whole answer to "the browser is running". Both browsers hold
// their history open, Firefox with a write-ahead log and Chrome with a
// rollback journal, and opening the original would either fail on the lock or
// disturb a file spoor has no business touching. It is also why the numbers
// can be a moment stale: the copy is not atomic, and a visit made during it
// is picked up by the next run rather than this one.
func copyDatabase(path string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "spoor-history-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.Chmod(dir, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}

	dst := filepath.Join(dir, filepath.Base(path))
	if err := copyFile(path, dst); err != nil {
		cleanup()
		return "", nil, err
	}
	for _, suffix := range sidecars {
		// A sidecar that is not there is the normal case: it exists only
		// while a transaction is in flight, or while the browser is running.
		if err := copyFile(path+suffix, dst+suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanup()
			return "", nil, err
		}
	}
	return dst, cleanup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	// The copy holds the same browsing history as the original, so it gets
	// the same permissions the rest of spoor's data has: owner only.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// stampOf is the "has anything happened here" check: the size and mtime of
// the database and of every sidecar, folded together. The sidecars matter —
// a browser can record visits in its write-ahead log for a long while without
// the main file changing at all.
func stampOf(path string) (int64, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	size, mtime := info.Size(), info.ModTime()

	for _, suffix := range sidecars {
		si, err := os.Stat(path + suffix)
		if err != nil {
			continue
		}
		size += si.Size()
		if si.ModTime().After(mtime) {
			mtime = si.ModTime()
		}
	}
	return size, mtime, nil
}
