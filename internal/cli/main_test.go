// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package cli

import (
	"fmt"
	"os"
	"testing"
)

// TestMain puts the whole package's tests in an empty home directory.
//
// Every command here falls back to a real location when a flag is left out —
// ~/.claude/projects, the XDG config and data directories — which is correct
// for a person running the tool and wrong for a test. One forgotten
// --claude-dir and the suite reads the author's own sessions: it passes on
// that machine, fails everywhere else, and puts somebody's real data through
// code under test. Hard limit 1 of this project says tests run on synthetic
// fixtures and never on a dump of real sessions, and a rule that depends on
// remembering a flag is a rule that has already been broken once.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "spoor-test-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, v := range map[string]string{
		"HOME":              home,
		"XDG_CONFIG_HOME":   home + "/config",
		"XDG_DATA_HOME":     home + "/data",
		"XDG_CACHE_HOME":    home + "/cache",
		"CLAUDE_CONFIG_DIR": home + "/claude",
		"SPOOR_CONFIG":      "",
		// SPOOR_DB is checked before XDG_DATA_HOME and overrides it outright,
		// so a developer who exports it would have any test that omits --db
		// writing into their real database.
		"SPOOR_DB": home + "/data/spoor.db",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
