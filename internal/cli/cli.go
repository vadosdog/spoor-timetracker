// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package cli is the command line surface of spoor.
package cli

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/paths"
	"github.com/vadosdog/spoor-timetracker/internal/source/claudecode"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Version is stamped at build time.
var Version = "dev"

const usage = `spoor — reconstructs the working day from traces already on disk.

Usage:
  spoor ingest [flags]   read sources and store what they show
  spoor count  [flags]   how many events the database holds for a date range
  spoor version          print the version

Run "spoor <command> -h" for the flags of a command.
spoor never uses the network.
`

// Run executes one command. It returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	var err error
	switch args[0] {
	case "ingest":
		err = runIngest(args[1:], stdout)
	case "count":
		err = runCount(args[1:], stdout)
	case "version":
		fmt.Fprintln(stdout, Version)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "spoor: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}

	if err != nil {
		fmt.Fprintf(stderr, "spoor: %v\n", err)
		return 1
	}
	return 0
}

func runIngest(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dbPath := fs.String("db", "", "database file (default: XDG data dir)")
	srcDir := fs.String("claude-dir", "", "Claude Code projects directory (default: ~/.claude/projects)")
	quiet := fs.Bool("quiet", false, "print nothing unless something went wrong")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root := *srcDir
	if root == "" {
		var err error
		if root, err = paths.ClaudeProjectsDir(); err != nil {
			return err
		}
	}

	st, closeDB, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer closeDB()

	rep, err := claudecode.Ingest(st, root)
	if err != nil {
		return err
	}

	total, err := st.TotalEvents()
	if err != nil {
		return err
	}

	if !*quiet {
		fmt.Fprintf(stdout, "claude-code: %d files (%d read, %d unchanged), %d lines\n",
			rep.FilesSeen, rep.FilesRead, rep.FilesSkipped, rep.LinesRead)
		fmt.Fprintf(stdout, "events: %d found, %d new, %d session-state lines skipped\n",
			rep.Events, rep.NewEvents, rep.SkippedState)
		if rep.Malformed > 0 || rep.Oversized > 0 {
			fmt.Fprintf(stdout, "dropped lines: %d malformed, %d over the size limit\n",
				rep.Malformed, rep.Oversized)
		}
		fmt.Fprintf(stdout, "database: %d events total\n", total)
	}
	for _, e := range rep.Errors {
		fmt.Fprintf(stdout, "warning: %s\n", e)
	}
	return nil
}

func runCount(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("count", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dbPath := fs.String("db", "", "database file (default: XDG data dir)")
	from := fs.String("from", "", "first local day, YYYY-MM-DD (default: 6 days before --to)")
	to := fs.String("to", "", "last local day, inclusive, YYYY-MM-DD (default: today)")
	source := fs.String("source", "", "restrict to one source, e.g. claude-code")
	if err := fs.Parse(args); err != nil {
		return err
	}

	last, err := parseDay(*to, time.Now())
	if err != nil {
		return fmt.Errorf("--to: %w", err)
	}
	first, err := parseDay(*from, last.AddDate(0, 0, -6))
	if err != nil {
		return fmt.Errorf("--from: %w", err)
	}
	if first.After(last) {
		return fmt.Errorf("--from %s is after --to %s", first.Format(time.DateOnly), last.Format(time.DateOnly))
	}

	st, closeDB, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer closeDB()

	// The range is inclusive of the last day, so the upper bound is midnight
	// of the day after it, in local time.
	n, err := st.CountEvents(*source, first, last.AddDate(0, 0, 1))
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "%d events from %s to %s\n",
		n, first.Format(time.DateOnly), last.Format(time.DateOnly))
	return nil
}

// parseDay reads a YYYY-MM-DD day in the machine's local zone, falling back to
// the local midnight of def when the flag was not given.
func parseDay(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return time.Date(def.Year(), def.Month(), def.Day(), 0, 0, 0, 0, time.Local), nil
	}
	t, err := time.ParseInLocation(time.DateOnly, s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("want YYYY-MM-DD, got %q", s)
	}
	return t, nil
}

func openStore(dbPath string) (*store.Store, func(), error) {
	if dbPath == "" {
		var err error
		if dbPath, err = paths.DBPath(); err != nil {
			return nil, nil, err
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return st, func() { _ = st.Close() }, nil
}
