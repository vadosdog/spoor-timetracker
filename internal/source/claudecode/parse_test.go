// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package claudecode

import "testing"

// Every line below is synthetic. Shapes come from the format survey, values
// do not: no real session is ever committed to this repository.

func TestParseLineCore(t *testing.T) {
	line := []byte(`{"type":"assistant","uuid":"11111111-1111-1111-1111-111111111111",` +
		`"parentUuid":"00000000-0000-0000-0000-000000000000","timestamp":"2026-08-25T07:15:30.500Z",` +
		`"sessionId":"s-1","version":"2.1.219","cwd":"/home/u/projects/widget","gitBranch":"main",` +
		`"entrypoint":"claude-desktop","isSidechain":false,"userType":"external",` +
		`"message":{"model":"claude-opus-5","content":[{"type":"text"},{"type":"tool_use","name":"Read"}]}}`)

	ev, outcome := ParseLine(line)
	if outcome != Parsed {
		t.Fatalf("outcome = %v, want Parsed", outcome)
	}
	if ev.ExternalID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("ExternalID = %q", ev.ExternalID)
	}
	if ev.TS != "2026-08-25T07:15:30.500Z" {
		t.Errorf("TS = %q", ev.TS)
	}
	if ev.Project != "widget" {
		t.Errorf("Project = %q, want widget", ev.Project)
	}
	if ev.RawText != "claude-opus-5 Read" {
		t.Errorf("RawText = %q", ev.RawText)
	}
	if ev.SessionID != "s-1" || ev.Entrypoint != "claude-desktop" || ev.GitBranch != "main" {
		t.Errorf("core fields lost: %+v", ev)
	}
	if ev.DurationMS != nil {
		t.Errorf("DurationMS = %v, want nil for a point event", *ev.DurationMS)
	}
}

// The CLI writes sessionId and session_id side by side. Either spelling has to
// be enough on its own.
func TestParseLineSessionIDSpellings(t *testing.T) {
	cases := map[string]string{
		"camelCase only":  `{"type":"user","uuid":"u1","timestamp":"2026-08-25T07:00:00.000Z","sessionId":"s-camel"}`,
		"snake_case only": `{"type":"user","uuid":"u2","timestamp":"2026-08-25T07:00:00.000Z","session_id":"s-snake"}`,
		"both, camel wins": `{"type":"user","uuid":"u3","timestamp":"2026-08-25T07:00:00.000Z",` +
			`"sessionId":"s-camel","session_id":"s-snake"}`,
	}
	want := map[string]string{
		"camelCase only":   "s-camel",
		"snake_case only":  "s-snake",
		"both, camel wins": "s-camel",
	}

	for name, line := range cases {
		ev, outcome := ParseLine([]byte(line))
		if outcome != Parsed {
			t.Fatalf("%s: outcome = %v, want Parsed", name, outcome)
		}
		if ev.SessionID != want[name] {
			t.Errorf("%s: SessionID = %q, want %q", name, ev.SessionID, want[name])
		}
	}
}

// type "system" is two incompatible shapes under one name. Both must parse,
// and subtype is what tells them apart.
func TestParseLineSystemSubtypes(t *testing.T) {
	hooks := `{"type":"system","subtype":"stop_hook_summary","uuid":"h1",` +
		`"timestamp":"2026-08-25T08:00:00.000Z","sessionId":"s-1","cwd":"/home/u/projects/widget",` +
		`"entrypoint":"claude-desktop","hookCount":2,"stopReason":"end_turn","toolUseID":"t-9"}`
	compact := `{"type":"system","subtype":"turn_duration","uuid":"c1",` +
		`"timestamp":"2026-08-25T08:05:00.000Z","sessionId":"s-1","cwd":"/home/u/projects/widget",` +
		`"entrypoint":"cli","isMeta":true,"durationMs":42000,"messageCount":17}`

	ev, outcome := ParseLine([]byte(hooks))
	if outcome != Parsed || ev.Subtype != "stop_hook_summary" {
		t.Fatalf("hooks: outcome=%v subtype=%q", outcome, ev.Subtype)
	}
	if ev.DurationMS != nil {
		t.Errorf("hooks: DurationMS = %v, want nil", *ev.DurationMS)
	}

	ev, outcome = ParseLine([]byte(compact))
	if outcome != Parsed || ev.Subtype != "turn_duration" {
		t.Fatalf("compaction: outcome=%v subtype=%q", outcome, ev.Subtype)
	}
	if ev.DurationMS == nil || *ev.DurationMS != 42000 {
		t.Errorf("compaction: DurationMS = %v, want 42000", ev.DurationMS)
	}
}

