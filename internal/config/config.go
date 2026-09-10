// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package config reads the YAML file spoor keeps under $XDG_CONFIG_HOME.
//
// There is no file by default and no file is ever written: an absent config
// means "nothing configured", not an error. Everything machine dependent
// lives here rather than in the source, which is why the repository contains
// no personal paths.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole file. It grows one section per stage.
type Config struct {
	Browser     Browser     `yaml:"browser"`
	Calendar    Calendar    `yaml:"calendar"`
	Report      Report      `yaml:"report"`
	Attribution Attribution `yaml:"attribution"`
}

// Calendar configures the one source that can reach the network.
//
// Everywhere else in spoor a setting decides how something on this machine is
// read. This one decides whether the machine talks to anybody at all, so it is
// off unless it is switched on, and switching it on takes two deliberate acts:
// this flag, and a file holding an address that is not in this file.
type Calendar struct {
	// Enabled is the network switch, and it is here rather than implied by the
	// presence of a calendar so that the promise can be read rather than
	// deduced. Somebody checking whether this tool goes out should find the
	// answer on one line, not work it out from whether a file exists.
	Enabled bool `yaml:"enabled"`

	// Sources are the calendars to read, in the order written. More than one
	// is the normal case — a work calendar and a shared team one — and the
	// plural is here from the start because the alternative is a dedup key
	// that has to change later, which re-imports the whole history as new
	// rows.
	Sources []CalendarSource `yaml:"sources"`
}

// CalendarSource is one feed.
type CalendarSource struct {
	// ID names the calendar. It is part of the identity of every event read
	// from it, so two calendars must not share one — see Validate.
	ID string `yaml:"id"`

	// URLFile is the path to a file holding the feed's address and nothing
	// else. The address itself is deliberately not a setting: an iCalendar
	// URL is a bearer credential that reads the whole calendar, and this file
	// is the artefact people paste into issues, show in chat and commit to
	// dotfile repositories. ssh keeps the key out of ssh_config for the same
	// reason, and git keeps it out of .gitconfig.
	//
	// Left out, it defaults to a file named after the id, next to this one.
	URLFile string `yaml:"url_file"`

	// File is a downloaded .ics read straight off the disk, for a machine
	// that is not to go out at all. The same parser, a different way in.
	// Mutually exclusive with URLFile.
	File string `yaml:"file"`

	// Me is the addresses that are the reader. A feed says which attendee
	// declined an invitation; it does not say which attendee is you. Without
	// this, a meeting you turned down is imported like any other, because the
	// alternative is guessing. Addresses are read to answer that one question
	// and are never stored.
	Me Strings `yaml:"me"`
}

// Validate reports what is wrong with the calendar section.
//
// Fatal comes first and is fatal on purpose. A repeated id is not a style
// problem: the id is part of every event's identity, so two calendars sharing
// one means the second calendar's meetings collide with the first's on the
// unique index and are dropped — no error, no warning, an import that reports
// success and quietly holds half the meetings. That is the exact failure this
// project has already paid for once.
//
// The rest are warnings, and they exist because the worst thing a config line
// can do is parse, look right and never fire.
func (c Calendar) Validate() (fatal []Problem, warnings []Problem) {
	seen := map[string]int{}
	for i, s := range c.Sources {
		where := fmt.Sprintf("calendar #%d", i+1)
		if s.ID != "" {
			where = fmt.Sprintf("calendar %q", s.ID)
		}
		switch {
		case s.ID == "":
			fatal = append(fatal, Problem{where,
				"has no id; the id is part of the identity of every meeting read from it"})
		case seen[s.ID] > 0:
			fatal = append(fatal, Problem{where, fmt.Sprintf(
				"is also the id of calendar #%d; two calendars sharing an id would "+
					"silently drop the second one's meetings as duplicates", seen[s.ID])})
		case !plainName(s.ID):
			// The id names a file when url_file is left out, so it has to be
			// a name and not a path. Refused rather than escaped, because an
			// id is written once by hand and a rule is easier to read than a
			// transformation.
			fatal = append(fatal, Problem{where,
				"has an id that is not a plain name; letters, digits, '-', '_' and '.' only"})
		}
		seen[s.ID] = i + 1

		if s.URLFile != "" && s.File != "" {
			fatal = append(fatal, Problem{where,
				`has both "url_file" and "file"; it is one or the other`})
		}
		if s.File != "" && !c.Enabled {
			continue // a local file needs no network and no switch
		}
		if !c.Enabled {
			warnings = append(warnings, Problem{where,
				"is configured but calendar.enabled is false, so it is not read"})
		}
	}
	if c.Enabled && len(c.Sources) == 0 {
		warnings = append(warnings, Problem{"calendar",
			"is enabled but lists no sources, so nothing is read"})
	}
	return fatal, warnings
}

