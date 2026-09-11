// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package configfile

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Everything here works in lines. A YAML parse says where things are; the edit
// itself is an insertion of text at a line number, and then the file is parsed
// again. Config files are tens of lines, so re-parsing after every addition is
// free, and it means no function here has to keep line numbers valid after
// somebody else's edit — the commonest way this kind of code goes wrong.

// lastLine is the last line of the file a node covers, 1-based.
//
// Trailing comments are deliberately not included. A comment after the last
// item of a list is attached by the parser to whatever comes next, so
// inserting in front of it puts the new rule with the rules it belongs to and
// leaves the comment introducing the next thing where its author put it.
func lastLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	line := n.Line
	for _, c := range n.Content {
		if l := lastLine(c); l > line {
			line = l
		}
	}
	return line
}

// keyIndent is how far the keys of a mapping are indented, or fallback for a
// mapping that has none yet. Taken from the file rather than assumed: somebody
// who indents by four gets their file back indented by four.
func keyIndent(m *yaml.Node, fallback int) int {
	if m != nil && m.Kind == yaml.MappingNode && len(m.Content) > 0 {
		return m.Content[0].Column - 1
	}
	return fallback
}

// insert puts lines into the file after the 1-based line number given, and
// re-parses. after == 0 means the very top.
func (f *File) insert(after int, lines []string) error {
	if after > len(f.lines) {
		after = len(f.lines)
	}
	out := make([]string, 0, len(f.lines)+len(lines))
	out = append(out, f.lines[:after]...)
	out = append(out, lines...)
	out = append(out, f.lines[after:]...)
	f.lines = out
	return f.parse()
}

// replace swaps one line for several, and re-parses.
func (f *File) replace(at int, lines []string) error {
	out := make([]string, 0, len(f.lines)+len(lines))
	out = append(out, f.lines[:at-1]...)
	out = append(out, lines...)
	out = append(out, f.lines[at:]...)
	f.lines = out
	return f.parse()
}

// where is a place in the dictionary, remembered as a path rather than as a
// node. Nodes are tied to the parse they came from, so anything held across an
// insertion is stale, and a stale node points at a line that now holds
// something else.
type where struct {
	list    string // never | ambiguous
	project string
	subject string
}

func (f *File) locate(w where) *yaml.Node {
	attribution := mapValue(f.root(), "attribution")
	switch {
	case w.list != "":
		return mapValue(attribution, w.list)
	case w.project != "":
		p := named(mapValue(attribution, "projects"), w.project)
		if w.subject == "" {
			return p
		}
		return named(mapValue(p, "subjects"), w.subject)
	case w.subject != "":
		return named(mapValue(attribution, "subjects"), w.subject)
	}
	return attribution
}

// ensureSection makes sure a mapping exists at a key, and returns it.
//
// parents names the mapping it goes in — empty for the top level, or
// "attribution" for a section inside it. A new key is added at the end of the
// block it belongs to, which is where a person adding one by hand would put it.
func (f *File) ensureSection(key string, indent int, parents ...string) (*yaml.Node, error) {
	parent := f.root()
	for _, p := range parents {
		parent = mapValue(parent, p)
		if parent == nil {
			return nil, fmt.Errorf("%q is not in the config", p)
		}
	}
	if got := mapValue(parent, key); got != nil {
		if got.Kind == yaml.MappingNode || got.Tag == "!!null" {
			if got.Tag == "!!null" {
				return got, nil
			}
			return got, nil
		}
		return nil, fmt.Errorf("%q in the config is not a section", key)
	}

	at := len(f.lines)
	if parent != nil {
		at = lastLine(parent)
	}
	line := strings.Repeat(" ", indent) + key + ":"
	// A new top-level section wants a blank line in front of it, the way
	// every other section in this file has one.
	add := []string{line}
	if parent == nil && len(f.lines) > 0 && strings.TrimSpace(f.lines[len(f.lines)-1]) != "" {
		add = []string{"", line}
	}
	if err := f.insert(at, add); err != nil {
		return nil, err
	}
	parent = f.root()
	for _, p := range parents {
		parent = mapValue(parent, p)
	}
	got := mapValue(parent, key)
	if got == nil {
		return nil, fmt.Errorf("could not add %q to the config", key)
	}
	return got, nil
}

