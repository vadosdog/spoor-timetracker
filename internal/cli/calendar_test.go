// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The calendar is the one source that can leave the machine. These tests are
// mostly about it not doing so.

// unreachable is a URL that would fail loudly if anything ever fetched it.
// TEST-NET-1 (RFC 5737) is reserved and routed nowhere.
const unreachable = "https://192.0.2.1/ical/private-nothing/basic.ics"

// icsFile writes a calendar holding one meeting at local noon on the day
// given, and returns its path.
func icsFile(t *testing.T, dir string, day time.Time, minutes int) string {
	t.Helper()
	noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.Local)
	end := noon.Add(time.Duration(minutes) * time.Minute)
	path := filepath.Join(dir, "feed.ics")
	body := fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n"+
		"UID:meeting-1@example.invalid\r\nSUMMARY:Planning\r\n"+
		"DTSTART:%s\r\nDTEND:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		noon.UTC().Format("20060102T150405Z"), end.UTC().Format("20060102T150405Z"))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The promise, checked rather than asserted in prose: with no config there is
// no calendar, so there is nothing to go out for. If this ever stops being
// true the test fails by hanging or by erroring on an unreachable address,
// which is the point.
func TestWithoutAConfigTheCalendarDoesNothing(t *testing.T) {
	dir := t.TempDir()
	out := ingest(t, filepath.Join(dir, "spoor.db"), t.TempDir())
	if strings.Contains(out, "calendar") {
		t.Errorf("an unconfigured calendar reported for itself:\n%s", out)
	}
}

// enabled is false by default, and a calendar that is configured but not
// switched on is not read. The address here is unroutable: reaching it would
// take the test's whole timeout, and reading it would be the bug.
func TestCalendarIsOffUnlessEnabled(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "url")
	if err := os.WriteFile(secret, []byte(unreachable), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      url_file: "+secret+"\n")

	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"ingest", "--db", filepath.Join(dir, "spoor.db"),
			"--claude-dir", t.TempDir(), "--config", cfg, "--no-browser"}, &out, &errOut)
	}()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exited %d: %s%s", code, out.String(), errOut.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("ingest went to the network for a calendar that is not enabled")
	}
	// And it says so, rather than leaving somebody to wonder why their
	// meetings are missing.
	if !strings.Contains(out.String(), "enabled is false") {
		t.Errorf("nothing said the calendar was configured but off:\n%s", out.String())
	}
}

// --no-calendar wins over the config. This is the switch the usage text
// promises, so it has to be the one that cannot be overridden from a file.
func TestNoCalendarBeatsTheConfig(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "url")
	if err := os.WriteFile(secret, []byte(unreachable), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir,
		"calendar:\n  enabled: true\n  sources:\n    - id: work\n      url_file: "+secret+"\n")

	done := make(chan string, 1)
	go func() {
		var out, errOut bytes.Buffer
		Run([]string{"ingest", "--db", filepath.Join(dir, "spoor.db"),
			"--claude-dir", t.TempDir(), "--config", cfg,
			"--no-browser", "--no-calendar"}, &out, &errOut)
		done <- out.String() + errOut.String()
	}()
	select {
	case out := <-done:
		if strings.Contains(out, "calendar:") {
			t.Errorf("--no-calendar still ran the source:\n%s", out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("--no-calendar went to the network anyway")
	}
}

// A file on disk needs no switch, because it goes nowhere. This is the way in
// for anybody who does not want the network at all.
func TestCalendarFromAFileNeedsNoNetwork(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "spoor.db")
	feed := icsFile(t, dir, time.Now().AddDate(0, 0, -1), 60)
	cfg := writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      file: "+feed+"\n")

	out := run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(),
		"--config", cfg, "--no-browser")
	if !strings.Contains(out, "1 meetings") && !strings.Contains(out, "1 new") {
		t.Errorf("the meeting was not imported:\n%s", out)
	}

	// And the second run inserts nothing: the identity is stable across runs.
	again := run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(),
		"--config", cfg, "--no-browser")
	if !strings.Contains(again, "0 new") {
		t.Errorf("a second run inserted rows:\n%s", again)
	}
}

// Two calendars sharing an id would collide on the unique index and the
// second one's meetings would vanish with no error at all. That is the exact
// failure this project has already paid for once, so it is refused up front.
func TestARepeatedCalendarIdIsRefused(t *testing.T) {
	dir := t.TempDir()
	feed := icsFile(t, dir, time.Now(), 30)
	cfg := writeConfig(t, dir,
		"calendar:\n  sources:\n    - id: work\n      file: "+feed+
			"\n    - id: work\n      file: "+feed+"\n")

	var out, errOut bytes.Buffer
	code := Run([]string{"ingest", "--db", filepath.Join(dir, "spoor.db"),
		"--claude-dir", t.TempDir(), "--config", cfg, "--no-browser"}, &out, &errOut)
	if code == 0 {
		t.Fatalf("a repeated id exited 0, which reads as success:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "silently drop") {
		t.Errorf("the message does not say what would go wrong: %s", errOut.String())
	}
	// And the rest of the run still happened: a config problem in one section
	// costs that section, not the work the other sources already did.
	if !strings.Contains(out.String(), "database:") {
		t.Errorf("the run was abandoned before its totals:\n%s", out.String())
	}
}

// A calendar id names a file when url_file is left out, so it has to be a
// name rather than a path.
func TestACalendarIdMustBeAPlainName(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfig(t, dir, "calendar:\n  enabled: true\n  sources:\n    - id: ../../etc/passwd\n")

	var out, errOut bytes.Buffer
	if code := Run([]string{"ingest", "--db", filepath.Join(dir, "spoor.db"),
		"--claude-dir", t.TempDir(), "--config", cfg, "--no-browser"}, &out, &errOut); code == 0 {
		t.Fatalf("a path was accepted as an id:\n%s", out.String())
	}
}

// A missing secret file is a plain error naming the path. The path is not a
// secret; the one line inside it is, and no message may quote that.
func TestAMissingSecretFileIsReportedByPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-there")
	cfg := writeConfig(t, dir,
		"calendar:\n  enabled: true\n  sources:\n    - id: work\n      url_file: "+missing+"\n")

	out, _ := runBoth(t, "ingest", "--db", filepath.Join(dir, "spoor.db"),
		"--claude-dir", t.TempDir(), "--config", cfg, "--no-browser")
	if !strings.Contains(out, missing) {
		t.Errorf("the warning does not name the missing file:\n%s", out)
	}
}

