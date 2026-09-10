// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package browser

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/text"
)

// SourceName is what ingested events are tagged with. Chrome and Firefox are
// one source, not two: they answer the same question, differ only in where
// they keep the answer, and a person who uses both wants one timeline.
const SourceName = "browser"

// tsLayout matches the Claude Code source: UTC, milliseconds, always Z, so
// that string comparison in SQL is chronological comparison.
const tsLayout = "2006-01-02T15:04:05.000Z"

// visit is one row of a browser's history, before it is reduced to an event.
type visit struct {
	ID         int64
	Time       time.Time
	URL        string
	Title      string
	Transition string
}

// Outcome says what happened to one visit.
type Outcome int

const (
	// Kept means the visit became an event.
	Kept Outcome = iota
	// NotWeb means the URL is not http or https — file://,
	// chrome-extension:// and the like, about 2% of visits when this was
	// measured. They are dropped because every field this source stores
	// describes a web address: a file:// URL has no host and no port, and
	// its "first path segment" is a directory on this machine, which is the
	// file source's business rather than the browser's.
	NotWeb
	// Ignored means the host is on the ignore list from the config.
	Ignored
	// Unparseable means the URL is not a URL. Not seen in real data, but a
	// history database is written by somebody else's code.
	Unparseable
)

// toEvent reduces one visit to the metadata spoor keeps.
//
// What is deliberately thrown away is the whole of the URL below the first
// path segment: the query string, the fragment, and every segment after the
// first. That is where session tokens, password reset links and search terms
// live, and none of them are time-tracking data.
//
// What is deliberately kept is more than the domain. One host serves several
// projects — gitlab.example.com/team-a and gitlab.example.com/team-b — and a
// dev server is told apart only by its port, so host, port and the first
// segment are all needed before any of this can name a project.
func toEvent(v visit, flavour, profile string, ignore Ignore) (event.Event, Outcome) {
	// An empty address is how the readers report a row they could not use: a
	// visit whose url is NULL, or one whose page has already been expired out
	// of the urls table. It is a row spoor cannot place, not a broken import.
	if v.URL == "" {
		return event.Event{}, Unparseable
	}
	u, err := url.Parse(v.URL)
	if err != nil {
		return event.Event{}, Unparseable
	}

	switch u.Scheme {
	case "http", "https":
	default:
		return event.Event{}, NotWeb
	}

	host := canonicalHost(u.Hostname())
	if host == "" {
		return event.Event{}, Unparseable
	}
	if ignore.Match(host) {
		return event.Event{}, Ignored
	}

	return event.Event{
		Source:     SourceName,
		ExternalID: externalID(flavour, profile, v.Time, v.ID),
		TS:         v.Time.UTC().Format(tsLayout),
		// No duration. A browser does report one, and it does not mean what
		// it looks like it means — see the note in chrome.go.
		DurationMS: nil,
		Type:       "visit",
		Subtype:    v.Transition,
		// Project is left to the attribution stage. Guessing it from the
		// host here would hard-code somebody's dictionary into the parser.
		Project:    "",
		Entrypoint: flavour,
		Host:       host,
		Port:       port(u),
		PathHead:   pathHead(u.Path),
		Title:      text.Printable(strings.TrimSpace(v.Title)),
	}, Kept
}

// port returns the port only when it says something. A URL written with its
// scheme's own default — https://example.com:443/ — means the same place as
// one without it, and storing "443" for the first and "" for the second would
// split one host into two keys for whatever groups by (host, port). Chrome
// normalises this away before writing; nothing promises Firefox does.
func port(u *url.URL) string {
	p := u.Port()
	if (u.Scheme == "https" && p == "443") || (u.Scheme == "http" && p == "80") {
		return ""
	}
	return p
}

