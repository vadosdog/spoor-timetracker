// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package rules

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/event"
)

// Every fixture is written as the YAML somebody would actually type, so that
// the test covers the shape of the file as well as the matching. A rule that
// works when hand-built in Go and cannot be expressed in the config is not a
// rule anybody has.
func compile(t *testing.T, doc string) *Rules {
	t.Helper()
	r, problems := parse(t, doc)
	for _, p := range problems {
		t.Errorf("unexpected problem: %q %s", p.Entry, p.Reason)
	}
	return r
}

func parse(t *testing.T, doc string) (*Rules, []Problem) {
	t.Helper()
	var cfg struct {
		Attribution config.Attribution `yaml:"attribution"`
	}
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("config does not parse: %v\n%s", err, doc)
	}
	return New(cfg.Attribution)
}

func cc(cwd string) event.Event {
	return event.Event{Source: "claude-code", CWD: cwd, Project: lastElement(cwd)}
}

// lastElement is the crude guess the import makes, repeated here so that a
// fixture event looks exactly like one that came out of the database.
func lastElement(cwd string) string {
	cwd = strings.TrimRight(cwd, "/")
	if i := strings.LastIndex(cwd, "/"); i >= 0 {
		return cwd[i+1:]
	}
	return cwd
}

func web(host, port, segment, title string) event.Event {
	return event.Event{Source: "browser", Host: host, Port: port, PathHead: segment, Title: title}
}

func resolve(t *testing.T, r *Rules, e event.Event) (string, string) {
	t.Helper()
	p, s, _ := r.Resolve(e)
	return p, s
}

func TestPathMatchesTheDirectoryAndEverythingUnderIt(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: widget
      paths: /src/widget
`)
	for _, cwd := range []string{"/src/widget", "/src/widget/api", "/src/widget/api/internal"} {
		if got, _ := resolve(t, r, cc(cwd)); got != "widget" {
			t.Errorf("%s named %q, want widget", cwd, got)
		}
	}
	// The point of the whole rule: a subdirectory opened on its own used to be
	// a project called "api", and two unrelated directories called "src" used
	// to be one project.
	if got, _ := resolve(t, r, cc("/src/widget-other")); got != "widget-other" {
		t.Errorf("/src/widget-other named %q; a directory that merely starts the same is not the project", got)
	}
	if got, _ := resolve(t, r, cc("/elsewhere/widget/api")); got != "api" {
		t.Errorf("/elsewhere/widget/api named %q, want the import guess", got)
	}
}

// An agent creates a worktree per session, with a random suffix. Those are one
// project written one way and a dozen written the other, and a trailing star is
// what says so.
func TestATrailingStarIsAPlainPrefix(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: campaign
      paths: /tmp/campaign-*
`)
	for _, cwd := range []string{"/tmp/campaign-a1b2c3", "/tmp/campaign-d4e5f6/notes"} {
		if got, _ := resolve(t, r, cc(cwd)); got != "campaign" {
			t.Errorf("%s named %q, want campaign", cwd, got)
		}
	}
	if got, _ := resolve(t, r, cc("/tmp/other")); got != "other" {
		t.Errorf("/tmp/other named %q, want the import guess", got)
	}
}

func TestTheLongerPathWins(t *testing.T) {
	// Written in the order that would give the wrong answer if the file order
	// decided: the broad rule first.
	r := compile(t, `
attribution:
  projects:
    - name: everything
      paths: /src
    - name: widget
      paths: /src/widget
`)
	if got, _ := resolve(t, r, cc("/src/widget/api")); got != "widget" {
		t.Errorf("named %q, want widget: the more specific rule has to win whatever the order", got)
	}
	if got, _ := resolve(t, r, cc("/src/other")); got != "everything" {
		t.Errorf("named %q, want everything", got)
	}
}

