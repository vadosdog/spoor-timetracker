// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package configfile adds rules to the config file without taking it over.
//
// The config is written by hand. It has comments in it, the order of its rules
// is a decision somebody made, and it is the only place the dictionary lives.
// A tool that rewrites it silently is a tool you stop trusting with it — and
// the whole of this stage rests on writing to it, because an answer that does
// not become a rule is hand marking, which the concept forbids.
//
// So: the file is located with a YAML parser and edited as text. What is added
// is the lines that were added, and every other byte of the file is the byte it
// was. Re-encoding the parsed tree would keep the comments and the order —
// yaml.v3 does carry both — and would still re-indent, requote and reflow
// everything the author wrote, which for a file this personal is the same
// insult in slower motion.
//
// Three more rules, all about the same thing:
//
//   - the exact text is shown before it is written;
//   - the write is atomic, a copy is kept, and the permissions are the ones
//     that were there;
//   - a file that changed on disk since it was read is not written to at all.
package configfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The four kinds of rule, and the two top-level lists a decision about a trace
// can go on. Spelled exactly as they are in the file, because they are written
// into it.
const (
	Paths    = "paths"
	Keys     = "keys"
	Branches = "branches"
	Titles   = "titles"

	Never     = "never"
	Ambiguous = "ambiguous"
)

// Addition is one rule to add: what it matches on, and where it goes.
type Addition struct {
	// List is Never or Ambiguous for a decision about a trace, and empty for a
	// rule that names something.
	List string
	// Project and Subject say where a naming rule goes. A subject with no
	// project is a subject of every project, which is the top-level list.
	Project string
	Subject string
	// Kind is one of the four above, Value is what it matches on.
	Kind  string
	Value string
	// Since is the day the rule starts applying, if it is not to apply to
	// everything already collected. Zero for a plain rule.
	Since time.Time
	// Work is written only when the project has to be created, and only when
	// it says something: leaving it out is a third answer, not a default.
	Work *bool
}

// File is a config file open for editing.
type File struct {
	path string
	// real is path with every symbolic link resolved, and is what Save writes
	// to. A config kept in a dotfiles repository and linked into place is the
	// normal home for a file this package calls hand-written; renaming onto
	// the link would replace it with a regular file, leave the copy in the
	// repository frozen at whatever it last said, and give no sign that the
	// two had parted. Messages keep using path, which is the name the person
	// typed.
	real string
	// lines is the file as it stands, one string per line, no terminators.
	lines []string
	doc   *yaml.Node
	mode  fs.FileMode
	// sum is what was read, so that a file somebody edited in the meantime is
	// refused rather than overwritten.
	sum      string
	existed  bool
	backedUp bool
}

// Load reads the file. A file that is not there is an empty document, which is
// what running without a config means everywhere else in the program.
func Load(path string) (*File, error) {
	f := &File{path: path, real: resolve(path), mode: 0o600}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f, f.parse()
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if info, statErr := os.Stat(path); statErr == nil {
		f.mode = info.Mode().Perm()
	}
	f.real = resolve(path)

	f.existed = true
	f.sum = sum(data)
	f.lines = splitLines(string(data))
	return f, f.parse()
}

// resolve follows symbolic links to the file that will actually be written.
//
// EvalSymlinks answers this for a link that points at something, and refuses a
// link whose target is not there — which is the state nobody would notice: the
// config has been moved inside the dotfiles repository, the link dangles, and
// renaming onto it would replace the link with a regular file and quietly end
// the arrangement. So a dangling link is followed by hand, one hop at a time,
// with a bound in case somebody has made a ring of them.
func resolve(path string) string {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target
	}
	for range 16 {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return path
		}
		target, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return path
}

// Text is the file as it now stands, with the additions applied.
func (f *File) Text() string {
	if len(f.lines) == 0 {
		return ""
	}
	return strings.Join(f.lines, "\n") + "\n"
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (f *File) parse() error {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(f.Text()), &doc); err != nil {
		return fmt.Errorf("%s: %w", f.path, err)
	}
	f.doc = &doc
	return nil
}

// root is the top-level mapping, or nil for an empty file.
func (f *File) root() *yaml.Node {
	if f.doc == nil || len(f.doc.Content) == 0 {
		return nil
	}
	if n := f.doc.Content[0]; n.Kind == yaml.MappingNode {
		return n
	}
	return nil
}

// mapValue finds a key in a mapping. Nil when the mapping or the key is not
// there, which the callers treat the same way: something to create.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// named finds the entry of a sequence whose "name" is name.
func named(seq *yaml.Node, name string) *yaml.Node {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range seq.Content {
		if v := mapValue(item, "name"); v != nil && v.Value == name {
			return item
		}
	}
	return nil
}

