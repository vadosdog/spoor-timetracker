// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Running without a config file is the normal state of the tool.
func TestMissingFileIsNotAnError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nothing-here.yaml"))
	if err != nil {
		t.Fatalf("missing config: %v", err)
	}
	if len(cfg.Browser.Ignore) != 0 || len(cfg.Browser.History) != 0 {
		t.Errorf("a missing file produced settings: %+v", cfg)
	}
}

func TestEmptyFileIsNotAnError(t *testing.T) {
	for _, body := range []string{"", "\n", "# nothing but a comment\n"} {
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("%q: %v", body, err)
		}
	}
}

func TestBrowserSection(t *testing.T) {
	cfg, err := Load(write(t, `
browser:
  ignore:
    - videos.example
    - social.example
  history:
    - /somewhere/Profile 1/History
`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"videos.example", "social.example"}; !reflect.DeepEqual(cfg.Browser.Ignore, want) {
		t.Errorf("ignore = %q, want %q", cfg.Browser.Ignore, want)
	}
	if want := []string{"/somewhere/Profile 1/History"}; !reflect.DeepEqual(cfg.Browser.History, want) {
		t.Errorf("history = %q, want %q", cfg.Browser.History, want)
	}
}

// A misspelled key in this file means an ignore list that is quietly not
// applied. That has to be an error the user sees, not a shrug.
func TestUnknownKeyIsAnError(t *testing.T) {
	_, err := Load(write(t, "browser:\n  ignored:\n    - videos.example\n"))
	if err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), "ignored") {
		t.Errorf("the error does not name the bad key: %v", err)
	}
}

func TestBrokenYAMLIsAnError(t *testing.T) {
	if _, err := Load(write(t, "browser: [unclosed\n")); err == nil {
		t.Fatal("broken YAML was accepted")
	}
}
