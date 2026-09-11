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
	"time"
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

// The clustering threshold is a measurement, not a constant, so it lives
// here. A number nobody can change without rebuilding cannot be remeasured.
func TestReportThresholds(t *testing.T) {
	cfg, err := Load(write(t, "report:\n  cluster_gap: 12m\n  attention_window: 90s\n  count_background: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(cfg.Report.ClusterGap); got != 12*time.Minute {
		t.Errorf("cluster gap %s, want 12m", got)
	}
	if got := time.Duration(cfg.Report.AttentionWindow); got != 90*time.Second {
		t.Errorf("attention window %s, want 1m30s", got)
	}
	if !cfg.Report.CountBackground {
		t.Error("count_background was not read")
	}
}

// An absent section means the defaults, which is the state the tool ships in.
func TestReportDefaultsWhenNothingIsSet(t *testing.T) {
	cfg, err := Load(write(t, "browser:\n  ignore:\n    - videos.example\n"))
	if err != nil {
		t.Fatal(err)
	}
	// Absent means zero here, and zero is what report.Options turns into the
	// default. The config layer does not know the default and must not: two
	// places knowing it is how they come to disagree.
	if got := time.Duration(cfg.Report.ClusterGap); got != 0 {
		t.Errorf("cluster gap %s, want nothing at all", got)
	}
	if cfg.Report.CountBackground {
		t.Error("count_background defaulted to on")
	}
}

// A duration that is not one is an error naming the line, not a silent zero
// that would put the threshold back to its default behind your back.
func TestBadDurationIsAnError(t *testing.T) {
	for _, body := range []string{
		"report:\n  cluster_gap: soon\n",
		"report:\n  cluster_gap: 600\n",
		"report:\n  cluster_gap: -5m\n",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%q was accepted", body)
		}
	}
}

func TestAttributionSection(t *testing.T) {
	cfg, err := Load(write(t, `
attribution:
  fallback: none
  never:
    - search.example.com
  subjects:
    - titles: '\b([A-Z]+-\d+)\b'
  projects:
    - name: widget
      work: true
      paths: ~/src/widget
      keys: [widget.example.com, dev.example.com:3000/admin]
      branches: '^widget/'
      titles: 'Widget Service'
      subjects:
        - name: episode 14
          branches: 'ep-14'
`))
	if err != nil {
		t.Fatalf("attribution: %v", err)
	}
	a := cfg.Attribution
	if a.Fallback != FallbackNone {
		t.Errorf("Fallback = %q, want %q", a.Fallback, FallbackNone)
	}
	if !reflect.DeepEqual(a.Never.Keys.Values(), []string{"search.example.com"}) {
		t.Errorf("Never.Keys = %v", a.Never)
	}
	if len(a.Projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(a.Projects))
	}
	p := a.Projects[0]
	if p.Work == nil || !*p.Work {
		t.Errorf("Work = %v, want true", p.Work)
	}
	if !reflect.DeepEqual(p.Keys.Values(), []string{"widget.example.com", "dev.example.com:3000/admin"}) {
		t.Errorf("Keys = %v", p.Keys)
	}
	if len(p.Branches) != 1 || !p.Branches[0].MatchString("widget/thing") {
		t.Errorf("Branches = %v", p.Branches)
	}
	if len(p.Subjects) != 1 || p.Subjects[0].Name != "episode 14" {
		t.Errorf("Subjects = %+v", p.Subjects)
	}
	if len(a.Subjects) != 1 || a.Subjects[0].Titles[0].NumSubexp() != 1 {
		t.Errorf("global subjects = %+v", a.Subjects)
	}
}

// Most rules have exactly one path or one key, and the file is meant to be
// twenty lines. Writing a list of one has to be optional.
func TestAListMayBeWrittenAsOneValue(t *testing.T) {
	cfg, err := Load(write(t, `
attribution:
  projects:
    - name: widget
      paths: /src/widget
      titles: 'Widget'
`))
	if err != nil {
		t.Fatalf("scalar list: %v", err)
	}
	p := cfg.Attribution.Projects[0]
	if !reflect.DeepEqual(p.Paths.Values(), []string{"/src/widget"}) {
		t.Errorf("Paths = %v, want one entry", p.Paths)
	}
	if len(p.Titles) != 1 {
		t.Errorf("Titles = %v, want one entry", p.Titles)
	}
}