// plainName reports whether s can stand as one path element.
func plainName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// Problem is one thing wrong with the file: what it is about, and what is
// wrong with it.
type Problem struct {
	Entry  string
	Reason string
}

func (p Problem) String() string { return p.Entry + " " + p.Reason }

// Report configures how events are turned into a day.
//
// Both thresholds are here rather than in a constant because both are
// measurements, and a measurement gets remeasured. The clustering one was
// taken from the density of pauses between events on two weeks of one
// person's data, and the buckets that decided it hold four to eleven
// observations each. Nobody should have to rebuild the binary to try another
// number.
type Report struct {
	// ClusterGap is how far apart two events can be and still belong to the
	// same block of work. Left out — or written as 0s — means the default,
	// ten minutes: a threshold of nothing would make every event a block of
	// its own, which nobody means by writing zero.
	ClusterGap Duration `yaml:"cluster_gap"`

	// AttentionWindow is the half-width of the window a moment of human
	// attention casts around itself: a prompt typed at 12:00 with a window of
	// 5m means the person was there from 11:55 to 12:05. Windows are merged,
	// and what is left of a block of work belongs to the agent rather than to
	// the person.
	//
	// Left out — or written as 0s — it follows ClusterGap: half of it. Not a
	// constant of its own, so that the two cannot drift apart when the
	// threshold is measured again.
	//
	// Unlike Head and Tail, zero here cannot mean zero: a window of nothing
	// would make every second of a block the agent's, and there would be no
	// way left to say "just use the default".
	AttentionWindow Duration `yaml:"attention_window"`

	// Head is how long writing a prompt takes, added before a block that opens
	// with one. Tail is how long reading the last answer takes, added after a
	// block that had a person in it anywhere. Neither is added to a block of
	// nothing but agent output: there was nobody there to write or to read.
	//
	// A pointer each, because leaving the key out has to mean the default
	// while writing "0s" has to mean "add nothing at all" — and this is the
	// one setting where somebody will genuinely want the second.
	Head *Duration `yaml:"head"`
	Tail *Duration `yaml:"tail"`

	// CountBackground adds the time the agent worked alone to the totals.
	// Off by default: the report says how long it was on a line of its own,
	// and whether that is your working time is your call, not the tool's.
	CountBackground bool `yaml:"count_background"`
}

// Duration is a time.Duration written the way a person writes one: "10m",
// "1h30m", "45s". yaml.v3 would otherwise want the number of nanoseconds.
type Duration time.Duration

// UnmarshalYAML reads a duration from a string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: want a duration like \"10m\", got %s", node.Line, node.Tag)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration like \"10m\"", node.Line, s)
	}
	if parsed < 0 {
		return fmt.Errorf("line %d: %q is negative", node.Line, s)
	}
	*d = Duration(parsed)
	return nil
}

// OrDefault is Or for a key that may legitimately be set to zero: an absent
// key gives def, and a present one gives whatever it says, including 0s.
func (d *Duration) OrDefault(def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return time.Duration(*d)
}

