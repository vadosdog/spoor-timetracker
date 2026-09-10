// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package browser

import (
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vadosdog/spoor-timetracker/internal/text"
)

func TestToEventReducesTheURL(t *testing.T) {
	for _, c := range []struct {
		url                  string
		host, port, pathHead string
		outcome              Outcome
	}{
		{"https://example.com/", "example.com", "", "", Kept},
		{"https://example.com", "example.com", "", "", Kept},
		{"https://Example.COM./Team/Repo", "example.com", "", "Team", Kept},
		{"http://localhost:3000/orders/17?x=1", "localhost", "3000", "orders", Kept},
		{"https://gitlab.example.com/group/svc/-/pipelines", "gitlab.example.com", "", "group", Kept},
		{"https://example.com//double//slash", "example.com", "", "double", Kept},
		{"https://example.com/?q=secret#frag", "example.com", "", "", Kept},
		{"https://user:pass@example.com/x", "example.com", "", "x", Kept},
		{"https://192.0.2.10:8443/health", "192.0.2.10", "8443", "health", Kept},
		// A port that is the scheme's own default says nothing, and storing
		// it would split one host into two keys for whatever groups by
		// (host, port).
		{"https://example.com:443/a", "example.com", "", "a", Kept},
		{"http://example.com:80/a", "example.com", "", "a", Kept},
		{"https://example.com:8443/a", "example.com", "8443", "a", Kept},
		// Percent-encoding is the only way a delimiter can appear inside a
		// segment. Each ends the segment, so nothing below the first one
		// survives any of these. Cut, never deleted: deleting would turn
		// "a%0d%0ab" into "ab", a name that never existed.
		{"https://example.com/%3Fq=SEARCHTERM", "example.com", "", "", Kept},
		{"https://example.com/%23FRAG/second", "example.com", "", "", Kept},
		{"https://example.com/a%00b/SECRET", "example.com", "", "a", Kept},
		{"https://example.com/a%0d%0ab/SECRET", "example.com", "", "a", Kept},
		{"https://example.com/team%3Fq=SECRET", "example.com", "", "team", Kept},
		{"https://example.com/%2F%2Fdeep%2FSECRET", "example.com", "", "deep", Kept},
		// ';' is the third RFC 3986 path delimiter, and the one a servlet
		// container puts a session id after when cookies are off.
		{"https://example.com/portal;jsessionid=SESSIONTOKEN", "example.com", "", "portal", Kept},
		{"https://example.com/seg%3Bfoo=SECRET", "example.com", "", "seg", Kept},
		// A backslash is a path separator where a lot of intranet servers run.
		{"https://example.com/share%5Cwin%5CSECRET", "example.com", "", "share", Kept},
		// Nothing above was predicted by name. The segment is an allow-list
		// taken from RFC 3986's own grammar, so what ends it is decided by
		// the grammar rather than by whoever last looked at the data.
		//
		// Of everything the grammar permits in a segment, three are removed:
		// the ones with a convention for carrying a parameter.
		// §3.3 names three: ';', '=' and ','. The comma is the one that looks
		// harmless — it attaches a value with no '=' anywhere, which is the
		// RFC's own "name,1.1" example, so without cutting there the value
		// would survive whole.
		{"https://example.com/a;SECRET", "example.com", "", "a", Kept},
		{"https://example.com/a=SECRET", "example.com", "", "a", Kept},
		{"https://example.com/session,ABC123SECRET/x", "example.com", "", "session", Kept},
		{"https://example.com/name,1.1/x", "example.com", "", "name", Kept},
		// '&' is not named by §3.3 — it belongs to the query grammar — but it
		// is how values are joined everywhere else and costs nothing to cut.
		{"https://example.com/a&SECRET", "example.com", "", "a", Kept},
		// The rest of the grammar is ordinary name punctuation and stays.
		// Nothing here is "below" the first segment — it is the segment.
		{"https://example.com/O'Brien/x", "example.com", "", "O'Brien", Kept},
		{"https://example.com/Foo%20(2024)/x", "example.com", "", "Foo (2024)", Kept},
		{"https://example.com/important!/x", "example.com", "", "important!", Kept},
		{"https://example.com/a$b*c+d/x", "example.com", "", "a$b*c+d", Kept},
		{"https://example.com/~alice/x", "example.com", "", "~alice", Kept},
		// A percent sign is not in the grammar: it introduces an escape.
		{"https://example.com/100%25/SECRET", "example.com", "", "100", Kept},
		// U+E000, private use: invisible, and no deny-list of controls and
		// format characters catches it. It turned up in real data.
		{"https://example.com/watch%EE%80%80", "example.com", "", "watch", Kept},
		// What a name is actually made of, in any script, survives whole.
		{"https://example.com/@handle/posts", "example.com", "", "@handle", Kept},
		{"https://example.com/%D0%BF%D1%80%D0%BE%D0%B5%D0%BA%D1%82/x", "example.com", "", "проект", Kept},
		{"https://example.com/Test-Repo_2024.v1/x", "example.com", "", "Test-Repo_2024.v1", Kept},
		// A space, because a SharePoint or an intranet file server puts one in
		// nearly every first segment it has. Without it three different
		// document libraries all report as "Shared".
		{"https://example.com/Shared%20Documents/report.docx", "example.com", "", "Shared Documents", Kept},
		{"https://example.com/Site%20Pages/Home.aspx", "example.com", "", "Site Pages", Kept},
		// An invisible character that is a letter passes: this is a rule about
		// what a name is, not about what can be seen. It pads; it cannot end
		// the segment early or carry the tail out.
		{"https://example.com/watch%E3%85%A4/TAIL", "example.com", "", "watch\u3164", Kept},
		// Above ASCII, U+202E reverses everything after it on screen.
		{"https://example.com/\u202eexe.gnp", "example.com", "", "", Kept},
		{"https://example.com/a\u2028b", "example.com", "", "a", Kept},
		{"", "", "", "", Unparseable},
		{"file:///home/someone/notes.html", "", "", "", NotWeb},
		{"chrome://newtab/", "", "", "", NotWeb},
		{"about:blank", "", "", "", NotWeb},
		{"https:///nohost", "", "", "", Unparseable},
		{"://broken", "", "", "", Unparseable},
	} {
		ev, outcome := toEvent(visit{URL: c.url, Time: time.Unix(0, 0)}, "chrome", "P", Ignore{})
		if outcome != c.outcome {
			t.Errorf("%s: outcome = %v, want %v", c.url, outcome, c.outcome)
			continue
		}
		if outcome != Kept {
			continue
		}
		if ev.Host != c.host || ev.Port != c.port || ev.PathHead != c.pathHead {
			t.Errorf("%s: host/port/pathHead = %q/%q/%q, want %q/%q/%q",
				c.url, ev.Host, ev.Port, ev.PathHead, c.host, c.port, c.pathHead)
		}
	}
}

