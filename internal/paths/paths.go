// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package paths resolves where spoor keeps its data and configuration.
//
// Everything follows the XDG base directory spec. Nothing machine dependent
// is ever compiled in: the repository contains no personal paths.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandHome turns a leading "~" into the home directory. Nothing else is
// expanded: a config that interpolated environment variables would mean one
// thing in a terminal and another under cron, and a config file has to mean
// the same in both.
//
// Every path a person types into the config goes through this. It used to
// serve the dictionary alone, and a calendar's url_file did not — so a
// documented "~/.config/spoor/calendars/work" parsed, looked right and never
// found the file. A path setting that is silently not expanded is the same
// failure as a rule that never fires.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%q starts with ~ and there is no home directory: %w", p, err)
	}
	return home + strings.TrimPrefix(p, "~"), nil
}

const appDir = "spoor"

// DataDir returns $XDG_DATA_HOME/spoor, falling back to ~/.local/share/spoor.
func DataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, appDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", appDir), nil
}

// ConfigDir returns $XDG_CONFIG_HOME/spoor, falling back to ~/.config/spoor.
func ConfigDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, appDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", appDir), nil
}

// DBPath returns the SQLite file spoor accumulates events in.
//
// SPOOR_DB overrides it outright, which is what the tests use so that no test
// ever touches the real database.
func DBPath() (string, error) {
	if p := os.Getenv("SPOOR_DB"); p != "" {
		return p, nil
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spoor.db"), nil
}

// ConfigPath returns the YAML file spoor reads its settings from.
//
// Nothing writes this file: it does not have to exist, and when it does not,
// every source runs on its defaults. SPOOR_CONFIG overrides it outright,
// which is what the tests use so that no test reads the real one.
func ConfigPath() (string, error) {
	if p := os.Getenv("SPOOR_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// CalendarSecret returns the file holding one calendar's URL, when the config
// does not name one itself: $XDG_CONFIG_HOME/spoor/calendars/<id>.
//
// A directory of one-line files rather than a section of the config, and a
// file per calendar rather than one file with names in it. The address of a
// private feed reads the whole calendar and never expires, so it is kept the
// way ssh keeps a key: on its own, with nothing beside it that could be quoted
// in a message, and rotatable one at a time.
//
// Nothing here creates the file. spoor never writes a secret it did not get.
func CalendarSecret(id string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "calendars", id), nil
}

// ClaudeProjectsDir returns the directory Claude Code writes session JSONL to.
//
// CLAUDE_CONFIG_DIR mirrors the variable Claude Code itself honours.
func ClaudeProjectsDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}
