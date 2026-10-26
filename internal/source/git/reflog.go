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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/text"
)

// One reflog line, as git writes it:
//
//	<old sha> <new sha> <name> <<email>> <unix seconds> <±hhmm>\t<verb>: <message>
//
// Everything from the tab onwards is the message, and everything after the
// first ": " in it is free text somebody typed — a commit message, or the
// address a clone came from. This package reads the verb and stops.
const (
	// maxLogBytes is how much of one reflog file is read. A reflog is appended
	// to, so what matters is the end: past the cap the *tail* is read and the
	// run says so, rather than the file being refused and a repository going
	// silent. At roughly 150 bytes a line this is over fifty thousand entries.
	maxLogBytes = 8 << 20

	// maxEntries caps one repository. A limit that is reached is always
	// reported.
	maxEntries = 200_000

	// maxVerb and maxRef cap two strings that come off somebody else's disk.
	maxVerb = 32
	maxRef  = 256
)

// entry is one line of a reflog, reduced to what spoor may keep.
type entry struct {
	// Ref is the branch as a person writes it: "main", "origin/main",
	// "HEAD", "stash".
	Ref string
	// Rank orders the refs that recorded the same moment. One commit is
	// written to both `logs/HEAD` and `logs/refs/heads/<branch>` with the same
	// second and the same resulting sha; keeping the branch's copy is what
	// fills git_branch with something better than "HEAD".
	Rank int
	At   time.Time
	// Old and New are the shas the ref moved between, full length.
	Old, New string
	// Verb is the action and deliberately no more: taken from a fixed list,
	// or otherVerb when it is not on it. See verbOf.
	Verb string
}

// refRank decides which copy of one moment survives. Lower wins.
const (
	rankBranch = iota
	rankHEAD
	rankRemote
	rankOther
)

