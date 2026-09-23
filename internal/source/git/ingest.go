// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package git

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// tsLayout matches every other source: UTC, milliseconds, always Z, so that
// comparing the strings in SQL compares the instants.
const tsLayout = "2006-01-02T15:04:05.000Z"

// idLayout is the instant inside an external_id. To the second, because that
// is the whole resolution a reflog records — writing three zeroes of
// milliseconds into the key would be three characters pretending to be a
// measurement.
const idLayout = "2006-01-02T15:04:05Z"

// Report is what one import run did.
type Report struct {
	Repos     int
	ReposRead int
	Logs      int // reflog files read
	Entries   int // lines that looked like a reflog line
	Events    int // entries inside the range, after collapsing
	NewEvents int // of those, rows that were not already in the database
	// Collapsed is entries that recorded a moment another ref had already
	// recorded — one commit is written to HEAD and to its branch.
	Collapsed int
	// OutOfRange is entries outside the window the source was asked about,
	// which on a first run is most of the history and after it is most of
	// everything. A clock set forward lands here too.
	OutOfRange int
	Malformed  int
	// Capped is reflog files so long that only their tail was read.
	Capped int
	Errors []string
}

// Collect turns a date range into events, and touches nothing else.
//
// This is the shape the plugin contract is going to have — a range in, a list
// of events out — and this source is the first one that fits it exactly. The
// other three cannot: the two incremental ones carry bookkeeping that has to
// live beside the database, and the calendar needs to say more than "here are
// the events", because replacing a window needs "and I understood all of it".
// A reflog needs none of that. It is re-read whole every run and the identity
// of an entry throws away what is already stored.
//
// from is inclusive, to exclusive.
func Collect(repos []Repo, from, to time.Time) ([]event.Event, Report) {
	var rep Report
	rep.Repos = len(repos)
	if !Supported() {
		return nil, rep
	}

	var events []event.Event
	for _, r := range repos {
		dir, err := gitDir(r.Path)
		if err != nil {
			// A configured path that is not a repository is a typo, and a typo
			// here means this repository is never read and nothing says so.
			// Reported rather than passed over — the opposite of how a
			// *discovered* absence is treated, because nobody discovered this
			// one, somebody wrote it down.
			rep.Errors = append(rep.Errors, fmt.Sprintf("repository %q: %v", r.ID, err))
			continue
		}
		rep.ReposRead++

		for _, e := range readRepo(dir, &rep) {
			if e.At.Before(from) || !e.At.Before(to) {
				rep.OutOfRange++
				continue
			}
			rep.Events++
			events = append(events, toEvent(e, r))
		}
	}

	// A total order that does not depend on the order repositories were
	// configured in or on how the filesystem handed back a directory. Two runs
	// over the same disk produce the same slice.
	sort.Slice(events, func(i, j int) bool {
		if events[i].TS != events[j].TS {
			return events[i].TS < events[j].TS
		}
		return events[i].ExternalID < events[j].ExternalID
	})
	return events, rep
}

// toEvent reduces one reflog entry to the metadata spoor keeps.
//
// No duration, ever. A commit is a point: nothing in the repository says how
// long the work before it took, and a source that guessed would be inventing
// the one number this tool exists to measure honestly.
func toEvent(e entry, r Repo) event.Event {
	return event.Event{
		Source: SourceName,
		// The repository's id, the instant and the resulting sha. Not the
		// position in the log: `HEAD@{0}` is renumbered by the next action, so
		// a key built on it would re-import the whole reflog under new
		// identities every time anything happened. Time and sha do not move —
		// not when the log is truncated, not when a rebase makes the object
		// unreachable, not when the branch is deleted.
		ExternalID: fmt.Sprintf("%s/%s/%s", r.ID, e.At.Format(idLayout), short(e.New)),
		TS:         e.At.Format(tsLayout),
		DurationMS: nil,
		Type:       "reflog",
		Subtype:    e.Verb,
		// The crude guess, exactly as the Claude Code source makes it: the
		// last element of the working directory. It is what the dictionary's
		// `fallback` and `never.paths` settings are written against, so a
		// repository nobody has decided about behaves the same way a directory
		// nobody has decided about already does.
		Project: filepath.Base(r.Path),
		// Which shas moved, in git's own range syntax, so that the row can be
		// pasted back into `git log` by whoever is reading the database. The
		// message is not here and is never read: it is a commit message.
		RawText:    short(e.Old) + ".." + short(e.New),
		Entrypoint: r.ID,
		CWD:        r.Path,
		GitBranch:  e.Ref,
	}
}

// short is the abbreviation people actually use. Twelve rather than seven: a
// reflog can hold years of one repository, and seven collides.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Ingest collects and stores. Append only: a reflog entry that has gone is
// still an action that happened, so nothing here deletes and nothing restates.
func Ingest(st *store.Store, repos []Repo, from, to time.Time) (Report, error) {
	events, rep := Collect(repos, from, to)
	if len(events) == 0 {
		return rep, nil
	}
	n, err := st.InsertEvents(events)
	if err != nil {
		return rep, err
	}
	rep.NewEvents = n
	return rep, nil
}

// Expand resolves a configured path into a Repo, with "~" meaning the home
// directory and nothing else expanded — the same rule every other path setting
// follows, because a config that interpolated environment variables would read
// differently under cron.
func Expand(id, path string, expand func(string) (string, error)) (Repo, error) {
	p, err := expand(path)
	if err != nil {
		return Repo{}, err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return Repo{}, err
	}
	return Repo{ID: id, Path: strings.TrimRight(filepath.Clean(abs), "/")}, nil
}

// Describe is the one-line summary the CLI prints. Kept here so that the
// counters and the words about them cannot drift apart.
func (r Report) Describe() string {
	return fmt.Sprintf("git: %d repositories (%d read), %d reflogs, %d entries",
		r.Repos, r.ReposRead, r.Logs, r.Entries)
}

// Notes are the things a run refused, one line each and only when there is
// something to say. A source that quietly drops most of what it reads looks
// exactly like one that works.
func (r Report) Notes() []string {
	var out []string
	if r.Malformed > 0 {
		out = append(out, fmt.Sprintf(
			"git: %d line(s) under logs/ were not reflog lines and were skipped", r.Malformed))
	}
	if r.Capped > 0 {
		out = append(out, fmt.Sprintf(
			"git: %d reflog(s) were longer than the read limit, so only the most recent "+
				"part of them was read", r.Capped))
	}
	return out
}

func errorIsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

func errorIsPermission(err error) bool { return errors.Is(err, fs.ErrPermission) }
