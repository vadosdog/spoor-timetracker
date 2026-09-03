// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package claudecode reads Claude Code session logs from ~/.claude/projects.
//
// What the format actually looks like was measured before this parser was
// written, and the measurement, not the documentation, is what it follows:
//
//   - Only the 12 field core is read. It is identical across every client
//     (claude-desktop, cli, claude-vscode, sdk-py) and has never changed.
//     Everything else is optional and its absence is not an error.
//   - The field set is decided by the client (entrypoint), not by the version.
//     Versions are not monotonic in time, so "newer version" means nothing.
//   - The CLI writes both sessionId and session_id. Both spellings are read.
//   - type "system" covers two incompatible shapes (hooks and compaction).
//     They are told apart by subtype, never by type.
//   - Records without a timestamp (last-prompt, ai-title, mode, ...) are
//     session state, not events. There are thousands of them; they are
//     skipped silently and are not broken lines.
package claudecode

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/event"
)

// SourceName is what ingested events are tagged with.
const SourceName = "claude-code"

// tsLayout is how timestamps are normalised on the way in: UTC, milliseconds,
// always Z, so that string comparison in SQL is chronological comparison.
const tsLayout = "2006-01-02T15:04:05.000Z"

// record is the core of a JSONL line plus the few optional fields spoor uses.
// Anything not listed here is deliberately ignored.
type record struct {
	Type         string `json:"type"`
	UUID         string `json:"uuid"`
	Timestamp    string `json:"timestamp"`
	SessionID    string `json:"sessionId"`
	SessionIDAlt string `json:"session_id"` // CLI writes this one too
	Version      string `json:"version"`
	CWD          string `json:"cwd"`
	GitBranch    string `json:"gitBranch"`
	Entrypoint   string `json:"entrypoint"`
	IsSidechain  bool   `json:"isSidechain"`

	// One of these three is present depending on type.
	Message    *message    `json:"message"`
	Attachment *attachment `json:"attachment"`
	Subtype    string      `json:"subtype"`

	// Optional. Only system/turn_duration carries a real duration. Read as a
	// json.Number rather than an int64: the writer is JavaScript, and a value
	// arriving as 42000.0 one day must not cost us the whole event.
	DurationMS *json.Number `json:"durationMs"`
}

type message struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type attachment struct {
	Type string `json:"type"`
}

// contentBlock is read for its shape only: the block kind and, for tool calls,
// the tool name. The text of a block is never looked at.
type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// Outcome says what happened to one line.
type Outcome int

const (
	// Parsed means the line became an event.
	Parsed Outcome = iota
	// SkippedState means the line is session state, not an event: no
	// timestamp, or no uuid to identify it by. Expected and silent.
	SkippedState
	// Malformed means the line is not JSON. Measured at 0 out of 57 350,
	// but a truncated write is possible in principle.
	Malformed
)

// ParseLine turns one JSONL line into an event.
//
// It never returns an error: a line spoor cannot use is reported through the
// outcome and counted, because the caller must not abort an import over one
// unusable line in a 250 MB pile of them.
func ParseLine(line []byte) (event.Event, Outcome) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return event.Event{}, SkippedState
	}

	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return event.Event{}, Malformed
	}

	// No timestamp: session state (last-prompt, ai-title, mode, ...).
	// No uuid: not a core record, and nothing stable to dedup it by.
	if rec.Timestamp == "" || rec.UUID == "" {
		return event.Event{}, SkippedState
	}

	ts, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil {
		return event.Event{}, Malformed
	}

	sessionID := rec.SessionID
	if sessionID == "" {
		sessionID = rec.SessionIDAlt
	}

	return event.Event{
		Source:        SourceName,
		ExternalID:    rec.UUID,
		TS:            ts.UTC().Format(tsLayout),
		DurationMS:    durationMS(rec.DurationMS),
		Type:          rec.Type,
		Subtype:       rec.Subtype,
		Project:       projectFromCWD(rec.CWD),
		RawText:       label(rec),
		SessionID:     sessionID,
		CWD:           rec.CWD,
		GitBranch:     rec.GitBranch,
		Entrypoint:    rec.Entrypoint,
		IsSidechain:   rec.IsSidechain,
		ClientVersion: rec.Version,
	}, Parsed
}

// durationMS turns the reported duration into milliseconds. A value that
// cannot be read as a number costs the duration, not the event.
func durationMS(n *json.Number) *int64 {
	if n == nil {
		return nil
	}
	if ms, err := n.Int64(); err == nil {
		return &ms
	}
	f, err := n.Float64()
	if err != nil {
		return nil
	}
	ms := int64(f)
	return &ms
}

// projectFromCWD is the crude rule the measurement supports: the working
// directory names the project for 100% of Claude Code rows. Anything smarter
// (dictionaries, path prefixes, per-repository overrides) belongs to the
// attribution stage, not here.
func projectFromCWD(cwd string) string {
	cwd = strings.TrimRight(cwd, "/")
	if cwd == "" {
		return ""
	}
	base := filepath.Base(cwd)
	if base == "/" || base == "." {
		return ""
	}
	return base
}

// label builds the short "what happened" string stored in raw_text.
//
// Metadata only: model name, tool names, attachment kind. The text of a
// prompt, a reply or a tool result never reaches the database — that is a
// boundary of the project, not an optimisation.
func label(rec record) string {
	switch rec.Type {
	case "assistant":
		if rec.Message == nil {
			return ""
		}
		parts := []string{}
		if rec.Message.Model != "" {
			parts = append(parts, rec.Message.Model)
		}
		if tools := toolNames(rec.Message.Content); len(tools) > 0 {
			parts = append(parts, strings.Join(tools, ","))
		}
		return strings.Join(parts, " ")

	case "user":
		if rec.Message == nil {
			return ""
		}
		if hasBlock(rec.Message.Content, "tool_result") {
			return "tool_result"
		}
		return "prompt"

	case "attachment":
		if rec.Attachment == nil {
			return ""
		}
		return rec.Attachment.Type
	}
	// system and anything new: subtype already carries the meaning.
	return ""
}

func toolNames(content json.RawMessage) []string {
	var names []string
	for _, b := range blocks(content) {
		if b.Type == "tool_use" && b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return names
}

func hasBlock(content json.RawMessage, kind string) bool {
	for _, b := range blocks(content) {
		if b.Type == kind {
			return true
		}
	}
	return false
}

// blocks decodes the content array. Content is sometimes a plain string
// instead, in which case there are no blocks and that is not an error.
func blocks(content json.RawMessage) []contentBlock {
	if len(content) == 0 {
		return nil
	}
	var bs []contentBlock
	if err := json.Unmarshal(content, &bs); err != nil {
		return nil
	}
	return bs
}
