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

	// Entrypoint names the program that wrote the trace: "cli" or
	// "claude-desktop" for Claude Code, "chrome" or "firefox" for the
	// browser. It is the one field both sources fill.
	Entrypoint string

	// Fields below are Claude Code shaped. The plugin contract that
	// generalises per-source fields is a later stage; until then each source
	// fills its own and leaves the rest empty.
	SessionID     string
	CWD           string
	GitBranch     string
	IsSidechain   bool
	ClientVersion string

	// Fields below are browser shaped: where a visit went, never what was on
	// the page. There is deliberately no field for the full URL — a query
	// string carries session tokens and search terms.
	//
	// Host alone does not separate projects (one host serves several) and a
	// dev server is only told apart by its port, so all three are kept.
	Host string // "gitlab.example.com"
	// Port is "3000". It is empty when the URL carried no port, and also when
	// it carried the scheme's own default: https://x:443/ and https://x/ are
	// the same place, and storing them differently would split one host into
	// two keys for whatever groups by (host, port).
	Port string
	// PathHead is the first path segment and nothing below it, capped at 64
	// characters. What may stay in it is an allow-list taken from RFC 3986's
	// grammar for a path segment, less the characters §3.3 names as parameter
	// delimiters, so anything else ends it whether or not it was predicted.
	// See pathHead in the browser source for why it is that way round.
	PathHead string
	// Title is the page title the browser recorded, capped at 4096 characters,
	// with anything that could make it display as something else replaced by a
	// space. Metadata by the rules of this project, and still the most
	// revealing browser field there is: the title of a search result page is
	// the search query.
	Title string
	// Host, PathHead, Title and ExternalID are all repaired on the way in —
	// to valid UTF-8, and free of anything that would make them display as
	// something else. They carry bytes from somebody else's database or from
	// a directory name, and a Latin-1 path is not text. What SQLite cannot
	// decode it refuses to return at all, failing the whole query.
}