func TestKeyMatchesHostPortAndSegment(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: tracker
      keys: [tracker.example.com, dev.example.com:3000/admin]
`)
	cases := []struct {
		e    event.Event
		want string
	}{
		// A bare host covers its subdomains and every segment of it.
		{web("tracker.example.com", "", "issues", ""), "tracker"},
		{web("eu.tracker.example.com", "", "", ""), "tracker"},
		{web("tracker.example.com", "8080", "x", ""), "tracker"},
		// A written-out port and segment have to be the ones.
		{web("dev.example.com", "3000", "admin", ""), "tracker"},
		{web("dev.example.com", "5173", "admin", ""), ""},
		{web("dev.example.com", "3000", "public", ""), ""},
		// "example.com" is not a subdomain of "tracker.example.com".
		{web("example.com", "", "", ""), ""},
		{web("nottracker.example.com", "", "", ""), ""},
	}
	for _, c := range cases {
		if got, _ := resolve(t, r, c.e); got != c.want {
			t.Errorf("%s:%s/%s named %q, want %q", c.e.Host, c.e.Port, c.e.PathHead, got, c.want)
		}
	}
}

func TestTheLongerKeyWins(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: platform
      keys: app.example.com
    - name: payroll
      keys: app.example.com/salary
`)
	if got, _ := resolve(t, r, web("app.example.com", "", "salary", "")); got != "payroll" {
		t.Errorf("named %q, want payroll", got)
	}
	if got, _ := resolve(t, r, web("app.example.com", "", "orders", "")); got != "platform" {
		t.Errorf("named %q, want platform", got)
	}
}

// Where something happened beats what a piece of text looked like. The two can
// only compete on one event when a Claude Code event has a branch, or a browser
// event a title.
func TestALiteralBeatsAnExpression(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: by-title
      titles: 'widget'
    - name: by-key
      keys: docs.example.com
`)
	if got, _ := resolve(t, r, web("docs.example.com", "", "", "widget manual")); got != "by-key" {
		t.Errorf("named %q, want by-key", got)
	}
	if got, _ := resolve(t, r, web("other.example.com", "", "", "widget manual")); got != "by-title" {
		t.Errorf("named %q, want by-title", got)
	}
}

// Two expressions cannot be compared for specificity, so the order they are
// written in is what decides — and it has to decide the same way every time.
func TestBetweenExpressionsTheOrderDecides(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: first
      titles: 'shared'
    - name: second
      titles: 'shared'
`)
	for i := 0; i < 20; i++ {
		if got, _ := resolve(t, r, web("x.example.com", "", "", "a shared page")); got != "first" {
			t.Fatalf("named %q on run %d, want first every time", got, i)
		}
	}
}

// The never list takes the key away, not the event: the host says nothing, and
// the title still says what it says. This is what lets an issue tracker be
// recognised without an API — and the tracker's home page, which names no
// project at all, stay unnamed.
func TestNeverRefusesTheKeyAndNotTheTitle(t *testing.T) {
	r := compile(t, `
attribution:
  never: tracker.example.com
  projects:
    - name: widget
      titles: 'Widget Service'
`)
	if got, _ := resolve(t, r, web("tracker.example.com", "", "boards", "All boards")); got != "" {
		t.Errorf("named %q; a key on the never list must not name a project", got)
	}
	if got, _ := resolve(t, r, web("tracker.example.com", "", "browse", "WID-7 fix the thing - Widget Service")); got != "widget" {
		t.Errorf("named %q, want widget: the title is still evidence", got)
	}
}

// A bare host under keys covers every segment of it, so the never list is how
// one segment is carved back out. Without this the only way to exclude a
// search page under a host you own is to list every other segment of it.
func TestNeverCarvesASegmentOutOfABroadRule(t *testing.T) {
	r := compile(t, `
attribution:
  never: wiki.example.com/search
  projects:
    - name: handbook
      keys: wiki.example.com
`)
	if got, _ := resolve(t, r, web("wiki.example.com", "", "page", "")); got != "handbook" {
		t.Errorf("named %q, want handbook", got)
	}
	if got, _ := resolve(t, r, web("wiki.example.com", "", "search", "")); got != "" {
		t.Errorf("named %q, want nothing: the search page of a wiki is every project at once", got)
	}
}

// "~" is the home directory, and nothing else is expanded. A config that
// interpolated environment variables would mean one thing in a terminal and
// another under cron.
func TestTildeIsTheHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "/home/somebody")
	r := compile(t, `
attribution:
  projects:
    - name: widget
      paths: ~/src/widget
`)
	if got, _ := resolve(t, r, cc("/home/somebody/src/widget/api")); got != "widget" {
		t.Errorf("named %q, want widget", got)
	}
	// The same path under a different home is named "widget" too — by the
	// import guess, not by the rule. Asking what named it is the only way to
	// tell those apart when they agree.
	if r.Covers(cc("/home/else/src/widget")) {
		t.Error("~ expanded to something that matches another home directory")
	}
}

