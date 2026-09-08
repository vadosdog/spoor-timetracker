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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/paths"
	"github.com/vadosdog/spoor-timetracker/internal/source/browser"
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

Sources: claude-code (session logs), browser (Chrome history; Firefox
untested — see README).

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

	// "spoor ingest -h" is somebody asking a question, not a failed run. The
	// flag package has already printed the flags by the time it hands this
	// back; printing an error after them and exiting non-zero would make the
	// answer look like a fault.
	if errors.Is(err, flag.ErrHelp) {
		return 0
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
	cfgPath := fs.String("config", "", "config file (default: XDG config dir)")
	srcDir := fs.String("claude-dir", "", "Claude Code projects directory (default: ~/.claude/projects)")
	noClaude := fs.Bool("no-claude-code", false, "skip the Claude Code source")
	noBrowser := fs.Bool("no-browser", false, "skip the browser source")
	var history repeatedPath
	fs.Var(&history, "browser-history", "read this browser history database instead of the ones found automatically; repeatable")
	quiet := fs.Bool("quiet", false, "print nothing unless something went wrong")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}

	st, closeDB, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer closeDB()

	var warnings []string
	if !*noClaude {
		rep, err := ingestClaudeCode(st, *srcDir, stdout, *quiet)
		if err != nil {
			return err
		}
		warnings = append(warnings, rep...)
	}
	if !*noBrowser {
		rep, err := ingestBrowser(st, cfg, history, stdout, *quiet)
		if err != nil {
			return err
		}
		warnings = append(warnings, rep...)
	}

	total, err := st.TotalEvents()
	if err != nil {
		return err
	}
	if !*quiet {
		fmt.Fprintf(stdout, "database: %d events total\n", total)
	}
	for _, w := range warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	return nil
}

func ingestClaudeCode(st *store.Store, root string, stdout io.Writer, quiet bool) ([]string, error) {
	if root == "" {
		var err error
		if root, err = paths.ClaudeProjectsDir(); err != nil {
			return nil, err
		}
	}

	rep, err := claudecode.Ingest(st, root)
	if err != nil {
		return nil, err
	}
	// A source that found nothing to read says nothing: on a machine without
	// Claude Code the tool is not broken, it is just doing the other half.
	if rep.FilesSeen == 0 && len(rep.Errors) == 0 {
		return nil, nil
	}
	if !quiet {
		fmt.Fprintf(stdout, "claude-code: %d files (%d read, %d unchanged), %d lines\n",
			rep.FilesSeen, rep.FilesRead, rep.FilesSkipped, rep.LinesRead)
		fmt.Fprintf(stdout, "  events: %d found, %d new, %d session-state lines skipped\n",
			rep.Events, rep.NewEvents, rep.SkippedState)
		if rep.Malformed > 0 || rep.Oversized > 0 {
			fmt.Fprintf(stdout, "  dropped lines: %d malformed, %d over the size limit\n",
				rep.Malformed, rep.Oversized)
		}
	}
	return rep.Errors, nil
}

// ingestBrowser runs the browser source. On a system the source does not
// support it prints nothing and does nothing, which is the rule for every
// source: declare where you work, and stay out of the way elsewhere.
//
// Naming history files on the command line replaces the search, the way
// --claude-dir replaces the default projects directory. The same list in the
// config file adds to it instead: that one describes the machine — a browser
// installed somewhere unusual — rather than overriding it for one run.
func ingestBrowser(st *store.Store, cfg config.Config, named []string, stdout io.Writer, quiet bool) ([]string, error) {
	if !browser.Supported() {
		return nil, nil
	}

	// Named here rather than "paths": that is the name of an imported package.
	var profiles []browser.Profile
	histories := named
	if len(named) == 0 {
		profiles = browser.Discover()
		histories = cfg.Browser.History
	}

	// A path the user typed is held to a higher standard than one spoor found
	// for itself: a discovered profile that is not there means the browser is
	// not installed, but a configured one that is not there is a typo, and a
	// typo in this list means that browser is never imported and nothing ever
	// says so.
	var warnings []string
	for _, path := range histories {
		p, ok := browser.ProfileAt(path)
		if !ok {
			warnings = append(warnings, fmt.Sprintf(
				"%s: not a browser history file — expected one named %q or %q",
				path, "History", "places.sqlite"))
			continue
		}
		if _, err := os.Stat(p.Path); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v — not read", p.Path, err))
			continue
		}
		profiles = append(profiles, p)
	}

	ignore, problems := browser.NewIgnore(cfg.Browser.Ignore)
	for _, p := range problems {
		warnings = append(warnings, fmt.Sprintf("ignore entry %q %s", p.Entry, p.Reason))
	}

	if len(profiles) == 0 {
		return warnings, nil // no browser here: say nothing about it
	}

	rep, err := browser.Ingest(st, profiles, ignore)
	if err != nil {
		return nil, err
	}
	if !quiet {
		fmt.Fprintf(stdout, "browser: %d profiles (%d read, %d unchanged), %d visits\n",
			rep.Profiles, rep.ProfilesRead, rep.ProfilesSkipped, rep.Visits)
		fmt.Fprintf(stdout, "  events: %d found, %d new, %d ignored, %d not http(s)\n",
			rep.Events, rep.NewEvents, rep.Ignored, rep.NotWeb)
		if rep.Unparseable > 0 {
			fmt.Fprintf(stdout, "  dropped visits: %d with an unreadable address\n", rep.Unparseable)
		}
	}
	return append(warnings, rep.Errors...), nil
}

func loadConfig(path string) (config.Config, error) {
	if path == "" {
		var err error
		if path, err = paths.ConfigPath(); err != nil {
			return config.Config{}, err
		}
	}
	return config.Load(path)
}

// repeatedPath collects a flag that may be given more than once.
type repeatedPath []string

func (p *repeatedPath) String() string { return strings.Join(*p, ", ") }

func (p *repeatedPath) Set(v string) error {
	*p = append(*p, v)
	return nil
}

func runCount(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("count", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dbPath := fs.String("db", "", "database file (default: XDG data dir)")
	from := fs.String("from", "", "first local day, YYYY-MM-DD (default: 6 days before --to)")
	to := fs.String("to", "", "last local day, inclusive, YYYY-MM-DD (default: today)")
	source := fs.String("source", "", "restrict to one source: claude-code or browser")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// With two valid values, a silent "0 events" is a coin flip between "I
	// have none of that" and "I typed it wrong".
	switch *source {
	case "", claudecode.SourceName, browser.SourceName:
	default:
		return fmt.Errorf("--source: no such source %q; there are %s and %s",
			*source, claudecode.SourceName, browser.SourceName)
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