// externalID is the dedup key. It carries the visit time as well as the
// visit id, and the time is not decoration: both browsers number visits with
// an ordinary rowid, and clearing the history restarts that numbering from
// one. Keyed on the id alone, every visit made after a "clear history" would
// collide with an already-imported row and be silently dropped.
//
// The profile name is sanitised because it is a directory name, and a
// directory name on Linux is bytes rather than text: nothing stops one
// holding a byte that is not UTF-8, and SQLite refuses to hand back a column
// it cannot decode.
func externalID(flavour, profile string, t time.Time, id int64) string {
	return strings.Join([]string{
		flavour,
		text.Sanitise(profile, text.Replacement),
		t.UTC().Format("2006-01-02T15:04:05.000000Z"),
		strconv.FormatInt(id, 10),
	}, "/")
}

// canonicalHost reduces a host to one spelling, so that "Example.COM." and
// "example.com" are one domain rather than three.
//
// An address has more than one spelling too — "::1", "0:0:0:0:0:0:0:1" and
// "::0001" are the same machine — and it is the ignore list that makes this
// matter: an entry in one spelling and a stored host in the other would never
// meet, without a warning, because both are perfectly valid. Both sides go
// through here, so both end up written the way netip writes it.
//
// It is also where a host is made safe to store. Both sides go through it —
// the host on the way in and the ignore entry on the way to the matcher — so
// a host that reaches the database can always be named in the config, whatever
// invisible thing it carried. Producer and validator agree because they are
// the same call. What that call changed about an entry is reported separately,
// by NewIgnore, before it gets here.
func canonicalHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(text.Sanitise(host, text.Replacement)), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.String()
	}
	return host
}

// CanonicalHost is canonicalHost for the attribution rules, which have the same
// problem the ignore list has and for the same reason: an entry written in one
// spelling and a stored host written in another would never meet, and nothing
// would warn. The rule that producer and validator must share one call does not
// stop at this package.
func CanonicalHost(host string) string { return canonicalHost(host) }

// IsAddress says whether a host is an address literal rather than a name. An
// address has no subdomains, so a rule matching one must match it exactly:
// walking labels would let an entry of "10" swallow 192.0.2.10.
func IsAddress(host string) bool {
	_, err := netip.ParseAddr(host)
	return err == nil
}

// pathHead returns the first path segment and nothing else. "/team/repo/-/
// merge_requests/12" becomes "team".
//
// It works on the decoded path on purpose, so that a %2F counts as the
// separator it decodes to: "/%2F%2Fdeep%2FSECRET" yields "deep", not the whole
// escaped run with the tail still in it.
//
// What ends the segment is decided by an allow-list, not by a list of
// delimiters to look out for. That is the whole point: a deny-list is only
// ever as good as the last thing somebody thought of, and this one was
// extended three times — for '?', then ';' after a review found jsessionid,
// then '\\' after the next one found UNC paths. Each fix was correct and each
// left the question open.
//
// Turned round, the question closes. A first path segment is a name, and what
// a name may contain is derived from the URL grammar rather than from whoever
// last looked at the data — see nameRune. Everything outside it ends the segment,
// whether or not anybody predicted it: '?', '#', ';', '=', '&', '\\', a
// control character, a private-use character, a delimiter invented after this
// was written.
//
// Measured before it was written, on 730 distinct segments of real browsing:
// the allow-list keeps 729 of them whole. The one it shortens ends in
// U+E000, a private-use character the previous deny-list did not catch —
// which is the argument in one row of data.
//
// Getting the set wrong costs a truncated name, not a leak, which is the
// direction the failure should point. It is not free, though: two segments
// that differ only past their first non-name character become one value, and
// this column is what the reporting stage will group on.
func pathHead(path string) string {
	seg := ""
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			seg = s
			break
		}
	}

	if i := strings.IndexFunc(seg, func(r rune) bool { return !nameRune(r) }); i >= 0 {
		seg = seg[:i]
	}

	// No sanitise here, unlike every other column: the cut has already done
	// it. Nothing unsafeInText is a name rune, and a byte that is not UTF-8
	// decodes to U+FFFD, which is not one either — so both are cut rather
	// than replaced. Calling sanitise as well would be a line that can never
	// change anything, and a line that can never fire reads to the next
	// person as a rule that can.
	//
	// A first path segment is a name — an owner, a group, a section. One
	// longer than 64 characters is not, and the database is meant to be read
	// by eye.
	return text.Truncate(seg, maxPathHead)
}