// The writer is JavaScript. A duration arriving as a float must cost the
// duration at worst, never the whole event.
func TestParseLineToleratesFloatDuration(t *testing.T) {
	line := `{"type":"system","subtype":"turn_duration","uuid":"c1",` +
		`"timestamp":"2026-08-25T08:05:00.000Z","sessionId":"s-1","durationMs":42000.0}`

	ev, outcome := ParseLine([]byte(line))
	if outcome != Parsed {
		t.Fatalf("outcome = %v, want Parsed", outcome)
	}
	if ev.DurationMS == nil || *ev.DurationMS != 42000 {
		t.Errorf("DurationMS = %v, want 42000", ev.DurationMS)
	}
}

// Thousands of lines are session state, not events. Skipping them is normal
// operation and must never be reported as damage.
func TestParseLineSessionStateIsSkipped(t *testing.T) {
	lines := []string{
		`{"type":"last-prompt","lastPrompt":"...","leafUuid":"x","sessionId":"s-1"}`,
		`{"type":"ai-title","aiTitle":"...","sessionId":"s-1"}`,
		`{"type":"mode","mode":"default","sessionId":"s-1"}`,
		`{"type":"permission-mode","permissionMode":"plan","sessionId":"s-1"}`,
		`{"type":"file-history-snapshot","messageId":"m","snapshot":{},"isSnapshotUpdate":false}`,
		`{"type":"custom-title","sessionId":"s-1"}`,
		// Has a timestamp but no uuid: nothing stable to dedup it by.
		`{"type":"queue-operation","operation":"enqueue","sessionId":"s-1","timestamp":"2026-08-25T09:00:00.000Z"}`,
		``,
	}
	for _, line := range lines {
		if _, outcome := ParseLine([]byte(line)); outcome != SkippedState {
			t.Errorf("outcome = %v for %s, want SkippedState", outcome, line)
		}
	}
}

func TestParseLineMalformed(t *testing.T) {
	for _, line := range []string{
		`{"type":"user","uuid":"u1",`,
		`not json at all`,
		`{"type":"user","uuid":"u1","timestamp":"yesterday"}`,
	} {
		if _, outcome := ParseLine([]byte(line)); outcome != Malformed {
			t.Errorf("outcome for %q is not Malformed", line)
		}
	}
}

// The field set is decided by the client, not by the version: a record that
// carries only the core has to parse exactly as well as a rich one.
func TestParseLineOptionalFieldsAreOptional(t *testing.T) {
	bare := `{"type":"user","uuid":"u1","parentUuid":null,"timestamp":"2026-08-25T10:00:00.000Z",` +
		`"sessionId":"s-1","version":"2.1.226","cwd":"/home/u/projects/widget","gitBranch":"",` +
		`"entrypoint":"sdk-py","isSidechain":false,"userType":"external","message":{"content":"hello"}}`

	ev, outcome := ParseLine([]byte(bare))
	if outcome != Parsed {
		t.Fatalf("outcome = %v, want Parsed", outcome)
	}
	if ev.GitBranch != "" || ev.Entrypoint != "sdk-py" {
		t.Errorf("unexpected: %+v", ev)
	}
	// content is a plain string here, not an array of blocks.
	if ev.RawText != "prompt" {
		t.Errorf("RawText = %q, want prompt", ev.RawText)
	}
}

func TestParseLineTimestampIsNormalisedToUTC(t *testing.T) {
	line := `{"type":"user","uuid":"u1","timestamp":"2026-08-25T12:00:00+03:00","sessionId":"s-1"}`
	ev, outcome := ParseLine([]byte(line))
	if outcome != Parsed {
		t.Fatalf("outcome = %v", outcome)
	}
	if ev.TS != "2026-08-25T09:00:00.000Z" {
		t.Errorf("TS = %q, want 2026-08-25T09:00:00.000Z", ev.TS)
	}
}

func TestLabelCarriesNoConversationText(t *testing.T) {
	line := `{"type":"user","uuid":"u1","timestamp":"2026-08-25T10:00:00.000Z",` +
		`"message":{"content":[{"type":"tool_result","content":"secret output"}]}}`
	ev, _ := ParseLine([]byte(line))
	if ev.RawText != "tool_result" {
		t.Errorf("RawText = %q, want tool_result", ev.RawText)
	}

	attach := `{"type":"attachment","uuid":"a1","timestamp":"2026-08-25T10:00:00.000Z",` +
		`"attachment":{"type":"edited_text_file","content":"line one\nline two"}}`
	ev, _ = ParseLine([]byte(attach))
	if ev.RawText != "edited_text_file" {
		t.Errorf("RawText = %q, want edited_text_file", ev.RawText)
	}
}

func TestProjectFromCWD(t *testing.T) {
	cases := map[string]string{
		"/home/u/projects/widget":  "widget",
		"/home/u/projects/widget/": "widget",
		"":                         "",
		"/":                        "",
	}
	for cwd, want := range cases {
		if got := projectFromCWD(cwd); got != want {
			t.Errorf("projectFromCWD(%q) = %q, want %q", cwd, got, want)
		}
	}
}