// The credentials some URLs carry in front of the host are not metadata by
// anybody's definition.
func TestUserInfoIsNotStored(t *testing.T) {
	ev, outcome := toEvent(visit{
		URL:  "https://alice:hunter2@example.com/inbox",
		Time: time.Unix(0, 0),
	}, "chrome", "P", Ignore{})
	if outcome != Kept {
		t.Fatalf("outcome = %v", outcome)
	}
	for _, field := range []string{ev.Host, ev.Port, ev.PathHead, ev.Title, ev.RawText, ev.ExternalID} {
		if field == "alice" || field == "hunter2" ||
			field == "alice:hunter2@example.com" {
			t.Errorf("a credential reached the event: %q", field)
		}
	}
	if ev.Host != "example.com" {
		t.Errorf("host = %q, want example.com", ev.Host)
	}
}

func TestIgnoreMatchesTheHostAndItsSubdomains(t *testing.T) {
	ig, rejected := NewIgnore([]string{"example.com", " HTTPS://Videos.example/ ", "", "."})
	if len(rejected) != 0 {
		t.Errorf("rejected %q, want nothing", rejected)
	}

	for _, host := range []string{"example.com", "www.example.com", "a.b.example.com", "videos.example"} {
		if !ig.Match(host) {
			t.Errorf("%s is not ignored and should be", host)
		}
	}
	for _, host := range []string{"notexample.com", "example.com.evil.net", "example.org", "videos.example.net"} {
		if ig.Match(host) {
			t.Errorf("%s is ignored and should not be", host)
		}
	}
	empty, _ := NewIgnore(nil)
	if empty.Match("example.com") {
		t.Error("an empty list ignores something")
	}
	if !empty.Empty() {
		t.Error("an empty list does not report itself empty")
	}
}

func TestExternalIDIsStableAndDistinct(t *testing.T) {
	when := time.Date(2026, 8, 25, 9, 0, 0, 123456000, time.UTC)

	a := externalID("chrome", "Profile 1", when, 42)
	if b := externalID("chrome", "Profile 1", when, 42); a != b {
		t.Errorf("the same visit produced %q and %q", a, b)
	}
	for _, other := range []string{
		externalID("firefox", "Profile 1", when, 42),
		externalID("chrome", "Profile 2", when, 42),
		externalID("chrome", "Profile 1", when.Add(time.Microsecond), 42),
		externalID("chrome", "Profile 1", when, 43),
	} {
		if other == a {
			t.Errorf("a different visit produced the same id %q", a)
		}
	}
}