// Browser configures the browser history source.
type Browser struct {
	// Ignore lists domains that must never reach the database. An entry
	// matches the host itself and any subdomain of it, so "example.com"
	// covers "www.example.com" as well.
	//
	// The filter runs before the insert, not before the display. A visit to
	// an ignored domain is not stored, quietly kept and hidden — it is not
	// stored at all.
	Ignore []string `yaml:"ignore"`

	// History lists browser history databases to read on top of the ones
	// found automatically. Which browser a file belongs to is decided by its
	// name: "History" is read as Chrome, "places.sqlite" as Firefox. That is
	// how a Chromium fork or a Firefox fork in an unusual place can be tried
	// without spoor having to know it exists. Only Chrome has been run
	// against a real profile, so trying is all it is.
	History []string `yaml:"history"`
}

// Attribution is the dictionary: which project an event belongs to, and which
// accumulating thing inside that project.
//
// It is read when a report is built, never when events are imported. Rules
// therefore apply to everything in the database, including what was imported
// years before the rule was written — which is the whole reason collecting and
// reporting are two commands. Change a line here and run the report again.
//
// The target was twenty lines covering ninety per cent of events. Measured, it
// holds for directories and not for browsing: about fifteen path rules named
// every working directory on the machine this was written on, while a hundred
// lines named 83.6% of events and the rest is not addressable by more lines —
// over half of what was left is on keys the dictionary refuses on purpose, and
// the remainder is a tail of hundreds of hosts seen once each.
//
// So a dictionary that has to list every host somebody visits has stopped
// being a dictionary, and that is not a failure: the block a visit falls in is
// what names it, and it does.
type Attribution struct {
	// Fallback says what happens to a Claude Code event no rule names:
	// "cwd-basename" keeps the crude guess made at import time, "none" leaves
	// it to be inherited from the block around it like a browser visit.
	//
	// The default is the crude guess, so that adding a first rule cannot take
	// a name away from an event that already had one. It is the blunt control:
	// a single directory is silenced with never.paths below, which leaves the
	// guess working everywhere else so that a new directory still shows up.
	Fallback Fallback `yaml:"fallback"`

	// Never lists traces that must not name a project: browser keys under Keys,
	// working directories under Paths. A search engine, a wiki root or an issue
	// tracker's home serves every project at once — one key on real data carried
	// a sixth of all browsing — and the right answer for those is no project
	// rather than the wrong project. What they were
	// about is decided by the block around them, which is where the evidence
	// actually is.
	//
	// It takes the key away, not the event. A rule reading the page title
	// still applies, and should: the title of a tracker page names the board or
	// the repository, which is the thing the host does not. That is what makes
	// an issue tracker work here without an API, and it is why this list and
	// Titles below are not in conflict.
	//
	// A path here names the one directory written, not the tree under it — the
	// opposite of a path under a project. See pathPattern in the rules package
	// for why. The tree is two entries, "~/x" and "~/x/*": a trailing "*" is a
	// plain string prefix here as everywhere else, so "~/x*" would take
	// "~/xylophone" as well.
	//
	// An entry loses to a more specific rule of its own kind, and wins over a
	// less specific one. That is what makes both directions expressible: a bare
	// host under Keys matches every segment of it, so a narrower entry here
	// carves one segment out — and silencing a whole directory does not take
	// away the rule on the one checkout inside it, which matters because
	// `report --unmatched` prints the home directory as something to decide
	// about.
	//
	// The everyday use is neither: a trace nobody has written a rule for is
	// unnamed in any case, so putting it here says so deliberately, which is
	// what keeps it out of the list `report --unmatched` prints.
	//
	// Entries have the same shape as Keys and Paths below.
	Never Never `yaml:"never"`

	// Subjects are tried inside every project, after that project's own list.
	// One line here — an issue key in a page title — covers every tracker,
	// every repository host and every branch at once.
	Subjects []Subject `yaml:"subjects"`

	// Projects are tried in the order written, but a more specific rule wins
	// over a less specific one whatever the order: a longer path or a longer
	// browser key beats a shorter one, and either beats a regular expression.
	// Order decides only between rules that are equally specific.
	Projects []Project `yaml:"projects"`
}

