// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package git reads the reflog — git's own journal of what moved and when —
// as a source of activity.
//
// It is the last source and the smallest by hours, on purpose. A commit is a
// point in time, not an interval: nothing about it says how long the work
// before it took, so this source is never allowed to invent time. What it is
// here for is names. A block of work whose only other traces are browsing can
// be left unnamed because no rule covers the hosts in it; a reflog entry in
// the middle of that block carries a directory and a branch, and both are
// things the dictionary already knows how to read.
//
// Four things shape the package:
//
//   - The reflog, and nothing else. Every commit made on this machine is in
//     it — "commit:", "commit (amend):", "commit (initial):" — together with
//     every checkout, rebase, pull and reset. `git log` is a list of objects
//     rather than a journal of actions: the author date of a commit somebody
//     else pushed says when *they* worked, and a rebase rewrites dates
//     wholesale. Reading the log would put other people's machines on this
//     machine's timeline.
//   - No subprocess and no library. `.git/logs/**` is a plain text file with
//     one line per action, as stable a format as JSONL, and reading it needs
//     neither a `git` binary on PATH nor a packfile parser.
//   - Metadata only, and here that bites harder than anywhere else: the text
//     after the first ": " in a reflog message is a commit message, which is
//     content. It is never read past the verb. That rule also keeps
//     credentials out — "clone: from https://user:token@host/x" puts one in
//     the part this package throws away.
//   - Append only. A reflog truncates itself, a rebase makes a commit
//     unreachable and a deleted branch takes its whole log with it. What was
//     imported outlives all three, which is why nothing here ever deletes.
package git

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vadosdog/spoor-timetracker/internal/text"
)

// SourceName is what ingested events are tagged with.
const SourceName = "git"

// Repo is one repository to read, as the config names it.
type Repo struct {
	// ID names the repository and is part of the identity of every event read
	// from it, exactly as a calendar's id is. Two repositories must not share
	// one: the second one's entries would collide on the unique index and be
	// dropped with no error and no warning.
	ID string
	// Path is the working tree, as written in the config with "~" already
	// expanded. It becomes the event's cwd, which is what the dictionary's
	// path rules match on — so a rule somebody already wrote for a checkout
	// covers that checkout's reflog for free.
	Path string
}

// verifiedOS is where this source has actually been run. It is the
// declaration rule 7 asks for, and nothing else: see Supported.
var verifiedOS = map[string]bool{
	"linux":   true,
	"darwin":  true,
	"windows": true,
}

// Supported reports whether the git source runs on this system at all, and
// the answer is everywhere.
//
// Unlike the browser, this source knows no paths of its own — every one of
// them is written in the config — so there is nothing here that could be
// wrong on another system: it opens the files it was pointed at. Returning
// false anywhere else would mean a repository somebody wrote in the config by
// hand is never read, with no warning, no counter and a successful exit —
// which is the worst failure this project has, and it would happen on exactly
// the systems nobody checked.
//
// What has and has not been run is a statement in the documentation and in
// verifiedOS, not a switch that silences a setting.
func Supported() bool { return true }

// Verified reports whether this system is one the source has been run on.
func Verified() bool { return verifiedOS[runtime.GOOS] }

// gitDir finds the directory holding the repository's own files.
//
// Three shapes, because all three are ordinary: a checkout with a `.git`
// directory, a worktree or submodule whose `.git` is a file pointing
// elsewhere, and a bare repository which is the directory itself.
func gitDir(path string) (string, error) {
	dot := filepath.Join(path, ".git")
	info, err := os.Stat(dot)
	switch {
	case err == nil && info.IsDir():
		return dot, nil
	case err == nil && info.Mode().IsRegular():
		return followGitFile(path, dot)
	}

	// A bare repository has no working tree and no `.git` at all. Recognised
	// by what it does have, rather than by the directory being named "*.git",
	// which is a convention and not a rule.
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		if _, err := os.Stat(filepath.Join(path, "refs")); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: not a git repository — no .git here", path)
}

// maxGitFile caps the `.git` pointer file. It holds one line; anything larger
// is not one.
const maxGitFile = 4 << 10

func followGitFile(path, dot string) (string, error) {
	f, err := os.Open(dot)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, maxGitFile)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
		if !ok {
			continue
		}
		target := strings.TrimSpace(rest)
		if target == "" {
			break
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(path, target)
		}
		return filepath.Clean(target), nil
	}
	// Sanitised, because this is a file's contents on their way to a terminal.
	return "", fmt.Errorf("%s: a .git file that does not say gitdir", text.Printable(dot))
}