// The dictionary can only be maintained if the tool says what it does not
// cover. A key deliberately refused counts as covered — a decision was made
// about it — while one nobody has mentioned does not.
func TestCoversTellsDecidedFromUnmentioned(t *testing.T) {
	r := compile(t, `
attribution:
  never: search.example.com
  projects:
    - name: widget
      paths: /src/widget
`)
	cases := []struct {
		e    event.Event
		want bool
	}{
		{cc("/src/widget"), true},
		{cc("/src/other"), false},
		{web("search.example.com", "", "q", ""), true},
		{web("unknown.example.com", "", "", ""), false},
	}
	for _, c := range cases {
		if got := r.Covers(c.e); got != c.want {
			t.Errorf("Covers(%s%s) = %v, want %v", c.e.CWD, c.e.Host, got, c.want)
		}
	}
}

func TestSubjectFromACaptureGroup(t *testing.T) {
	r := compile(t, `
attribution:
  subjects:
    - titles: '\b([A-Z]+-\d+)\b'
  projects:
    - name: widget
      paths: /src/widget
`)
	_, subject := resolve(t, r, web("tracker.example.com", "", "browse", "[WID-42] make it work"))
	if subject != "WID-42" {
		t.Errorf("subject = %q, want WID-42", subject)
	}
	// One line, every ticket. That is the whole point of the capture group.
	_, subject = resolve(t, r, web("tracker.example.com", "", "browse", "[WID-43] and the other thing"))
	if subject != "WID-43" {
		t.Errorf("subject = %q, want WID-43", subject)
	}
	if _, subject := resolve(t, r, web("tracker.example.com", "", "browse", "no key here")); subject != "" {
		t.Errorf("subject = %q, want none", subject)
	}
}

func TestANamedSubjectBeatsTheGlobalOne(t *testing.T) {
	r := compile(t, `
attribution:
  subjects:
    - titles: '\b([A-Z]+-\d+)\b'
  projects:
    - name: film
      paths: /src/film
      subjects:
        - name: episode 14
          branches: 'ep-14'
`)
	e := cc("/src/film")
	e.GitBranch = "ep-14"
	e.Title = "WID-1 something else"
	project, subject := resolve(t, r, e)
	if project != "film" || subject != "episode 14" {
		t.Errorf("got %q / %q, want film / episode 14", project, subject)
	}
}

// A subject with no name and no capture group cannot call itself anything, and
// silently producing empty subjects is worse than saying so.
func TestASubjectMustBeAbleToNameItself(t *testing.T) {
	_, problems := parse(t, `
attribution:
  subjects:
    - titles: 'no group here'
`)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "capture group") {
		t.Fatalf("problems = %+v, want one about a capture group", problems)
	}
}

func TestFallbackKeepsOrDropsTheImportGuess(t *testing.T) {
	kept := compile(t, `
attribution:
  projects:
    - name: widget
      paths: /src/widget
`)
	if got, _ := resolve(t, kept, cc("/elsewhere/scratch")); got != "scratch" {
		t.Errorf("named %q, want scratch: leaving the key out must not take a name away", got)
	}

	dropped := compile(t, `
attribution:
  fallback: none
  projects:
    - name: widget
      paths: /src/widget
`)
	if got, _ := resolve(t, dropped, cc("/elsewhere/scratch")); got != "" {
		t.Errorf("named %q, want nothing", got)
	}
	if got, _ := resolve(t, dropped, cc("/src/widget")); got != "widget" {
		t.Errorf("named %q, want widget: the fallback is not the rules", got)
	}
}

func TestNoRulesAtAllChangesNothing(t *testing.T) {
	r, problems := New(config.Attribution{})
	if len(problems) != 0 {
		t.Fatalf("problems = %+v, want none", problems)
	}
	if got, _, _ := r.Resolve(cc("/src/widget")); got != "widget" {
		t.Errorf("named %q, want the import guess: no config file has to keep working", got)
	}
	if got, _, _ := r.Resolve(web("x.example.com", "", "", "")); got != "" {
		t.Errorf("named %q, want nothing", got)
	}
}