// Project is one entry of the dictionary: a name, the rules that give an event
// that name, and the things inside it worth counting separately.
type Project struct {
	// Name is what the report calls it. It replaces the name guessed at import
	// time, which is why one project spread over several directories comes out
	// as one row.
	Name string `yaml:"name"`

	// Work marks the project as work rather than personal. Left out, it says
	// nothing — which is not the same as "personal". The measurement that
	// motivated it found personal projects carrying more than twice the hours
	// of work ones, so a tool that assumed either way would be wrong about
	// most of somebody's day.
	Work *bool `yaml:"work"`

	// Paths match the working directory of a Claude Code event. An entry names
	// a directory and matches it and everything under it, so a checkout and a
	// subdirectory opened separately are one project — and two directories
	// that merely end in the same word are not. A trailing "*" makes it a
	// plain prefix instead, for the temporary worktrees an agent creates with
	// a random suffix.
	//
	// "~" is the home directory. Nothing else is expanded: a config that
	// interpolated environment variables would read differently under cron.
	Paths Strings `yaml:"paths"`

	// Keys match a browser visit. The key is host[:port][/first-segment] —
	// exactly what the report prints in its evidence column, so a line of the
	// report can be pasted here. An entry without a port matches any port, an
	// entry without a segment matches any segment, and a bare host also
	// matches its subdomains.
	Keys Strings `yaml:"keys"`

	// Branches are regular expressions over the git branch. Useful as a last
	// resort only: on real data 57% of branches are "HEAD" or "master", which
	// name nothing. A branch says much more about the subject than about the
	// project.
	Branches Regexps `yaml:"branches"`

	// Titles are regular expressions over the page title. This is what stands
	// in for an issue tracker integration: the issue key lives in a path
	// segment spoor deliberately does not store, and in the title, which it
	// does.
	Titles Regexps `yaml:"titles"`

	// Subjects are the second level of grouping, and the last: there is no
	// third. An episode, a level, a feature, a ticket — something that spans
	// weeks and is not a project of its own.
	Subjects []Subject `yaml:"subjects"`
}

// Subject is the accumulating thing inside a project: episode 14, level 3, one
// feature, one ticket.
//
// Two levels, deliberately, and no more. A tree of arbitrary depth would need
// a way to ask about a level of it, and every report would have to say which
// level it was answering about; the question people actually ask is "how long
// did this one thing take", and one level below the project answers it.
type Subject struct {
	// Name is what the subject is called. Left out, the first capture group of
	// whichever expression matched becomes the name — which is how one line
	//
	//	subjects: [{titles: '\b([A-Z]+-\d+)\b'}]
	//
	// gives every ticket a subject of its own without listing any of them.
	Name string `yaml:"name"`

	Paths    Strings `yaml:"paths"`
	Keys     Strings `yaml:"keys"`
	Branches Regexps `yaml:"branches"`
	Titles   Regexps `yaml:"titles"`
}

// Never is the two lists of traces that must not name a project.
//
// A directory needs this as much as a host does, and for longer: a scratch
// directory nobody has written a rule for is named by the last element of its
// path, which is a guess, and the only way to refuse that guess used to be
// Fallback — which turns it off everywhere at once. A path here is the way to
// say "I have decided about this one" while new directories still surface.
//
// A path on this list also beats Fallback. Otherwise the guess would name what
// the list just refused, and the entry would do nothing at all.
type Never struct {
	Keys  Strings `yaml:"keys"`
	Paths Strings `yaml:"paths"`
}

