// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// newStore opens a throwaway database. Nothing in the test suite ever touches
// the real one: the path is a temp dir, never the XDG location.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "spoor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// session writes a synthetic session log: one user line and one assistant line
// per minute, plus the session-state lines a real file is full of.
func session(t *testing.T, dir, name, sessionID string, start time.Time, turns int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	for i := 0; i < turns; i++ {
		ts := start.Add(time.Duration(i) * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		fmt.Fprintf(f, `{"type":"user","uuid":"%s-u%d","parentUuid":null,"timestamp":"%s",`+
			`"sessionId":"%s","version":"2.1.219","cwd":"/home/u/projects/widget","gitBranch":"main",`+
			`"entrypoint":"cli","isSidechain":false,"userType":"external","message":{"content":"x"}}`+"\n",
			sessionID, i, ts, sessionID)
		fmt.Fprintf(f, `{"type":"assistant","uuid":"%s-a%d","parentUuid":"%s-u%d","timestamp":"%s",`+
			`"sessionId":"%s","version":"2.1.219","cwd":"/home/u/projects/widget","gitBranch":"main",`+
			`"entrypoint":"cli","isSidechain":false,"userType":"external",`+
			`"message":{"model":"claude-opus-5","content":[{"type":"text"}]}}`+"\n",
			sessionID, i, sessionID, i, ts, sessionID)
		// Session state: no timestamp, thousands of these in real data.
		fmt.Fprintf(f, `{"type":"ai-title","aiTitle":"t","sessionId":"%s"}`+"\n", sessionID)
	}
	return path
}

func TestIngestIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	session(t, filepath.Join(dir, "-home-u-projects-widget"), "s1.jsonl", "s1",
		time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC), 10)
	session(t, filepath.Join(dir, "-home-u-projects-other"), "s2.jsonl", "s2",
		time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC), 5)

	st := newStore(t)

	first, err := Ingest(st, dir)
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if first.Events != 30 || first.NewEvents != 30 {
		t.Fatalf("first run: events=%d new=%d, want 30/30", first.Events, first.NewEvents)
	}
	if first.SkippedState != 15 {
		t.Errorf("first run: skipped %d state lines, want 15", first.SkippedState)
	}
	if first.Malformed != 0 {
		t.Errorf("first run: %d malformed lines, want 0", first.Malformed)
	}

	afterFirst, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}

	second, err := Ingest(st, dir)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if second.NewEvents != 0 {
		t.Errorf("second run inserted %d events, want 0", second.NewEvents)
	}
	if second.FilesSkipped != 2 {
		t.Errorf("second run read %d unchanged files instead of skipping both", 2-second.FilesSkipped)
	}

	afterSecond, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst != afterSecond {
		t.Errorf("event count changed on a second run: %d then %d", afterFirst, afterSecond)
	}
}

// Claude Code deletes its own JSONL after 30 days. An event imported once
// has to outlive the file it came from, otherwise the database is a cache and
// no retrospective longer than a month is ever possible.
func TestEventsSurviveSourceFileDeletion(t *testing.T) {
	dir := t.TempDir()
	path := session(t, filepath.Join(dir, "-home-u-projects-widget"), "s1.jsonl", "s1",
		time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC), 4)

	st := newStore(t)
	if _, err := Ingest(st, dir); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	before, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if before != 8 {
		t.Fatalf("imported %d events, want 8", before)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	rep, err := Ingest(st, dir)
	if err != nil {
		t.Fatalf("ingest after deletion: %v", err)
	}
	if rep.FilesSeen != 0 {
		t.Errorf("still see %d files after deleting the only one", rep.FilesSeen)
	}

	after, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("deleting the source file took %d events with it (%d -> %d)",
			before-after, before, after)
	}

	// And the events are still individually there, not just counted.
	var n int
	err = st.DB().QueryRow(
		`SELECT count(*) FROM events WHERE external_id = ? OR external_id = ?`,
		"s1-u0", "s1-a3").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("found %d of the 2 named events after the file was deleted", n)
	}
}

func TestIngestReadsOnlyWhatWasAppended(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	path := session(t, sub, "s1.jsonl", "s1", time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC), 3)

	st := newStore(t)
	if _, err := Ingest(st, dir); err != nil {
		t.Fatal(err)
	}

	// Claude Code only ever appends. Make sure mtime moves: some filesystems
	// have coarse timestamps and the size change alone must not be relied on
	// by the test.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, `{"type":"user","uuid":"s1-u99","timestamp":"2026-08-25T10:00:00.000Z",`+
		`"sessionId":"s1","cwd":"/home/u/projects/widget","entrypoint":"cli"}`+"\n")
	f.Close()
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	rep, err := Ingest(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LinesRead != 1 {
		t.Errorf("re-read %d lines, want only the 1 appended", rep.LinesRead)
	}
	if rep.NewEvents != 1 {
		t.Errorf("inserted %d events, want 1", rep.NewEvents)
	}

	total, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
}