func TestWorkIsThreeValued(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: paid
      work: true
      paths: /src/paid
    - name: mine
      work: false
      paths: /src/mine
    - name: unsaid
      paths: /src/unsaid
`)
	cases := []struct {
		project        string
		work, declared bool
	}{
		{"paid", true, true},
		{"mine", false, true},
		{"unsaid", false, false},
		{"never heard of it", false, false},
	}
	for _, c := range cases {
		work, declared := r.Work(c.project)
		if work != c.work || declared != c.declared {
			t.Errorf("Work(%q) = %v, %v; want %v, %v", c.project, work, declared, c.work, c.declared)
		}
	}
}

// A line that can never match anything is reported rather than ignored: it
// looks exactly like a line that works until somebody checks the numbers.
func TestUnusableLinesAreReported(t *testing.T) {
	cases := []struct {
		doc  string
		want string
	}{
		{"attribution:\n  projects:\n    - name: x\n      keys: https://example.com/a\n", "URL"},
		{"attribution:\n  projects:\n    - name: x\n      keys: example.com/a/b\n", "one path segment"},
		{"attribution:\n  projects:\n    - name: x\n      keys: /src/thing\n", "path"},
		{"attribution:\n  projects:\n    - name: x\n      paths: src/thing\n", "absolute"},
		{"attribution:\n  projects:\n    - name: x\n", "no rule"},
		{"attribution:\n  projects:\n    - paths: /src/thing\n", "no name"},
		{"attribution:\n  never: https://example.com\n", "URL"},
	}
	for _, c := range cases {
		_, problems := parse(t, c.doc)
		if len(problems) == 0 {
			t.Errorf("no problem reported for:\n%s", c.doc)
			continue
		}
		found := false
		for _, p := range problems {
			if strings.Contains(p.Reason, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("problems %+v say nothing about %q, for:\n%s", problems, c.want, c.doc)
		}
	}
}

// A rule reads where an event happened and what the page was called. It must
// not read one source's fields on another source's event: a browser visit has
// no working directory, and a session log has no host.
func TestRulesDoNotCrossSources(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: by-path
      paths: /src/widget
    - name: by-key
      keys: widget.example.com
`)
	// A browser event whose host happens to look like a path, and a Claude
	// Code event in a directory named after the host.
	if got, _ := resolve(t, r, web("/src/widget", "", "", "")); got != "" {
		t.Errorf("a browser event matched a path rule: %q", got)
	}
	if got, _ := resolve(t, r, cc("/widget.example.com")); got != "widget.example.com" {
		t.Errorf("a Claude Code event matched a key rule: %q", got)
	}
}

// An address literal is the one host with colons of its own, and the report
// prints it without brackets. Splitting it on the last colon would give a host
// of "::" and a port of "1" — a rule that matches nothing, for ever, and warns
// about nothing. Both spellings of an address have to reach the same rule, for
// the same reason the browser ignore list canonicalises both sides.
func TestAddressKeys(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: loopback
      keys: ["::1", "[0:0:0:0:0:0:0:1]:3000", 192.0.2.10]
`)
	cases := []struct {
		e    event.Event
		want string
	}{
		{web("::1", "", "admin", ""), "loopback"},
		{web("::1", "3000", "", ""), "loopback"},
		{web("192.0.2.10", "", "", ""), "loopback"},
		// An address has no subdomains, so the entry 192.0.2.10 must not
		// swallow something that merely ends in it.
		{web("10", "", "", ""), ""},
		{web("192.0.2.11", "", "", ""), ""},
	}
	for _, c := range cases {
		if got, _ := resolve(t, r, c.e); got != c.want {
			t.Errorf("%s:%s named %q, want %q", c.e.Host, c.e.Port, got, c.want)
		}
	}
}

// A host has more than one spelling and a config entry is typed by hand, so
// both sides go through the browser source's own canonicalising call.
func TestHostSpellingIsCanonicalisedOnBothSides(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: widget
      keys: Example.COM.
`)
	for _, host := range []string{"example.com", "EXAMPLE.com", "example.com."} {
		if got, _ := resolve(t, r, web(host, "", "", "")); got != "widget" {
			t.Errorf("%q named %q, want widget", host, got)
		}
	}
}