func TestProfileAtDecidesTheFlavourByFileName(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		file    string
		flavour string
		ok      bool
	}{
		{chromeHistoryFile, "chrome", true},
		{firefoxHistoryFile, "firefox", true},
		{"Bookmarks", "", false},
		{"history.db", "", false},
	} {
		p, ok := ProfileAt(filepath.Join(dir, "Some Profile", c.file))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.file, ok, c.ok)
			continue
		}
		if ok && (p.Flavour != c.flavour || p.Name != "Some Profile") {
			t.Errorf("%s: got %+v, want flavour %q and name %q", c.file, p, c.flavour, "Some Profile")
		}
	}
}

// A path segment is a name, and an unbounded value out of somebody else's
// database has no business in a column meant to be read by eye.
func TestPathHeadIsBounded(t *testing.T) {
	long := strings.Repeat("A", 5000)
	ev, outcome := toEvent(visit{
		URL:  "https://example.com/" + long + "/second",
		Time: time.Unix(0, 0),
	}, "chrome", "P", Ignore{})
	if outcome != Kept {
		t.Fatalf("outcome = %v", outcome)
	}
	if len([]rune(ev.PathHead)) != maxPathHead {
		t.Errorf("path_head is %d runes, want it capped at %d", len([]rune(ev.PathHead)), maxPathHead)
	}
}

// The title is the column the README tells a person to read with their own
// eyes, and the page chose its own title. A NUL makes SQLite's length()
// under-report; a newline — and U+2028, and U+0085 — turn one row of
// `sqlite3 -line` into two; U+202E reverses everything after it, so the title
// on screen is not the title in the database.
func TestCharactersThatMisrepresentTheTitleBecomeSpaces(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"first\nsecond\x00third\ttail", "first second third tail"},
		{"one\u2028two\u2029three\u0085four", "one two three four"},
		{"safe\u202etxt.exe", "safe txt.exe"},
		{"zero\u200bwidth", "zero width"},
	} {
		ev, outcome := toEvent(visit{
			URL: "https://example.com/a", Title: c.in, Time: time.Unix(0, 0),
		}, "chrome", "P", Ignore{})
		if outcome != Kept {
			t.Fatalf("%q: outcome = %v", c.in, outcome)
		}
		if ev.Title != c.want {
			t.Errorf("title = %q, want %q", ev.Title, c.want)
		}
	}
}

// Every field carrying bytes from the browser, against every input that has
// ever broken one of them — plus the fields that carry nothing but constants,
// since asserting them costs a map entry and would catch the day one of them
// stops being a constant.
//
// The column list is deliberately not the two or three we happened to think
// of: this exact defect has been found twice, each time in a column the last
// fix did not reach. A path is percent-encoded bytes rather than text, a
// profile name is a directory name, and SQLite refuses to return a column it
// cannot decode — so one such row turns `SELECT *`, which the README calls
// "the whole record", into no rows at all.
func TestEveryTextFieldIsSafeToStoreAndToReadBack(t *testing.T) {
	const secret = "SECRETTAIL"
	for _, bad := range []string{
		"caf\xe9", "\xff\xfe", "\x80\x80", "a\xc3b", // not UTF-8 at all
		"a\u202eb", "a\u0085b", "a\ufeffb", "a\u2028b", // valid, and misrepresenting
	} {
		ev, outcome := toEvent(visit{
			URL:   "https://ex" + url.PathEscape(bad) + "ample.com/" + url.PathEscape(bad) + "/" + secret,
			Title: "before " + bad + " after",
			Time:  time.Unix(0, 0),
		}, "chrome", "Prof"+bad+"ile", Ignore{})
		if outcome != Kept {
			t.Fatalf("%q: outcome = %v", bad, outcome)
		}

		// Every string field on the event, not the ones that came to mind.
		// The six that this source never sets are here too: asserting they
		// stay empty costs a map entry and catches the day a browser value
		// starts reaching one of them.
		for name, v := range map[string]string{
			"host": ev.Host, "port": ev.Port, "path_head": ev.PathHead,
			"title": ev.Title, "external_id": ev.ExternalID, "raw_text": ev.RawText,
			"type": ev.Type, "subtype": ev.Subtype, "entrypoint": ev.Entrypoint,
			"project": ev.Project, "source": ev.Source, "ts": ev.TS,
			"session_id": ev.SessionID, "cwd": ev.CWD, "git_branch": ev.GitBranch,
			"client_version": ev.ClientVersion,
		} {
			if !utf8.ValidString(v) {
				t.Errorf("%q: %s = %q, which is not valid UTF-8", bad, name, v)
			}
			if i := strings.IndexFunc(v, text.Unsafe); i >= 0 {
				t.Errorf("%q: %s = %q holds a character that misrepresents it", bad, name, v)
			}
			if strings.Contains(v, secret) {
				t.Errorf("%q: %s = %q carries the tail of the path", bad, name, v)
			}
		}

		// A stored host that cannot be excluded is the hole the entry
		// validation exists to close, so assert the property rather than the
		// absence of a complaint.
		if ig, _ := NewIgnore([]string{ev.Host}); !ig.Match(ev.Host) {
			t.Errorf("%q: host %q is stored but cannot be put on the ignore list", bad, ev.Host)
		}
	}
}

