// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package event defines the single record every source produces.
package event

// Event is one trace of activity, as stored in the events table.
//
// Only metadata lives here. Conversation content, page text and file contents
// are out of scope for the whole project, not just for this struct.
type Event struct {
	// Source names the plugin that produced the event, e.g. "claude-code".
	Source string
	// ExternalID is the source's own stable identity for the event. Two runs
	// over the same data must yield the same value: it is the dedup key.
	ExternalID string
	// TS is the event time, RFC3339 with millisecond precision, always UTC.
	TS string
	// DurationMS is set only when the source reports a real duration.
	// A nil value means "point in time", not "zero seconds".
	DurationMS *int64
	// Type and Subtype are the source's own classification, kept verbatim so
	// the database stays readable and the raw shape stays recoverable.
	Type    string
	Subtype string
	// Project is the attribution guess made at ingest time. Empty when the
	// source cannot tell. Refined later; never authoritative here.
	Project string
	// RawText is a short metadata label ("what happened"), never content.
	RawText string

	// Fields below are Claude Code shaped. They are real fields of the only
	// source that exists so far; the plugin contract that generalises them
	// is a later stage.
	SessionID     string
	CWD           string
	GitBranch     string
	Entrypoint    string
	IsSidechain   bool
	ClientVersion string
}
