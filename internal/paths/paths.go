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
	"os"
	"path/filepath"
)

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