// A line that has not been terminated yet is a file being written to right
// now, not an event. It must not be consumed, and it must be picked up whole
// on the next run.
func TestIngestLeavesUnterminatedLineForNextRun(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "s1.jsonl")

	complete := `{"type":"user","uuid":"u1","timestamp":"2026-08-25T09:00:00.000Z","sessionId":"s1"}` + "\n"
	partial := `{"type":"user","uuid":"u2","timestamp":"2026-08-25T09:01:00.000Z"`
	if err := os.WriteFile(path, []byte(complete+partial), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newStore(t)
	rep, err := Ingest(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != 1 || rep.Malformed != 0 {
		t.Fatalf("events=%d malformed=%d, want 1/0", rep.Events, rep.Malformed)
	}

	rest := `,"sessionId":"s1"}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, rest)
	f.Close()
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	rep, err = Ingest(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != 1 || rep.NewEvents != 1 || rep.Malformed != 0 {
		t.Fatalf("second run: events=%d new=%d malformed=%d, want 1/1/0",
			rep.Events, rep.NewEvents, rep.Malformed)
	}
}

// Resuming a session copies earlier lines into the new session's file with the
// same uuid and a different sessionId. Measured: 570 uuids appear twice this
// way. They are one event, not two, and the dedup key has to say so.
func TestSameUUIDInTwoFilesIsOneEvent(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	line := func(sessionID string) string {
		return `{"type":"assistant","uuid":"shared-1","timestamp":"2026-08-25T09:00:00.000Z",` +
			`"sessionId":"` + sessionID + `","cwd":"/home/u/projects/widget","entrypoint":"cli",` +
			`"message":{"model":"claude-opus-5"}}` + "\n"
	}
	if err := os.WriteFile(filepath.Join(sub, "s1.jsonl"), []byte(line("s1")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "s2.jsonl"), []byte(line("s2")), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newStore(t)
	rep, err := Ingest(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != 2 {
		t.Fatalf("parsed %d lines as events, want 2", rep.Events)
	}
	if rep.NewEvents != 1 {
		t.Errorf("stored %d rows for one uuid, want 1", rep.NewEvents)
	}
}

// A path can stop holding the file it used to hold: a restored backup, a
// copied session, a rewrite. The saved read offset then points into content
// nobody has seen, and resuming from it would lose everything before it.
func TestIngestRereadsWhenTheFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "s1.jsonl")

	line := func(id, ts string) string {
		return `{"type":"user","uuid":"` + id + `","timestamp":"` + ts + `","sessionId":"s1",` +
			`"cwd":"/home/u/projects/widget","entrypoint":"cli"}` + "\n"
	}

	first := line("old-1", "2026-08-25T09:00:00.000Z") + line("old-2", "2026-08-25T09:01:00.000Z")
	if err := os.WriteFile(path, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newStore(t)
	if _, err := Ingest(st, dir); err != nil {
		t.Fatal(err)
	}

	// Same path, different content, and no smaller than before.
	second := line("new-1", "2026-08-26T09:00:00.000Z") +
		line("new-2", "2026-08-26T09:01:00.000Z") +
		line("new-3", "2026-08-26T09:02:00.000Z")
	if err := os.WriteFile(path, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	rep, err := Ingest(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewEvents != 3 {
		t.Errorf("picked up %d of the 3 events in the replacing file", rep.NewEvents)
	}

	total, err := st.TotalEvents()
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (2 kept from the old file, 3 from the new)", total)
	}
}

// The harder version of the same problem: the file keeps its first line and
// grows past its old size, but the middle was rolled back and rewritten. The
// saved offset then points into bytes nobody has read, and everything between
// the rollback point and that offset would be lost in silence.
func TestIngestRereadsWhenTheMiddleIsRewritten(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "s1.jsonl")

	line := func(id string, minute int) string {
		return fmt.Sprintf(`{"type":"user","uuid":"%s","timestamp":"2026-08-25T09:%02d:00.000Z",`+
			`"sessionId":"s1","cwd":"/home/u/projects/widget","entrypoint":"cli"}`+"\n", id, minute)
	}

	var original strings.Builder
	for i := 0; i < 20; i++ {
		original.WriteString(line(fmt.Sprintf("old-%d", i), i))
	}
	if err := os.WriteFile(path, []byte(original.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newStore(t)
	if _, err := Ingest(st, dir); err != nil {
		t.Fatal(err)
	}

	// Rolled back to the first five lines, then written past the old length
	// with different content. Same first line, larger file.
	var rolled strings.Builder
	for i := 0; i < 5; i++ {
		rolled.WriteString(line(fmt.Sprintf("old-%d", i), i))
	}
	for i := 0; i < 25; i++ {
		rolled.WriteString(line(fmt.Sprintf("new-%d", i), i))
	}
	if err := os.WriteFile(path, []byte(rolled.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	if _, err := Ingest(st, dir); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := st.DB().QueryRow(
		`SELECT count(*) FROM events WHERE external_id LIKE 'new-%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 25 {
		t.Errorf("stored %d of the 25 rewritten events, lost %d silently", n, 25-n)
	}
}

// One pathological line must not be able to allocate the whole file, and must
// not stop the lines around it from being imported.
func TestIngestSurvivesAnOverlongLine(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "-home-u-projects-widget")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	good := `{"type":"user","uuid":"u1","timestamp":"2026-08-25T09:00:00.000Z","sessionId":"s1"}` + "\n"
	huge := `{"type":"user","uuid":"u2","timestamp":"2026-08-25T09:01:00.000Z","pad":"` +
		strings.Repeat("x", maxLine) + `"}` + "\n"
	after := `{"type":"user","uuid":"u3","timestamp":"2026-08-25T09:02:00.000Z","sessionId":"s1"}` + "\n"

	if err := os.WriteFile(filepath.Join(sub, "s1.jsonl"), []byte(good+huge+after), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newStore(t)
	rep, err := Ingest(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Oversized != 1 {
		t.Errorf("Oversized = %d, want 1", rep.Oversized)
	}
	if rep.NewEvents != 2 {
		t.Errorf("imported %d events around the overlong line, want 2", rep.NewEvents)
	}
}

func TestIngestMissingDirectoryIsNotAnError(t *testing.T) {
	st := newStore(t)
	rep, err := Ingest(st, filepath.Join(t.TempDir(), "nothing-here"))
	if err != nil {
		t.Fatalf("missing source directory reported as an error: %v", err)
	}
	if rep.FilesSeen != 0 {
		t.Errorf("FilesSeen = %d, want 0", rep.FilesSeen)
	}
}