// nameRune reports whether a rune may stay in a path segment.
//
// The set is *derived from* RFC 3986 §3.3 rather than equal to it, and the
// basis matters more than the contents. The first version of this list was
// whatever punctuation turned up in one person's browsing, and a list
// assembled that way grows by one every time somebody looks at different
// data — '@' for Mastodon handles, then a space for SharePoint, then an
// apostrophe, then brackets. Taking it from the grammar ends that, because
// membership stops being a matter of whose data was to hand.
//
// The grammar is pchar = unreserved / pct-encoded / sub-delims / ":" / "@",
// where unreserved is ALPHA / DIGIT / "-._~" and sub-delims is "!$&'()*+,;=".
//
// It deviates in three places, all deliberate:
//
//  1. Four sub-delims are removed. §3.3 names three of them itself — ';', '='
//     and ',' — with the worked examples "name;v=1.1" and "name,1.1". The
//     comma is the one that looks harmless and is not: it attaches a value
//     with no '=' anywhere, so "/session,ABC123" would keep the value whole.
//     The fourth, '&', the section does not name — it belongs to the query
//     grammar — but it is how values are joined everywhere else and removing
//     it costs nothing. What is left carries nothing: "/O'Brien",
//     "/Foo (2024)" and "/important!" are names.
//
//  2. A space is added. It is not pchar at all — a URL cannot contain one
//     unescaped — so it only ever arrives as %20 and no convention hangs a
//     secret off it. Without it a SharePoint or an intranet file server loses
//     nearly every first segment it has: "/Shared%20Documents/",
//     "/Shared%20Files/" and "/Shared%20Drive/" would all collapse to
//     "Shared", three projects reported as one.
//
//  3. ALPHA and DIGIT are read as their Unicode categories, plus marks,
//     rather than as ASCII. This is the counterpart of pct-encoded, which
//     otherwise has none here: the path arrives decoded, so a name written in
//     any script has already become letters by the time this runs, and a mark
//     is part of the letter it sits on. Note this is narrower than
//     pct-encoded, not wider — an escape can decode to anything at all, and
//     only the letters, digits and marks among those are kept.
//
// This is a rule about what a name is, not about what can be seen. An
// invisible character that is a letter — U+3164 HANGUL FILLER, U+115F —
// passes, because excluding it would mean an allow-list of scripts. It can
// pad a segment; it cannot end one early or carry anything out of the tail.
func nameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) ||
		strings.ContainsRune("-._~!$'()*+:@ ", r)
}

const (
	// maxPathHead caps the first path segment. A URL can carry a segment of
	// any length, and an unbounded value out of somebody else's database has
	// no business in a column a person is expected to read.
	maxPathHead = 64
)

// Ignore is the domain list from the config, prepared for matching.
type Ignore struct {
	hosts map[string]struct{}
}

// IgnoreProblem is something the matcher wants to say about one entry. It
// carries the entry as the user wrote it, because that is what they will look
// for in their config file.
type IgnoreProblem struct {
	Entry  string
	Reason string
}