// UnmarshalYAML accepts the two lists, and also a bare list of keys — which is
// what this section was before paths existed, and what is written in every
// config that predates them.
//
// The mapping is walked by hand rather than handed to Decode, because
// Node.Decode does not carry the decoder's KnownFields setting: a misspelt key
// inside here would be silently ignored, and this file promises the opposite.
func (n *Never) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		if err := n.Keys.UnmarshalYAML(node); err != nil {
			return fmt.Errorf("line %d: want a list of keys, or a mapping of \"keys\" and \"paths\"", node.Line)
		}
		return nil
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name, value := node.Content[i], node.Content[i+1]
		var into *Strings
		switch name.Value {
		case "keys":
			into = &n.Keys
		case "paths":
			into = &n.Paths
		default:
			return fmt.Errorf("line %d: no such key %q under never; there are \"keys\" and \"paths\"",
				name.Line, name.Value)
		}
		// yaml.v3 refuses a repeated key by itself; walking the mapping here
		// means saying so here too, or the second one would silently win.
		if seen[name.Value] {
			return fmt.Errorf("line %d: %q is given twice under never", name.Line, name.Value)
		}
		seen[name.Value] = true
		// A key with no value is an empty list, not a list holding one empty
		// entry. Decoding null through Strings would produce the latter, and
		// then every run would warn about a rule nobody wrote.
		if value.Tag == "!!null" {
			continue
		}
		if err := into.UnmarshalYAML(value); err != nil {
			return err
		}
	}
	return nil
}

// Fallback is what an unmatched Claude Code event keeps.
type Fallback string

const (
	// FallbackCWDBasename keeps the last element of the working directory, the
	// guess made at import time. The default.
	FallbackCWDBasename Fallback = "cwd-basename"
	// FallbackNone drops it, leaving the event to be named by the block around
	// it or not at all.
	FallbackNone Fallback = "none"
)

// UnmarshalYAML refuses a value that is neither, rather than quietly keeping
// the default: a misspelt "cwd_basename" would otherwise silently mean the
// opposite of what somebody who bothered to write the key wanted.
func (f *Fallback) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: want %q or %q, got %s", node.Line, FallbackCWDBasename, FallbackNone, node.Tag)
	}
	switch Fallback(s) {
	case FallbackCWDBasename, FallbackNone:
		*f = Fallback(s)
		return nil
	}
	return fmt.Errorf("line %d: %q is not a fallback; there are %q and %q",
		node.Line, s, FallbackCWDBasename, FallbackNone)
}

// Or returns the fallback, or the default when the key was left out.
func (f Fallback) Or(def Fallback) Fallback {
	if f == "" {
		return def
	}
	return f
}

// Strings is a list that may be written as one value. Most rules have exactly
// one path or one key, and "paths: ~/src/thing" reads better than a sequence
// of one — which matters when the whole file is meant to be twenty lines.
type Strings []string

// UnmarshalYAML accepts a scalar or a sequence.
func (s *Strings) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var one string
		if err := node.Decode(&one); err != nil {
			return fmt.Errorf("line %d: want a string or a list of them, got %s", node.Line, node.Tag)
		}
		*s = Strings{one}
		return nil
	}
	var many []string
	if err := node.Decode(&many); err != nil {
		return fmt.Errorf("line %d: want a string or a list of them, got %s", node.Line, node.Tag)
	}
	*s = many
	return nil
}

// Regexp is a regular expression compiled while the config is read, so that a
// broken one is a config error naming its line rather than a rule that
// silently matches nothing.
type Regexp struct{ *regexp.Regexp }

// UnmarshalYAML compiles the expression.
func (r *Regexp) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: want a regular expression, got %s", node.Line, node.Tag)
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a regular expression: %w", node.Line, s, err)
	}
	r.Regexp = re
	return nil
}

// Regexps is Strings for expressions: one may be written on its own.
type Regexps []Regexp

// UnmarshalYAML accepts a scalar or a sequence.
func (r *Regexps) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var one Regexp
		if err := one.UnmarshalYAML(node); err != nil {
			return err
		}
		*r = Regexps{one}
		return nil
	}
	var many []Regexp
	if err := node.Decode(&many); err != nil {
		return err
	}
	*r = many
	return nil
}

// Load reads the config file. A missing file yields a zero Config and no
// error: running without one has to keep working.
func Load(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}

	// KnownFields makes a misspelled key an error rather than a setting that
	// silently does nothing. An ignore list that is quietly not applied is
	// the worst possible failure for this particular file.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		// A file that holds only comments, or nothing at all, decodes to EOF.
		// That is an empty config, not a broken one.
		if errors.Is(err, io.EOF) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