// A broken expression is a config error naming its line, not a rule that
// silently matches nothing for the rest of the tool's life.
func TestBrokenRegexpIsAnError(t *testing.T) {
	_, err := Load(write(t, "attribution:\n  projects:\n    - name: x\n      titles: '([unclosed'\n"))
	if err == nil {
		t.Fatal("a broken regular expression was accepted")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error does not name the line: %v", err)
	}
}

// A misspelt fallback would otherwise mean the opposite of what somebody who
// bothered to write the key wanted.
func TestBadFallbackIsAnError(t *testing.T) {
	_, err := Load(write(t, "attribution:\n  fallback: cwd_basename\n"))
	if err == nil {
		t.Fatal("an unknown fallback was accepted")
	}
	if !strings.Contains(err.Error(), "cwd-basename") {
		t.Errorf("error does not say what is allowed: %v", err)
	}
}

// The section was a bare list of keys before paths existed, and every config
// written against that version still says so.
func TestNeverAcceptsBothShapes(t *testing.T) {
	flat, err := Load(write(t, "attribution:\n  never: [a.example.com, b.example.com]\n"))
	if err != nil {
		t.Fatalf("flat never: %v", err)
	}
	if !reflect.DeepEqual(flat.Attribution.Never.Keys.Values(), []string{"a.example.com", "b.example.com"}) {
		t.Errorf("a bare list is not read as keys: %+v", flat.Attribution.Never)
	}
	if len(flat.Attribution.Never.Paths) != 0 {
		t.Errorf("a bare list produced paths: %v", flat.Attribution.Never.Paths)
	}

	both, err := Load(write(t, "attribution:\n  never:\n    keys: a.example.com\n    paths: [~/scratch, /tmp/x]\n"))
	if err != nil {
		t.Fatalf("never with both lists: %v", err)
	}
	if !reflect.DeepEqual(both.Attribution.Never.Keys.Values(), []string{"a.example.com"}) {
		t.Errorf("Keys = %v", both.Attribution.Never.Keys)
	}
	if !reflect.DeepEqual(both.Attribution.Never.Paths.Values(), []string{"~/scratch", "/tmp/x"}) {
		t.Errorf("Paths = %v", both.Attribution.Never.Paths)
	}
}

// yaml.v3 does not carry KnownFields into a custom unmarshaler, so this file
// checks its own keys by hand. A typo here would otherwise be a list that
// silently does nothing — which is what the whole section exists to prevent.
func TestUnknownKeyUnderNeverIsAnError(t *testing.T) {
	_, err := Load(write(t, "attribution:\n  never:\n    keyz: [a.example.com]\n"))
	if err == nil {
		t.Fatal("a misspelt key under never was accepted")
	}
	if !strings.Contains(err.Error(), "keyz") || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error does not name the key and its line: %v", err)
	}
}

// A key with no value is an empty list, not a list of one empty string: the
// latter reaches the rules and warns, every run, about an entry nobody wrote.
// And a key given twice is an error here, because walking the mapping by hand
// takes that check away from yaml.v3.
func TestNeverRejectsWhatYamlWouldHaveCaught(t *testing.T) {
	empty, err := Load(write(t, "attribution:\n  never:\n    keys:\n    paths:\n"))
	if err != nil {
		t.Fatalf("never with empty lists: %v", err)
	}
	if n := empty.Attribution.Never; len(n.Keys) != 0 || len(n.Paths) != 0 {
		t.Errorf("an empty key produced entries: %+v", n)
	}

	_, err = Load(write(t, "attribution:\n  never:\n    keys: [a.example.com]\n    keys: [b.example.com]\n"))
	if err == nil {
		t.Fatal("a repeated key under never was accepted")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("error does not say the key is repeated: %v", err)
	}
}