// The same bytes must survive a round trip through the database, because the
// failure this guards against is SQLite refusing to return the column at all.
func TestInvalidUTF8SurvivesTheDatabase(t *testing.T) {
	st := newStore(t)
	p := chromeProfile(t, filepath.Join(t.TempDir(), "Prof\xffile"),
		hit{at("09:00"), "https://ex%FFample.com/caf%E9/second", "caf\xe9 \xff\xfe", 0},
	)
	if _, err := Ingest(st, []Profile{p}, Ignore{}); err != nil {
		t.Fatal(err)
	}

	// SELECT *, which is what the README's step 6 runs and calls "the whole
	// record". One undecodable column fails the whole query.
	rows, err := st.DB().Query(`SELECT * FROM events`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for rows.Next() {
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(any)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("SELECT * could not be read back: %v", err)
		}
		seen++
		for i, cell := range cells {
			s, ok := (*cell.(*any)).(string)
			if ok && !utf8.ValidString(s) {
				t.Errorf("%s came back as %q, which is not valid UTF-8", cols[i], s)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Errorf("SELECT * returned %d rows, want 1", seen)
	}
}

// The title is the one column an arbitrary web page fills in entirely.
func TestTitleIsBounded(t *testing.T) {
	ev, outcome := toEvent(visit{
		URL:   "https://example.com/a",
		Title: strings.Repeat("A", 200_000),
		Time:  time.Unix(0, 0),
	}, "chrome", "P", Ignore{})
	if outcome != Kept {
		t.Fatalf("outcome = %v", outcome)
	}
	if len([]rune(ev.Title)) != text.MaxTitle {
		t.Errorf("title is %d runes, want it capped at %d", len([]rune(ev.Title)), text.MaxTitle)
	}
}

// An entry that can never match is an ignore list that silently does nothing,
// which is the one thing this file must not do.
func TestIgnoreRejectsEntriesThatCouldNeverMatch(t *testing.T) {
	ig, rejected := NewIgnore([]string{
		"localhost:3000", "*.example.com", "example.com/watch",
		"user@example.com", "a..b",
		// A host cannot hold whitespace of any kind. A non-breaking space
		// inside a domain name is the same silent non-match as an invisible
		// character, but it is Zs rather than Cf, so nothing else catches it.
		"a\u00a0b.example", "a\u3000b.example",
		"good.example", "xn--e1afmkfd.xn--p1ai", "почта.рф",
		// An address literal is written with brackets in a URL and stored
		// without them. Both spellings must reach the same entry, or spoor
		// records a host nobody can ever exclude.
		"[fe80::1]", "::1", "192.0.2.10",
	})

	var got []string
	for _, p := range rejected {
		if !strings.Contains(p.Reason, "matches nothing") {
			t.Errorf("%q was reported as %q, want a rejection", p.Entry, p.Reason)
		}
		got = append(got, p.Entry)
	}
	want := []string{"localhost:3000", "*.example.com", "example.com/watch", "user@example.com",
		"a..b", "a\u00a0b.example", "a\u3000b.example"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rejected %q, want %q", got, want)
	}
	for _, host := range []string{
		"good.example", "xn--e1afmkfd.xn--p1ai", "почта.рф",
		"fe80::1", "::1", "192.0.2.10",
	} {
		if !ig.Match(host) {
			t.Errorf("%s should have been accepted and ignored", host)
		}
	}
}

// A domain pasted out of a rendered page, a chat message or a PDF can pick up
// a character nobody can see — a soft hyphen is what a word processor inserts
// at a line break. Such an entry must still match the host it was meant for,
// and the user must be told, because otherwise they believe a domain is
// covered when the only evidence either way is invisible.
//
// This one is regression cover: the check that used to catch it lived in
// isDomain, and moving the sanitising into canonicalHost turned that branch
// into one that could never be taken. Nothing failed; the warning simply
// stopped happening.
func TestIgnoreReportsAnInvisibleCharacterAndStillMatches(t *testing.T) {
	for _, entry := range []string{
		"videos\u00ad.example", // soft hyphen, the one a line break inserts
		"videos\u200b.example", // zero-width space
		"\ufeffvideos.example", // byte order mark, from a copied file
	} {
		ig, problems := NewIgnore([]string{entry})
		if len(problems) != 1 {
			t.Errorf("%q: %d problems, want exactly one", entry, len(problems))
			continue
		}
		if !strings.Contains(problems[0].Reason, "cannot be seen") {
			t.Errorf("%q: reported as %q", entry, problems[0].Reason)
		}
		if problems[0].Entry != entry {
			t.Errorf("the report names %q, not the entry as written", problems[0].Entry)
		}
		// Reported, and still doing its job: the host that carried the same
		// character is excluded.
		host, _ := toEvent(visit{
			URL: "https://" + strings.NewReplacer("\u00ad", "%C2%AD", "\u200b", "%E2%80%8B",
				"\ufeff", "%EF%BB%BF").Replace(entry) + "/watch",
			Time: time.Unix(0, 0),
		}, "chrome", "P", Ignore{})
		if !ig.Match(host.Host) {
			t.Errorf("%q does not match the host %q it was written for", entry, host.Host)
		}
	}
}

// An address has several valid spellings, and both browsers pick their own.
// An entry written one way and a host stored the other would never meet, and
// nothing would warn — both are perfectly valid.
func TestIgnoreMatchesAnAddressInAnySpelling(t *testing.T) {
	for _, c := range []struct{ entry, host string }{
		{"0:0:0:0:0:0:0:1", "::1"},
		{"::1", "0:0:0:0:0:0:0:1"},
		{"::0001", "::1"},
		{"[::1]", "0:0:0:0:0:0:0:1"},
		{"FE80::1", "fe80::1"},
	} {
		ig, rejected := NewIgnore([]string{c.entry})
		if len(rejected) != 0 {
			t.Errorf("%q was rejected", c.entry)
			continue
		}
		if !ig.Match(canonicalHost(c.host)) {
			t.Errorf("entry %q does not match host %q", c.entry, c.host)
		}
	}
}

// An address has no subdomains. Walking its labels would let an entry of "10"
// swallow 192.0.2.10 — over-matching, which deletes what the user did not ask
// to delete and says nothing about it.
func TestIgnoreDoesNotWalkTheLabelsOfAnAddress(t *testing.T) {
	ig, _ := NewIgnore([]string{"10", "2.10"})
	if ig.Match("192.0.2.10") {
		t.Error("an entry of 10 or 2.10 swallowed the address 192.0.2.10")
	}
}

// A source declares where it works and says nothing anywhere else. There is
// no system spoor builds for on which this one is meant to fail.
func TestSupportedOnEverySystemWeBuildFor(t *testing.T) {
	if !Supported() {
		t.Fatalf("the browser source declares itself absent on %s", runtime.GOOS)
	}
}

// Discover reads the home directory, so the test gives it an empty one. No
// test may look at the machine's real profiles: it would pass or fail
// depending on whose laptop it runs on, and it would be reading browsing
// history that is none of its business.
func TestDiscoverFindsNothingInAnEmptyHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", t.TempDir())
		t.Setenv("APPDATA", t.TempDir())
	}
	if got := Discover(); len(got) != 0 {
		t.Errorf("found %+v in an empty home, want nothing", got)
	}
}

func TestProfilesUnderFindsOnlyRealProfiles(t *testing.T) {
	root := t.TempDir()
	chromeProfile(t, filepath.Join(root, "Profile 1"), hit{at("09:00"), "https://example.com/", "x", 0})
	chromeProfile(t, filepath.Join(root, "Profile 2"), hit{at("09:00"), "https://example.com/", "x", 0})
	// A directory with no history in it, which is what most of the
	// directories beside a real profile are.
	firefoxProfile(t, filepath.Join(root, "ShaderCache"))

	found := profilesUnder(root, "chrome", chromeHistoryFile)
	if len(found) != 2 {
		t.Fatalf("found %d profiles, want 2: %+v", len(found), found)
	}
	for _, p := range found {
		if p.Flavour != "chrome" {
			t.Errorf("flavour = %q, want chrome", p.Flavour)
		}
	}
	if got := profilesUnder(filepath.Join(root, "nowhere"), "chrome", chromeHistoryFile); got != nil {
		t.Errorf("a missing directory yielded %+v, want nothing", got)
	}
}