// count knows the third source by name, so that "0 events" cannot mean "you
// spelled it wrong".
func TestCountKnowsTheCalendar(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "spoor.db")
	feed := icsFile(t, dir, time.Now(), 60)
	cfg := writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      file: "+feed+"\n")
	run(t, "ingest", "--db", db, "--claude-dir", t.TempDir(), "--config", cfg, "--no-browser")

	out := run(t, "count", "--db", db, "--source", "calendar")
	if !strings.Contains(out, "1 events") {
		t.Errorf("count --source calendar said %q", strings.TrimSpace(out))
	}

	var o, e bytes.Buffer
	if code := Run([]string{"count", "--db", db, "--source", "calender"}, &o, &e); code == 0 {
		t.Error("a misspelled source was accepted")
	}
}

// add-calendar exists so that nobody has to get a umask incantation right by
// hand. What it must guarantee is what the incantation was for.
func TestAddCalendarWritesAPrivateFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	var out bytes.Buffer
	if err := runAddCalendar([]string{"work"}, &out, strings.NewReader(unreachable+"\n")); err != nil {
		t.Fatalf("add-calendar: %v", err)
	}

	path := filepath.Join(dir, "spoor", "calendars", "work")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no file was written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode is %04o, want 0600: the URL reads the whole calendar", perm)
	}
	if perm := dirPerm(t, filepath.Dir(path)); perm != 0o700 {
		t.Errorf("directory mode is %04o, want 0700", perm)
	}

	// It is the URL and nothing else, so that reading it needs no parser.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != unreachable {
		t.Errorf("file holds %q", strings.TrimSpace(string(body)))
	}

	// And the URL is never echoed: the whole point of not taking it as an
	// argument is that it does not end up on a screen or in a scrollback.
	if strings.Contains(out.String(), unreachable) {
		t.Errorf("the URL was printed back:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "enabled: true") {
		t.Errorf("nothing told the user what to add to the config:\n%s", out.String())
	}
}

func dirPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// Overwriting is refused: the old URL is the only copy of a credential that
// cannot be recovered, and a second run is more likely a mistake than a
// rotation.
func TestAddCalendarWillNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	var out bytes.Buffer
	if err := runAddCalendar([]string{"work"}, &out, strings.NewReader(unreachable+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := runAddCalendar([]string{"work"}, &out, strings.NewReader(unreachable+"\n")); err == nil {
		t.Error("a second run overwrote the file")
	}
}

// A plain http address is refused before it is stored, so that a typo is a
// message now rather than a failed import later.
func TestAddCalendarRefusesPlainHTTP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	var out bytes.Buffer
	err := runAddCalendar([]string{"work"}, &out, strings.NewReader("http://example.invalid/x.ics\n"))
	if err == nil {
		t.Fatal("an http URL was stored")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "spoor", "calendars", "work")); statErr == nil {
		t.Error("the file was written anyway")
	}
}

// The id names a file, so it has to be a name.
func TestAddCalendarRefusesAPathAsAnId(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	var out bytes.Buffer
	if err := runAddCalendar([]string{"../escape"}, &out, strings.NewReader(unreachable+"\n")); err == nil {
		t.Error("a path was accepted as an id")
	}
}

// "~" in a config path. The README's own example used one, and without
// expansion the file it names is never found: the setting parses, looks right
// and does nothing, which is the failure this project ranks worst.
func TestTildeInCalendarPathsIsExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()

	feed := icsFile(t, home, time.Now().AddDate(0, 0, -1), 60)
	rel, err := filepath.Rel(home, feed)
	if err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      file: ~/"+rel+"\n")

	out, _ := runBoth(t, "ingest", "--db", filepath.Join(dir, "spoor.db"),
		"--claude-dir", t.TempDir(), "--config", cfg, "--no-browser")
	if strings.Contains(out, "no such file") {
		t.Fatalf("a ~ path was not expanded:\n%s", out)
	}
	if !strings.Contains(out, "1 new") {
		t.Errorf("the meeting was not imported:\n%s", out)
	}
}

// The same for the file holding the URL, which is the path the README tells
// people to write and the one where failing quietly costs them their meetings.
func TestTildeInURLFileIsExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(home, "url"), []byte(unreachable), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, dir, "calendar:\n  sources:\n    - id: work\n      url_file: ~/url\n")

	// enabled is false, so nothing is fetched; what is checked is that the
	// path resolves rather than being reported as missing.
	out, _ := runBoth(t, "ingest", "--db", filepath.Join(dir, "spoor.db"),
		"--claude-dir", t.TempDir(), "--config", cfg, "--no-browser")
	if strings.Contains(out, "~/url") {
		t.Errorf("the path was used unexpanded:\n%s", out)
	}
}