// Preview is exactly the text Add would put in the file, and where. Nothing is
// changed.
//
// The file is edited by hand and read by eye, so an addition that cannot be
// shown before it happens is an addition nobody agreed to. This is the same
// code path Add takes, not a description of it: showing one thing and writing
// another is the failure this is meant to prevent.
func (f *File) Preview(a Addition) (text string, err error) {
	clone := &File{
		path: f.path, real: f.real, lines: append([]string(nil), f.lines...),
		mode: f.mode, sum: f.sum, existed: f.existed,
	}
	if err := clone.parse(); err != nil {
		return "", err
	}
	before := clone.snapshot()
	if _, err := clone.add(a); err != nil {
		return "", err
	}
	// Every line the file gained, not only the item itself. An addition can
	// have to create the section, the project entry and its `work:` line
	// before there is a list to put anything in, and those are lines somebody
	// is about to have in their config: showing the one line that was asked
	// for and writing four is the failure this method exists to prevent.
	return strings.Join(gained(before, clone.lines), "\n"), nil
}

// gained is the lines of after that are not in before, in order. A textual
// edit can also rewrite a line in place — growing `paths: ~/one` into a list
// does — and a rewritten line is a new line by this reckoning, which is right:
// it is a line that will look different afterwards.
func gained(before, after []string) []string {
	var out []string
	i := 0
	for _, line := range after {
		if i < len(before) && before[i] == line {
			i++
			continue
		}
		// The line may simply be further down: what is between here and there
		// was inserted above it and has already been emitted. Without this,
		// the first rewritten line makes every line after it mismatch, and the
		// preview claims the whole rest of the file is being added.
		if j := indexFrom(before, line, i+1); j >= 0 {
			i = j + 1
			continue
		}
		out = append(out, line)
	}
	return out
}

// indexFrom is the first occurrence of line in lines at or after start.
func indexFrom(lines []string, line string, start int) int {
	for j := start; j < len(lines); j++ {
		if lines[j] == line {
			return j
		}
	}
	return -1
}

// Add puts the rule in. The file on disk is not touched until Save.
//
// It either lands whole or leaves nothing. Every shape here is a textual edit
// made in several steps — the section, the project entry, the list, the item —
// and half of them can fail after the earlier ones have already changed the
// lines. Returning an error on top of a half-written entry is worse than the
// error: the caller reports the failure, the next answer calls Save, and an
// empty project nobody agreed to is committed to the file. A project entry
// with an empty list is precisely the rule that parses, looks right and never
// fires.
func (f *File) Add(a Addition) error {
	was := f.snapshot()
	if _, err := f.add(a); err != nil {
		return errors.Join(err, f.restore(was))
	}
	return nil
}

// snapshot is the text as it now stands, for putting back.
func (f *File) snapshot() []string {
	return append([]string(nil), f.lines...)
}

// restore undoes an edit that failed part way through, node tree and all: the
// tree holds line numbers into the text it was parsed from, so putting the
// lines back without reparsing would leave every later edit aimed at the wrong
// line.
func (f *File) restore(lines []string) error {
	f.lines = lines
	if err := f.parse(); err != nil {
		// The text being put back is text that parsed a moment ago, so this
		// cannot happen from a failed edit alone. If it ever does, the
		// in-memory file is no longer something to write, and saying so is all
		// that is left.
		return fmt.Errorf("%s: could not be put back as it was: %w", f.path, err)
	}
	return nil
}

func (f *File) add(a Addition) ([]string, error) {
	if err := check(a); err != nil {
		return nil, err
	}
	list, indent, err := f.listFor(a)
	if err != nil {
		return nil, err
	}
	entry, err := render(a)
	if err != nil {
		return nil, err
	}
	was := len(list.Content)
	lines, err := f.appendTo(list, indent, entry)
	if err != nil {
		return nil, err
	}
	// And the list actually gained an item. Every shape here is a textual
	// edit, and a textual edit can land somewhere that still parses and means
	// nothing — a "]" inside a trailing comment is enough. A rule written into
	// a comment is a rule that parses, looks right and never fires, which is
	// the one failure this package exists to avoid.
	if grown := f.locate(placeOf(a)); grown != nil {
		if got := mapValue(grown, a.Kind); got != nil && len(got.Content) <= was && was > 0 {
			return nil, fmt.Errorf(
				"adding %q to %s did not change the list — the line it went on is not "+
					"something spoor can edit safely; add it by hand", a.Value, a.Kind)
		}
	}
	return lines, nil
}

// placeOf is where an addition goes, for looking the list up again after the
// file has been re-parsed.
func placeOf(a Addition) where {
	return where{list: a.List, project: a.Project, subject: a.Subject}
}