// readRepo reads every reflog under a git directory, newest-wins-free: the
// result is sorted and holds one entry per (moment, resulting sha).
//
// The caller gets the whole log rather than a slice of it. Filtering by date
// happens afterwards, in Collect, so that the date range stays a property of
// the source's interface rather than something smeared through its innards.
func readRepo(dir string, rep *Report) []entry {
	logs := filepath.Join(dir, "logs")
	var files []string
	err := filepath.WalkDir(logs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A repository that has never had a ref move has no logs
			// directory. That is a quiet repository, not a broken one.
			switch {
			case errorIsNotExist(err):
				return nil
			case errorIsPermission(err):
				rep.Errors = append(rep.Errors, text.Printable(path)+": permission denied, skipped")
				return nil
			}
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil && !errorIsNotExist(err) {
		rep.Errors = append(rep.Errors, text.Printable(logs)+": "+err.Error())
		return nil
	}
	// Walked in the order the filesystem hands directories back on some
	// systems; sorted here so that two runs read the same bytes in the same
	// order and the tie-break below cannot depend on the disk.
	sort.Strings(files)

	var entries []entry
	for _, path := range files {
		rel, err := filepath.Rel(logs, path)
		if err != nil {
			continue
		}
		rep.Logs++
		entries = append(entries, readLog(path, filepath.ToSlash(rel), rep)...)
		if len(entries) > maxEntries {
			rep.Errors = append(rep.Errors, text.Printable(dir)+": more than "+
				strconv.Itoa(maxEntries)+" reflog entries; the rest were not read")
			entries = entries[:maxEntries]
			break
		}
	}
	kept, collapsed := collapse(entries)
	rep.Collapsed += collapsed
	return kept
}

// readLog reads one reflog file.
func readLog(path, ref string, rep *Report) []entry {
	data, capped, err := tail(path)
	if err != nil {
		rep.Errors = append(rep.Errors, text.Printable(path)+": "+err.Error())
		return nil
	}
	if capped {
		rep.Capped++
	}

	name, rank := refName(ref)
	lines := strings.Split(string(data), "\n")
	// A capped read starts inside a line. Dropping the first one costs an
	// entry from the far past and keeps the parser from reporting a fragment
	// as malformed on every single run.
	if capped && len(lines) > 0 {
		lines = lines[1:]
	}

	out := make([]entry, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rep.Entries++
		e, ok, knownVerb := parseLine(line)
		if !ok {
			rep.Malformed++
			continue
		}
		if !knownVerb {
			rep.UnknownVerbs++
		}
		e.Ref, e.Rank = name, rank
		out = append(out, e)
	}
	return out
}

// tail reads the last maxLogBytes of a file. The second value says whether
// anything was left in front.
func tail(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	size := info.Size()
	if size <= maxLogBytes {
		data, err := os.ReadFile(path)
		return data, false, err
	}
	if _, err := f.Seek(size-maxLogBytes, 0); err != nil {
		return nil, false, err
	}
	// io.ReadFull rather than one Read: a single Read is not obliged to fill
	// the buffer, and a short one would silently shorten the tail. The result
	// has to be the same on every run — that is an invariant here, not a
	// preference.
	buf := make([]byte, maxLogBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	// Stat said the file was longer than the cap and the read returned
	// nothing, so it was truncated under us. Reported rather than passed
	// over: a log file that vanishes between two syscalls and leaves no
	// counter behind is exactly the silence this file is written against.
	if n == 0 {
		return nil, false, fmt.Errorf("read nothing from a file Stat reported as %d bytes", size)
	}
	return buf[:n], true, nil
}

// parseLine takes one reflog line apart.
//
// Everything from the tab on is the message. The verb is read out of it and
// the rest is dropped unread: past the first ": " is a commit message or the
// address a clone came from, and this project stores neither.
func parseLine(line string) (entry, bool, bool) {
	head, msg, _ := strings.Cut(line, "\t")

	fields := strings.Fields(head)
	// old, new, at least one field of the identity, the seconds and the zone.
	if len(fields) < 5 {
		return entry{}, false, true
	}
	old, next := fields[0], fields[1]
	if !isSHA(old) || !isSHA(next) {
		return entry{}, false, true
	}
	secs, err := strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return entry{}, false, true
	}

	verb, known := verbOf(msg)
	return entry{
		At:   time.Unix(secs, 0).UTC(),
		Old:  old,
		New:  next,
		Verb: verb,
	}, true, known
}

// isSHA accepts the two object-name lengths git uses, and nothing else. It is
// what tells a reflog line from any other file that ended up under logs/.
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// reflogVerbs is what git itself writes as the action of a reflog entry, and
// reflogQualifiers is the parenthesised part that may follow one.
//
// Both are allow-lists, and that is the whole point of them: see verbOf.
var reflogVerbs = map[string]bool{
	"am": true, "applypatch": true, "branch": true, "checkout": true,
	"cherry-pick": true, "clone": true, "commit": true, "detach": true,
	"fetch": true, "filter-branch": true, "gc": true, "initial": true,
	"merge": true, "pull": true, "push": true, "rebase": true,
	"reset": true, "revert": true, "stash": true, "update-ref": true,
}

var reflogQualifiers = map[string]bool{
	"(abort)": true, "(amend)": true, "(cherry-pick)": true, "(continue)": true,
	"(edit)": true, "(finish)": true, "(fixup)": true, "(initial)": true,
	"(merge)": true, "(no-ff)": true, "(pick)": true, "(reword)": true,
	"(squash)": true, "(start)": true,
}

// otherVerb is where an action this parser does not recognise lands.
const otherVerb = "other"

// verbOf is the action, and only the action. The second value is false when
// the action was not recognised, so that a run can say how often that
// happened.
//
// An allow-list of the verbs git writes, plus a parenthesised qualifier when
// one follows a verb that is on the list: "commit", "commit (amend)",
// "rebase (finish)", "checkout", "pull". Anything else becomes otherVerb.
//
// An allow-list rather than "the first word", because a reflog message is not
// always written by git. `git update-ref -m`, GIT_REFLOG_ACTION and a hook can
// each put arbitrary text there, and `git stash` already does: it writes
// "On <branch>: <message>" and "WIP on <branch>: …", whose first words are
// "On" and "WIP". Taking the first word would therefore put the first word of
// something a person typed into a column, which is the boundary this project
// does not cross — a commit message carries names and ticket numbers. The
// qualifier is on a list for the same reason: "commit (<anything>)" would
// otherwise walk straight through the verb check.
//
// Same shape, and the same reasoning, as the allow-list the browser source
// takes a path segment from: a deny-list there had to be extended three times,
// and getting an allow-list wrong costs a grouping rather than a leak.
func verbOf(msg string) (string, bool) {
	action, _, _ := strings.Cut(msg, ": ")
	fields := strings.Fields(action)
	if len(fields) == 0 {
		// No message at all, which `git update-ref` writes when it is given
		// none. otherVerb rather than "" so that the column says the same
		// thing the note about it says, and so that "no action recorded" is
		// not spelled the same way as "this source set no subtype".
		return otherVerb, false
	}
	verb := strings.ToLower(fields[0])
	if !reflogVerbs[verb] {
		return otherVerb, false
	}
	// The qualifier does not always sit directly after the verb: older git
	// writes "rebase -i (finish)". Looked for in the next two fields, and
	// still only accepted off the list.
	for _, f := range fields[1:min(len(fields), 3)] {
		if reflogQualifiers[strings.ToLower(f)] {
			verb += " " + strings.ToLower(f)
			break
		}
	}
	return text.Truncate(text.Printable(verb), maxVerb), true
}

// refName turns the path of a log file into the branch a person would say,
// and says how much that name is worth when two refs recorded one moment.
func refName(rel string) (string, int) {
	name, rank := rel, rankOther
	switch {
	case rel == "HEAD":
		rank = rankHEAD
	case strings.HasPrefix(rel, "refs/heads/"):
		name, rank = strings.TrimPrefix(rel, "refs/heads/"), rankBranch
	case strings.HasPrefix(rel, "refs/remotes/"):
		name, rank = strings.TrimPrefix(rel, "refs/remotes/"), rankRemote
	case strings.HasPrefix(rel, "refs/"):
		name = strings.TrimPrefix(rel, "refs/")
	}
	return text.Truncate(text.Printable(name), maxRef), rank
}

// collapse keeps one entry per moment.
//
// A single `git commit` writes the same second and the same resulting sha into
// `logs/HEAD` and into `logs/refs/heads/<branch>`, and a `git pull` adds a
// third copy under `logs/refs/remotes/`. They are one action. Keeping all
// three would be the failure this project cares most about — quietly more
// than there was — so the ranked copy wins and the others are counted.
//
// The order is total and involves no clock and no map iteration, so the same
// repository collapses the same way twice.
func collapse(entries []entry) ([]entry, int) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		switch {
		case !a.At.Equal(b.At):
			return a.At.Before(b.At)
		case a.New != b.New:
			return a.New < b.New
		case a.Rank != b.Rank:
			return a.Rank < b.Rank
		default:
			return a.Ref < b.Ref
		}
	})

	out, collapsed := make([]entry, 0, len(entries)), 0
	for i, e := range entries {
		if i > 0 && e.At.Equal(entries[i-1].At) && e.New == entries[i-1].New {
			collapsed++
			continue
		}
		out = append(out, e)
	}
	return out, collapsed
}