// An empty expression matches every string there is. Written by hand it is
// always a mistake, and it is the one mistake that otherwise produces no error,
// no warning, and a report full of the wrong project.
func TestAnEmptyExpressionIsRefused(t *testing.T) {
	for _, doc := range []string{
		"attribution:\n  projects:\n    - name: x\n      titles: ''\n",
		"attribution:\n  projects:\n    - name: x\n      branches: ['']\n",
	} {
		r, problems := parse(t, doc)
		found := false
		for _, p := range problems {
			if strings.Contains(p.Reason, "matches everything") {
				found = true
			}
		}
		if !found {
			t.Errorf("problems %+v say nothing about an empty expression, for:\n%s", problems, doc)
		}
		if got, _ := resolve(t, r, web("anything.example.com", "", "", "a title")); got != "" {
			t.Errorf("an empty expression named %q", got)
		}
	}

	// A key with no value at all is a different thing: yaml leaves the field
	// absent rather than empty, so the rule has nothing in it and is reported
	// as having nothing in it.
	_, problems := parse(t, "attribution:\n  projects:\n    - name: x\n      branches:\n")
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "no rule") {
		t.Errorf("problems = %+v, want one about a rule that matches nothing", problems)
	}
}

// The three ways a key could quietly match nothing. Each of them is a line
// somebody would plausibly type, and each used to compile into a rule that
// looked fine and never fired.
func TestKeysThatCouldNeverMatchAreReported(t *testing.T) {
	cases := []struct {
		doc  string
		want string
	}{
		// A colon with something after it that is not a port.
		{"attribution:\n  projects:\n    - name: x\n      keys: 'example.com:'\n", "not a port"},
		{"attribution:\n  projects:\n    - name: x\n      keys: 'example.com:80O'\n", "not a port"},
		// A scheme's own port, which the source deliberately does not store.
		{"attribution:\n  projects:\n    - name: x\n      keys: 'example.com:443'\n", "not stored for https"},
	}
	for _, c := range cases {
		_, problems := parse(t, c.doc)
		found := false
		for _, p := range problems {
			if strings.Contains(p.Reason, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("problems %+v say nothing about %q, for:\n%s", problems, c.want, c.doc)
		}
	}

	// The default-port entry is warned about and kept: the same port on the
	// other scheme is stored, and that is somebody's odd server rather than a
	// mistake.
	r, _ := parse(t, "attribution:\n  projects:\n    - name: x\n      keys: 'example.com:443'\n")
	if got, _ := resolve(t, r, web("example.com", "443", "", "")); got != "x" {
		t.Errorf("named %q; the rule was dropped rather than warned about", got)
	}
}

// "10" is a legal host name and is not an address, so guarding only on the
// entry would let it swallow 192.0.2.10 through the subdomain rule. An address
// has no labels, so the test has to be on the host as well.
func TestANameNeverSwallowsAnAddress(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: ten
      keys: "10"
`)
	if got, _ := resolve(t, r, web("192.0.2.10", "", "", "")); got != "" {
		t.Errorf("the entry \"10\" took %q — an address has no labels to walk", got)
	}
	if got, _ := resolve(t, r, web("10", "", "", "")); got != "ten" {
		t.Errorf("named %q, want ten: the host itself still matches", got)
	}
}

// A directory needs the same escape a host has, and needs it more: a host with
// no rule is simply unnamed, while a directory with no rule is named by the
// last element of its path — a guess nobody wrote down.
func TestNeverPathsSilenceADirectory(t *testing.T) {
	r := compile(t, `
attribution:
  never:
    paths: ~/scratch
  projects:
    - name: widget
      paths: /src/widget
`)
	// The guess is still on everywhere else, which is the whole point of
	// silencing one directory rather than turning the fallback off.
	if got, _ := resolve(t, r, cc("/elsewhere/thing")); got != "thing" {
		t.Errorf("an unrelated directory lost its guess: %q", got)
	}
	// And it beats the fallback: refusing the path and then letting the last
	// element of the same path name it would leave the entry doing nothing.
	t.Setenv("HOME", "/home/somebody")
	r = compile(t, `
attribution:
  never:
    paths: ~/scratch
`)
	if got, _ := resolve(t, r, cc("/home/somebody/scratch")); got != "" {
		t.Errorf("named %q; a silenced path must beat the guess", got)
	}
	if !r.Covers(cc("/home/somebody/scratch")) {
		t.Error("the silenced directory is still uncovered; a decision was made about it")
	}
}

// Silencing names the one directory somebody looked at, not the tree under it.
// A rule that names a project wants the tree — one line for a checkout and
// everything inside it — but `report --unmatched` prints the home directory as
// a candidate to decide about, so a subtree here would take one paste to switch
// discovery off for the whole machine, silently and for good.
func TestNeverPathsNameOneDirectoryUnlessAskedForTheTree(t *testing.T) {
	r := compile(t, `
attribution:
  never:
    paths: /scratch
`)
	deeper := cc("/scratch/deeper")
	if got, _ := resolve(t, r, deeper); got != "deeper" {
		t.Errorf("named %q; only the directory written is silenced", got)
	}
	if r.Covers(deeper) {
		t.Error("a directory under a silenced one is covered; nobody has looked at it yet")
	}

	// The tree is a second entry, and a trailing star is a plain string prefix
	// rather than a subtree: it takes the siblings too, which is why the docs
	// spell out the two-entry form instead of the shorter one.
	tree := compile(t, `
attribution:
  never:
    paths: [/scratch, /scratch/*]
`)
	if got, _ := resolve(t, tree, deeper); got != "" {
		t.Errorf("named %q; the tree was asked for", got)
	}
	if !tree.Covers(deeper) {
		t.Error("the tree was asked for and is still uncovered")
	}
	if got, _ := resolve(t, tree, cc("/scratchpad")); got != "scratchpad" {
		t.Errorf("a sibling was silenced too: %q", got)
	}

	star := compile(t, `
attribution:
  never:
    paths: /scratch*
`)
	if got, _ := resolve(t, star, cc("/scratchpad")); got != "" {
		t.Errorf("named %q; a trailing star is a plain prefix and takes siblings", got)
	}
}

// It takes the trace away, not the event — the same rule as for a key. A
// branch or a title still names what it names.
func TestNeverPathsLeaveTheOtherFieldsAlone(t *testing.T) {
	r := compile(t, `
attribution:
  never:
    paths: /scratch
  projects:
    - name: by-path
      paths: /scratch
    - name: by-branch
      branches: '^release/'
`)
	plain := cc("/scratch")
	if got, _ := resolve(t, r, plain); got != "" {
		t.Errorf("named %q, want nothing", got)
	}
	branched := cc("/scratch")
	branched.GitBranch = "release/7"
	if got, _ := resolve(t, r, branched); got != "by-branch" {
		t.Errorf("named %q, want by-branch: the branch is still evidence", got)
	}
}

// A path written under keys and a key written under paths are both mistakes
// somebody will make, and both have somewhere to go now.
func TestNeverEntriesAreCheckedPerList(t *testing.T) {
	_, problems := parse(t, "attribution:\n  never:\n    keys: ~/scratch\n")
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "never.paths") {
		t.Errorf("problems = %+v, want one pointing at never.paths", problems)
	}
	_, problems = parse(t, "attribution:\n  never:\n    paths: example.com\n")
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "absolute") ||
		!strings.Contains(problems[0].Reason, "the never list") {
		t.Errorf("problems = %+v, want one about an absolute path", problems)
	}
}

// Silencing a directory must not silence the checkout inside it. `--unmatched`
// prints /home/u as a directory to decide about, and pasting it in used to
// take every rule under it away at once — the report survived, but every
// project in the home directory collapsed to whatever its browser keys knew.
func TestNeverLosesToAMoreSpecificRuleOfItsOwnKind(t *testing.T) {
	r := compile(t, `
attribution:
  never:
    paths: /home/u
    keys: example.com
  projects:
    - name: widget
      paths: /home/u/src/widget
      keys: docs.example.com
`)
	cases := []struct {
		e    event.Event
		want string
	}{
		// Longer path, longer key: the rule speaks for itself.
		{cc("/home/u/src/widget"), "widget"},
		{cc("/home/u/src/widget/api"), "widget"},
		{web("docs.example.com", "", "", ""), "widget"},
		// Nothing more specific: silenced, and the guess does not creep back.
		// Only the directory written, so what is under it keeps the guess and
		// stays in the list of things to decide about.
		{cc("/home/u"), ""},
		{cc("/home/u/scratch"), "scratch"},
		{web("example.com", "", "", ""), ""},
		{web("other.example.com", "", "", ""), ""},
	}
	for _, c := range cases {
		if got, _ := resolve(t, r, c.e); got != c.want {
			t.Errorf("%s%s named %q, want %q", c.e.CWD, c.e.Host, got, c.want)
		}
	}
}

// The floor is per kind, so a never key never reaches a path rule and a never
// path never reaches a key rule. They describe different traces.
func TestNeverDoesNotReachTheOtherKind(t *testing.T) {
	r := compile(t, `
attribution:
  never:
    paths: /scratch
  projects:
    - name: widget
      keys: widget.example.com
`)
	e := web("widget.example.com", "", "", "")
	if got, _ := resolve(t, r, e); got != "widget" {
		t.Errorf("a never path suppressed a key rule: %q", got)
	}
}

// A rule that a never entry covers just as specifically can never fire. It sits
// in the file looking like a rule, and the only sign is a row that is not
// there — which is exactly the failure this list of problems exists to catch.
func TestARuleARefusalCoversIsReported(t *testing.T) {
	cases := []struct{ doc, want string }{
		// The same key refused and claimed.
		{`
attribution:
  never: tracker.example.com
  projects:
    - name: widget
      keys: tracker.example.com
`, "tracker.example.com"},
		// A silence written as a plain prefix, over a rule on the same prefix.
		{`
attribution:
  never:
    paths: /home/u*
  projects:
    - name: widget
      paths: /home/u
`, "/home/u"},
	}
	for _, c := range cases {
		_, problems := parse(t, c.doc)
		found := false
		for _, p := range problems {
			if strings.Contains(p.Reason, "can never fire") && p.Entry == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("problems %+v say nothing about %q, for:%s", problems, c.want, c.doc)
		}
	}

	// And a refusal that is broader than the rule is not a problem: that is the
	// pair the floor exists for.
	compile(t, `
attribution:
  never:
    keys: example.com
    paths: /home/u*
  projects:
    - name: widget
      keys: docs.example.com
      paths: /home/u/src/widget
`)
}

// A subject goes through the same floors as a project and dies the same way, so
// the same thing has to be said about it.
func TestARefusalThatKillsASubjectIsReported(t *testing.T) {
	_, problems := parse(t, `
attribution:
  never: tracker.example.com
  subjects:
    - name: tickets
      keys: tracker.example.com
  projects:
    - name: widget
      paths: /src/widget
      subjects:
        - name: reviews
          keys: tracker.example.com
`)
	var said []string
	for _, p := range problems {
		if strings.Contains(p.Reason, "can never fire") {
			said = append(said, p.Reason)
		}
	}
	if len(said) != 2 {
		t.Fatalf("got %d reports, want one per subject: %+v", len(said), problems)
	}
	for _, want := range []string{`subject "tickets"`, `subject "reviews" of project "widget"`} {
		found := false
		for _, r := range said {
			if strings.Contains(r, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("nothing said about the subject %q: %v", want, said)
		}
	}
}

// The third return says whether a rule named this, or whether the name is the
// source's own guess carried through.
//
// It decides confirmed_stretch.ground — the column that exists so somebody can
// ask, months later, whether half an hour rests on a rule they wrote or on
// basename(cwd). Every other test in this file throws it away, and the two
// that read it run against a stub with its own hardcoded answer, so the real
// implementation could return false for everything and nothing would notice.
func titled(e event.Event, title string) event.Event {
	e.Title = title
	return e
}

func TestResolveSaysWhetherARuleNamedIt(t *testing.T) {
	r := compile(t, `
attribution:
  projects:
    - name: widget
      paths: [/src/widget]
      subjects:
        - name: review
          titles: ['^Merge request']
    - name: reading
      keys: [ops.example.invalid]
`)
	for _, c := range []struct {
		what    string
		e       event.Event
		project string
		subject string
		byRule  bool
	}{
		{"a path rule", cc("/src/widget"), "widget", "", true},
		{"a key rule", web("ops.example.invalid", "", "board", ""), "reading", "", true},
		{"a subject inside a project", titled(cc("/src/widget"), "Merge request !12"), "widget", "review", true},
		{"the source's own guess", event.Event{Source: "claude-code", Project: "guessed", CWD: "/elsewhere"},
			"guessed", "", false},
		{"nothing at all", event.Event{Source: "browser", Host: "unknown.invalid"}, "", "", false},
	} {
		project, subject, byRule := r.Resolve(c.e)
		if project != c.project || subject != c.subject {
			t.Errorf("%s: named %q/%q, want %q/%q", c.what, project, subject, c.project, c.subject)
		}
		if byRule != c.byRule {
			t.Errorf("%s: byRule = %v, want %v", c.what, byRule, c.byRule)
		}
	}
}