// insertKey adds "key:" with nothing under it, at the end of a mapping.
func (f *File) insertKey(parent *yaml.Node, key string, indent int) error {
	at := lastLine(parent)
	if parent.Tag == "!!null" {
		// A key with nothing under it yet: the null node sits on the key's own
		// line, so the new key goes on the line after it.
		at = parent.Line
	}
	return f.insert(at, []string{strings.Repeat(" ", indent) + key + ":"})
}

// ensureEntry finds a named entry of a top-level sequence under attribution —
// projects — and creates it if it is not there.
func (f *File) ensureEntry(section, name string, work *bool) (*yaml.Node, error) {
	attribution := mapValue(f.root(), "attribution")
	seq := mapValue(attribution, section)
	if seq == nil || seq.Tag == "!!null" {
		if seq == nil {
			if err := f.insertKey(attribution, section, 2); err != nil {
				return nil, err
			}
		}
		attribution = mapValue(f.root(), "attribution")
		seq = mapValue(attribution, section)
	}
	if got := named(seq, name); got != nil {
		return got, nil
	}

	value, err := scalar(name)
	if err != nil {
		return nil, err
	}
	indent := 2
	if seq.Kind == yaml.SequenceNode && len(seq.Content) > 0 {
		indent = seq.Content[0].Column - 3
	}
	lines := []string{strings.Repeat(" ", indent) + "- name: " + value}
	if work != nil {
		lines = append(lines, strings.Repeat(" ", indent+2)+fmt.Sprintf("work: %t", *work))
	}
	at := lastLine(seq)
	if seq.Tag == "!!null" {
		at = seq.Line
	}
	if err := f.insert(at, lines); err != nil {
		return nil, err
	}
	got := named(mapValue(mapValue(f.root(), "attribution"), section), name)
	if got == nil {
		return nil, fmt.Errorf("could not add project %q to the config", name)
	}
	return got, nil
}

// ensureSubject finds a subject inside a project — or, with no project, in the
// list that applies inside every project — and creates it if it is not there.
func (f *File) ensureSubject(project, name string) (*yaml.Node, error) {
	container := f.locate(where{project: project})
	if container == nil {
		return nil, fmt.Errorf("project %q is not in the config", project)
	}
	indent := keyIndent(container, 2)
	seq := mapValue(container, "subjects")
	if seq == nil {
		if err := f.insertKey(container, "subjects", indent); err != nil {
			return nil, err
		}
		container = f.locate(where{project: project})
		seq = mapValue(container, "subjects")
	}
	if got := named(seq, name); got != nil {
		return got, nil
	}

	value, err := scalar(name)
	if err != nil {
		return nil, err
	}
	itemIndent := indent + 2
	if seq.Kind == yaml.SequenceNode && len(seq.Content) > 0 {
		itemIndent = seq.Content[0].Column - 3
	}
	at := lastLine(seq)
	if seq.Tag == "!!null" {
		at = seq.Line
	}
	if err := f.insert(at, []string{strings.Repeat(" ", itemIndent) + "- name: " + value}); err != nil {
		return nil, err
	}
	got := named(mapValue(f.locate(where{project: project}), "subjects"), name)
	if got == nil {
		return nil, fmt.Errorf("could not add subject %q to the config", name)
	}
	return got, nil
}