// NewIgnore prepares a matcher, and returns what it has to say about the
// entries it was given.
//
// Entries are canonicalised the same way hosts are, so a stray capital or an
// "https://" pasted in front of a domain does not quietly turn the entry into
// one that matches nothing. Two things are reported rather than swallowed,
// for the same reason the config file refuses a misspelled key: an ignore
// list that silently does nothing is the worst failure this file can have.
//
//   - An entry that could never be a host at all is rejected outright.
//   - An entry holding a character that cannot be seen is kept, sanitised the
//     way the stored host was, and said out loud. This is the common one, and
//     it is not exotic: a domain pasted out of a rendered page, a chat message
//     or a PDF can pick up a zero-width space or a soft hyphen, and a soft
//     hyphen is what a word processor inserts at a line break. Rejecting it
//     would reopen the hole where a host containing such a rune could never be
//     excluded; saying nothing would leave the user believing a domain is
//     covered when it is not.
func NewIgnore(entries []string) (Ignore, []IgnoreProblem) {
	ig := Ignore{hosts: make(map[string]struct{}, len(entries))}
	var problems []IgnoreProblem
	for _, raw := range entries {
		e := strings.ToLower(strings.TrimSpace(raw))
		e = strings.TrimPrefix(e, "https://")
		e = strings.TrimPrefix(e, "http://")
		e = strings.Trim(e, "./")
		// An address literal is written with brackets in a URL and stored
		// without them, so both spellings have to reach the same entry —
		// otherwise spoor records a host that can never be excluded, which is
		// a hole in the one control this project calls a privacy control.
		if inner, ok := strings.CutPrefix(e, "["); ok {
			if inner, ok := strings.CutSuffix(inner, "]"); ok {
				e = inner
			}
		}

		// Asked before canonicalHost, because canonicalHost sanitises and
		// would destroy the evidence. That is not hypothetical: this check
		// used to live in isDomain, and moving the sanitising into
		// canonicalHost silently turned it into a branch that could never be
		// taken.
		altered := text.Sanitise(e, text.Replacement) != e

		// Canonicalised last, once the entry is down to a bare host: it is the
		// same call the stored side makes, and running it before the brackets
		// came off would leave an address in whatever spelling was typed.
		e = canonicalHost(e)
		if e == "" {
			continue
		}
		if !isDomain(e) {
			problems = append(problems, IgnoreProblem{raw,
				"is not a domain name and matches nothing — entries are bare " +
					"domains, and one already covers its subdomains"})
			continue
		}
		if altered {
			problems = append(problems, IgnoreProblem{raw, fmt.Sprintf(
				"holds a character that cannot be seen, so it is matched as %q "+
					"— check it against the host in the database", e)})
		}
		ig.hosts[e] = struct{}{}
	}
	return ig, problems
}

// isDomain accepts anything a browser could report as a host — dotted labels,
// a bare name like "localhost", an address literal, a non-Latin domain — and
// rejects the shapes people reach for that could never match: a port
// ("localhost:3000"), a wildcard ("*.example.com"), a path
// ("example.com/watch"), an address with credentials in front of it.
//
// The rule is deliberately about what cannot be a host rather than about what
// a domain name may contain: guessing the latter would reject somebody's
// perfectly good domain and hide it behind a warning.
//
// It says nothing about invisible characters. It used to, and the check was
// dead: every caller hands it a value that canonicalHost has already
// sanitised. NewIgnore reports those before canonicalising instead.
func isDomain(s string) bool {
	if s == "" || strings.Contains(s, "..") {
		return false
	}
	// An IPv6 literal is the one host with colons in it, and spoor does store
	// them, so it has to be nameable here.
	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.IsValid()
	}
	// Any whitespace, not just a space and a tab: a host cannot contain one,
	// and a non-breaking space inside a domain name is the same silent
	// non-match as the invisible characters NewIgnore reports — it is simply
	// Zs rather than Cf, so nothing else would catch it.
	return !strings.ContainsAny(s, "/*?:@\\") && strings.IndexFunc(s, unicode.IsSpace) < 0
}

// Empty reports whether nothing is ignored.
func (ig Ignore) Empty() bool { return len(ig.hosts) == 0 }

// Match reports whether host is on the list. An entry covers the host itself
// and every subdomain of it: "example.com" matches "www.example.com" but not
// "notexample.com", because the comparison starts at a label boundary.
func (ig Ignore) Match(host string) bool {
	if len(ig.hosts) == 0 {
		return false
	}
	// An address has no subdomains, and walking its labels would mean an
	// entry of "10" swallowed 192.0.2.10 — over-matching, which deletes what
	// the user did not ask to delete and says nothing about it.
	if _, err := netip.ParseAddr(host); err == nil {
		_, ok := ig.hosts[host]
		return ok
	}
	for {
		if _, ok := ig.hosts[host]; ok {
			return true
		}
		dot := strings.Index(host, ".")
		if dot < 0 {
			return false
		}
		host = host[dot+1:]
	}
}