func check(a Addition) error {
	switch a.Kind {
	case Paths, Keys, Branches, Titles:
	default:
		return fmt.Errorf("no such kind of rule as %q; there are %s, %s, %s and %s",
			a.Kind, Paths, Keys, Branches, Titles)
	}
	if a.Value == "" {
		return errors.New("a rule with nothing to match on would match nothing")
	}
	switch a.List {
	case "":
	case Never, Ambiguous:
		if a.Kind != Paths && a.Kind != Keys {
			return fmt.Errorf("%s takes %s and %s; a decision about a trace is about where "+
				"something happened, not about what a piece of text looked like",
				a.List, Paths, Keys)
		}
		if a.Project != "" || a.Subject != "" {
			return fmt.Errorf("%s is a top-level list; it does not belong to a project", a.List)
		}
	default:
		return fmt.Errorf("no such list as %q; there are %s and %s", a.List, Never, Ambiguous)
	}
	if a.Subject != "" && a.Project == "" && a.List == "" {
		return nil // a subject of every project: the top-level subjects list
	}
	return nil
}

// listFor finds the sequence the entry goes on, creating whatever is missing on
// the way down, and returns it with the indentation its items sit at.
func (f *File) listFor(a Addition) (*yaml.Node, int, error) {
	attribution, err := f.ensureSection("attribution", 0)
	if err != nil {
		return nil, 0, err
	}

	// Where the rule goes, remembered as a path rather than as a node. Every
	// insertion re-parses the file, so a node held across one points at a line
	// that now holds something else — which is how an edit lands in the middle
	// of an unrelated rule.
	var w where
	var fallback int
	switch {
	case a.List != "":
		// never and ambiguous are mappings of keys and paths. The old flat
		// form — never as a bare list of keys — cannot have a path appended
		// to it without deciding what the file meant, so it is refused with
		// the one-line fix rather than rewritten underneath somebody.
		got := mapValue(attribution, a.List)
		if got != nil && got.Kind != yaml.MappingNode && got.Tag != "!!null" {
			return nil, 0, fmt.Errorf(
				"%s is written as a plain list, which is the old form meaning \"keys\"; "+
					"write it as \"%s:\" with \"keys:\" under it and spoor can add to it",
				a.List, a.List)
		}
		if _, err = f.ensureSection(a.List, 2, "attribution"); err != nil {
			return nil, 0, err
		}
		w, fallback = where{list: a.List}, 4
	case a.Project != "":
		if _, err = f.ensureEntry("projects", a.Project, a.Work); err != nil {
			return nil, 0, err
		}
		w, fallback = where{project: a.Project}, 6
		if a.Subject != "" {
			if _, err = f.ensureSubject(a.Project, a.Subject); err != nil {
				return nil, 0, err
			}
			w, fallback = where{project: a.Project, subject: a.Subject}, 10
		}
	case a.Subject != "":
		if _, err = f.ensureSubject("", a.Subject); err != nil {
			return nil, 0, err
		}
		w, fallback = where{subject: a.Subject}, 6
	default:
		return nil, 0, errors.New("a rule has to go somewhere: a project, a subject, never or ambiguous")
	}

	container := f.locate(w)
	if container == nil {
		return nil, 0, errors.New("could not find where to put the rule in the config")
	}
	indent := keyIndent(container, fallback)
	if mapValue(container, a.Kind) == nil {
		if err := f.insertKey(container, a.Kind, indent); err != nil {
			return nil, 0, err
		}
		container = f.locate(w)
	}
	list := mapValue(container, a.Kind)
	if list == nil {
		return nil, 0, fmt.Errorf("could not add %q to the config", a.Kind)
	}
	return list, indent + 2, nil
}

// render is the entry as it will be written.
//
// A dated rule is a flow mapping on one line, because that is what it is: one
// rule, with a date on it. Spreading it over three lines would make the common
// undated rule and the rare dated one look like different kinds of thing.
func render(a Addition) (string, error) {
	value, err := scalar(a.Value)
	if err != nil {
		return "", err
	}
	if a.Since.IsZero() {
		return value, nil
	}
	return fmt.Sprintf("{value: %s, since: %s}", value, a.Since.Format(time.DateOnly)), nil
}

// scalar quotes a value if YAML would read it as something other than the
// string it is. Done by asking yaml.v3 rather than by a list of characters:
// the list version is the deny-list mistake this project has already made once
// in the browser source.
func scalar(v string) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	got := strings.TrimSuffix(string(out), "\n")
	if strings.Contains(got, "\n") {
		// A value YAML wants to fold over several lines. Nothing spoor writes
		// looks like that — a path, a host, an expression — and a rule that
		// arrived here is better refused than written in a shape the reader
		// will not recognise.
		return "", fmt.Errorf("%q cannot be written on one line", v)
	}
	return got, nil
}
