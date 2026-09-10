// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/paths"
)

// runAddCalendar stores one calendar's URL in the file that holds it.
//
// This exists because the alternative was a paragraph of shell in the README:
//
//	mkdir -p ~/.config/spoor/calendars && (umask 077; ... read -rs ...)
//
// Every part of that is load-bearing and every part of it is easy to get
// wrong. Forget the umask and the credential is world-readable; use `read`
// without -s and it is on the screen; pass it as an argument instead and it is
// in the shell history and in `ps` for every account on the machine. A command
// that is hard to run safely is a command people run unsafely.
//
// What it deliberately does not do is edit the config file. `spoor` has never
// written config.yaml and this is not the feature that should start: a program
// that rewrites the file you hand-edit will one day reformat it, drop a
// comment, or lose a line. It prints the three lines to paste instead.
func runAddCalendar(args []string, stdout io.Writer, stdin io.Reader) error {
	fs := flag.NewFlagSet("add-calendar", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprint(stdout, `Usage: spoor add-calendar <id>

Stores a calendar's private feed URL in ~/.config/spoor/calendars/<id>, with
permissions only you can read, and prints the config lines to add.

The URL is read from the terminal without echoing it. It is never taken as an
argument: anything on a command line is visible to every account on this
machine through /proc and lands in your shell history besides.

Treat the URL as a password — see "Network" in the README.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("add-calendar takes one argument: a short name for the calendar, such as \"work\"")
	}
	id := fs.Arg(0)

	// The same rule the config applies, and for the same reason: the id names
	// a file.
	if problems, _ := (config.Calendar{Sources: []config.CalendarSource{{ID: id}}}).Validate(); len(problems) > 0 {
		return fmt.Errorf("%s", problems[0].Reason)
	}

	path, err := paths.CalendarSecret(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists; remove it first if you mean to replace it", path)
	}

	url, err := readSecretly(stdout, stdin)
	if err != nil {
		return err
	}
	// Checked before it is written, so that a typo is a message now rather
	// than a failed import later. The value is never echoed back.
	if !strings.HasPrefix(strings.ToLower(url), "https://") {
		return errors.New("a calendar URL must start with https:// — that was not one")
	}

	// 0700 on the directory and 0600 on the file, set at creation rather than
	// afterwards: a chmod after the write leaves a window in which the
	// credential is readable.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, url); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote %s (only you can read it).\n\n", path)
	fmt.Fprintf(stdout, "Now add this to %s:\n\n", cfgPath)
	fmt.Fprintf(stdout, "calendar:\n  enabled: true\n  sources:\n    - id: %s\n", id)
	fmt.Fprint(stdout, "      # Your own addresses, so a meeting you declined can be told\n")
	fmt.Fprint(stdout, "      # from one somebody else declined. Optional.\n")
	fmt.Fprint(stdout, "      me: you@example.com\n\n")
	fmt.Fprint(stdout, "Then run `spoor ingest`. Until enabled is true, nothing is fetched.\n")
	return nil
}

// readSecretly asks for the URL without putting it on the screen.
//
// When stdin is not a terminal — a pipe, a test — there is nothing to switch
// off and the line is read plainly. That is not a downgrade of anything: a
// pipe was never echoing in the first place.
func readSecretly(stdout io.Writer, stdin io.Reader) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(stdout, "Paste the private iCalendar URL (it will not be shown): ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stdout)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 0, 4096), 1<<16)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", errors.New("no URL on standard input")
	}
	return strings.TrimSpace(sc.Text()), nil
}