// appendTo puts one entry on a list, whatever shape the list is written in,
// and returns the lines that were added.
//
// Four shapes, because a config written by hand has all four in it: a block
// list, a list on one line in brackets, a single value written without a list
// at all, and a key with nothing under it yet.
func (f *File) appendTo(list *yaml.Node, indent int, entry string) ([]string, error) {
	switch {
	case list.Tag == "!!null":
		// keys:
		//   <- here
		line := strings.Repeat(" ", indent) + "- " + entry
		return []string{line}, f.insert(list.Line, []string{line})

	case list.Kind == yaml.SequenceNode && list.Style&yaml.FlowStyle != 0:
		return f.appendToFlow(list, entry)

	case list.Kind == yaml.SequenceNode:
		at := lastLine(list)
		if len(list.Content) > 0 {
			indent = list.Content[0].Column - 3
		}
		line := strings.Repeat(" ", indent) + "- " + entry
		return []string{line}, f.insert(at, []string{line})

	case list.Kind == yaml.ScalarNode:
		// paths: ~/src/thing — one value written without a list. It becomes a
		// list of two, keeping what was there.
		return f.growScalar(list, indent, entry)

	default:
		return nil, fmt.Errorf("the list at line %d is not something a rule can be added to", list.Line)
	}
}

// appendToFlow adds to a list written on one line: [a, b].
func (f *File) appendToFlow(list *yaml.Node, entry string) ([]string, error) {
	if lastLine(list) != list.Line {
		return nil, fmt.Errorf(
			"the list starting at line %d is written in brackets across several lines; "+
				"spoor will not guess where to put a rule in it", list.Line)
	}
	line := f.lines[list.Line-1]
	// The closing bracket of the list, not the last one on the line: a
	// trailing comment may hold brackets of its own, and inserting in front of
	// one of those writes the rule into the comment.
	bracket := strings.LastIndex(uncommented(line), "]")
	if bracket < 0 {
		return nil, fmt.Errorf("the list at line %d opens a bracket it never closes", list.Line)
	}
	sep := ", "
	if len(list.Content) == 0 {
		sep = ""
	}
	updated := line[:bracket] + sep + entry + line[bracket:]
	return []string{updated}, f.replace(list.Line, []string{updated})
}

// uncommented is the line up to a trailing comment. YAML starts a comment at a
// "#" that follows a space, so that a "#" inside a value is left alone.
func uncommented(line string) string {
	quoted := byte(0)
	for i := 0; i < len(line); i++ {
		switch {
		case quoted != 0:
			if line[i] == quoted {
				quoted = 0
			}
		case line[i] == '\'' || line[i] == '"':
			quoted = line[i]
		case line[i] == '#' && i > 0 && (line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// growScalar turns "paths: ~/one" into a list holding what was there and what
// is being added. The value already in the file is copied out of the file
// rather than out of the parsed node, so that whatever quoting its author used
// survives.
func (f *File) growScalar(list *yaml.Node, indent int, entry string) ([]string, error) {
	line := f.lines[list.Line-1]
	colon := strings.Index(line, ":")
	if colon < 0 {
		return nil, fmt.Errorf("line %d is not a key and a value", list.Line)
	}
	// A comment on the value's line belongs to the key, so it stays on it —
	// and where the comment starts is decided by the same quote-tracking used
	// everywhere else in this file. A plain search for " #" cuts
	// `titles: 'release # notes'` down to `'release`, which is a value with
	// one quote in it: the file no longer parses, and the rule it was carrying
	// is gone.
	value := uncommented(line[colon+1:])
	existing := strings.TrimSpace(value)
	head := line[:colon+1]
	// uncommented cuts before the "#", so the space that made it a comment is
	// at the end of the value. Put one back: `titles:# note` is not a comment,
	// it is a key whose value starts with a hash.
	if rest := line[colon+1+len(value):]; rest != "" {
		head += " " + rest
	}
	pad := strings.Repeat(" ", indent)
	lines := []string{head, pad + "- " + existing, pad + "- " + entry}
	return lines[1:], f.replace(list.Line, lines)
}
