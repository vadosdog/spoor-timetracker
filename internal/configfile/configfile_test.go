// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package configfile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
)

// The config is somebody's file, not spoor's. These tests are about the tool
// being a guest in it: what it adds is what it said it would add, and
// everything else comes back byte for byte.

const withComments = `# The dictionary. Edited by hand; spoor adds to it.
browser:
  ignore:
    - api.example.invalid

attribution:
  # The guess stays on so a new directory still shows up as a row.
  fallback: cwd-basename

  never:
    keys:
      - www.example.invalid   # a sixth of all browsing
    paths:
      - ~/scratch

  projects:
    - name: Widgets
      work: true
      paths:
        - ~/src/widgets
      keys: [widgets.example.invalid, wid.example.invalid]
      subjects:
        - name: rewrite
          paths: ~/src/widgets/rewrite
`

func load(t *testing.T, body string) (*File, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f, path
}

// added is the lines the file gained, in order. Anything else changing is what
// these tests are looking for.
func added(t *testing.T, before, after string) []string {
	t.Helper()
	was := strings.Split(before, "\n")
	is := strings.Split(after, "\n")
	var out []string
	i := 0
	for _, line := range is {
		if i < len(was) && was[i] == line {
			i++
			continue
		}
		out = append(out, line)
	}
	if i != len(was) {
		t.Fatalf("the file lost or changed a line:\nwas:\n%s\nis:\n%s", before, after)
	}
	return out
}

// The whole promise in one test: a comment, an order and a shape somebody
// chose all survive, and the file gains exactly one line.
func TestAddingARuleChangesOneLineAndNothingElse(t *testing.T) {
	f, _ := load(t, withComments)
	if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/widgets-ui"}); err != nil {
		t.Fatal(err)
	}
	got := added(t, withComments, f.Text())
	if len(got) != 1 || strings.TrimSpace(got[0]) != "- ~/src/widgets-ui" {
		t.Errorf("the file gained %q", got)
	}
	if !strings.Contains(f.Text(), "# a sixth of all browsing") {
		t.Error("a comment on a rule was lost")
	}
	if !strings.Contains(f.Text(), "# The dictionary. Edited by hand") {
		t.Error("the comment at the top was lost")
	}
}

// Four shapes of list, because a file written by hand has all four in it, and
// a rule that lands in the wrong one is a rule that silently does nothing.
func TestEveryShapeOfListCanBeAddedTo(t *testing.T) {
	for _, c := range []struct {
		name string
		add  Addition
		want string
	}{
		{"a list over lines", Addition{Project: "Widgets", Kind: Paths, Value: "~/src/two"},
			"- ~/src/two"},
		{"a list in brackets", Addition{Project: "Widgets", Kind: Keys, Value: "w3.example.invalid"},
			"keys: [widgets.example.invalid, wid.example.invalid, w3.example.invalid]"},
		{"a single value, no list", Addition{Project: "Widgets", Subject: "rewrite", Kind: Paths, Value: "~/src/widgets/ui"},
			"- ~/src/widgets/ui"},
		{"a kind the entry does not have yet", Addition{Project: "Widgets", Kind: Titles, Value: `\bWID-\d+\b`},
			`- \bWID-\d+\b`},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := load(t, withComments)
			preview, err := f.Preview(c.add)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Add(c.add); err != nil {
				t.Fatal(err)
			}
			text := f.Text()
			if !strings.Contains(text, c.want) {
				t.Errorf("want a line %q, got:\n%s", c.want, text)
			}
			// The preview is the same code path, so it says the same thing.
			for _, line := range strings.Split(preview, "\n") {
				if !strings.Contains(text, line) {
					t.Errorf("the preview said %q and the file has no such line", line)
				}
			}
			// And what came out still parses as the config it was.
			reload(t, text)
		})
	}
}

// Growing "paths: ~/one" into a list must keep the value that was there. A
// version of this that wrote the new value over the old one would look right
// in the diff and lose a rule.
func TestGrowingASingleValueKeepsIt(t *testing.T) {
	f, _ := load(t, withComments)
	if err := f.Add(Addition{Project: "Widgets", Subject: "rewrite", Kind: Paths, Value: "~/src/widgets/ui"}); err != nil {
		t.Fatal(err)
	}
	cfg := reload(t, f.Text())
	var got []string
	for _, p := range cfg.Attribution.Projects {
		for _, s := range p.Subjects {
			if s.Name == "rewrite" {
				got = s.Paths.Values()
			}
		}
	}
	if len(got) != 2 || got[0] != "~/src/widgets/rewrite" || got[1] != "~/src/widgets/ui" {
		t.Errorf("the subject's paths are %v", got)
	}
}

// A project and a subject that are not there yet are created, because the
// alternative is "make the project, restart, then assign", which is the thing
// this stage exists to remove.
func TestAProjectAndASubjectAreCreatedOnTheSpot(t *testing.T) {
	f, _ := load(t, withComments)
	work := false
	for _, a := range []Addition{
		{Project: "Sprockets", Kind: Paths, Value: "~/src/sprockets", Work: &work},
		{Project: "Sprockets", Subject: "launch", Kind: Keys, Value: "sprockets.example.invalid"},
	} {
		if err := f.Add(a); err != nil {
			t.Fatalf("%+v: %v", a, err)
		}
	}
	cfg := reload(t, f.Text())
	var found *config.Project
	for i, p := range cfg.Attribution.Projects {
		if p.Name == "Sprockets" {
			found = &cfg.Attribution.Projects[i]
		}
	}
	if found == nil {
		t.Fatalf("the project was not created:\n%s", f.Text())
	}
	if found.Work == nil || *found.Work {
		t.Errorf("work is %v, want false", found.Work)
	}
	if len(found.Paths) != 1 || found.Paths[0].Value != "~/src/sprockets" {
		t.Errorf("paths are %v", found.Paths.Values())
	}
	if len(found.Subjects) != 1 || found.Subjects[0].Name != "launch" {
		t.Fatalf("subjects are %+v", found.Subjects)
	}
	if got := found.Subjects[0].Keys.Values(); len(got) != 1 || got[0] != "sprockets.example.invalid" {
		t.Errorf("the subject's keys are %v", got)
	}
}

// A dated rule, which is the third answer to "what is this edit": it applies
// from a day and leaves what is already counted alone.
func TestADatedRuleIsWrittenWithItsDay(t *testing.T) {
	f, _ := load(t, withComments)
	since := time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)
	if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/shared", Since: since}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Text(), "- {value: ~/src/shared, since: 2026-09-10}") {
		t.Fatalf("the dated rule is not in the file:\n%s", f.Text())
	}
	cfg := reload(t, f.Text())
	for _, p := range cfg.Attribution.Projects {
		if p.Name != "Widgets" {
			continue
		}
		for _, path := range p.Paths {
			if path.Value != "~/src/shared" {
				continue
			}
			if path.Since.IsZero() || path.Since.Format(time.DateOnly) != "2026-09-10" {
				t.Errorf("the date came back as %v", path.Since)
			}
			return
		}
	}
	t.Error("the rule did not survive a re-read")
}

// never and ambiguous are the two decisions about a trace, and they go on
// top-level lists rather than inside a project.
func TestDecisionsAboutATraceGoOnTheirOwnLists(t *testing.T) {
	f, _ := load(t, withComments)
	for _, a := range []Addition{
		{List: Never, Kind: Keys, Value: "search.example.invalid"},
		{List: Ambiguous, Kind: Keys, Value: "ops.example.invalid"},
		{List: Ambiguous, Kind: Paths, Value: "~/src/service"},
	} {
		if err := f.Add(a); err != nil {
			t.Fatalf("%+v: %v", a, err)
		}
	}
	cfg := reload(t, f.Text())
	if got := cfg.Attribution.Never.Keys.Values(); len(got) != 2 || got[1] != "search.example.invalid" {
		t.Errorf("never.keys is %v", got)
	}
	if got := cfg.Attribution.Ambiguous.Keys.Values(); len(got) != 1 || got[0] != "ops.example.invalid" {
		t.Errorf("ambiguous.keys is %v", got)
	}
	if got := cfg.Attribution.Ambiguous.Paths.Values(); len(got) != 1 {
		t.Errorf("ambiguous.paths is %v", got)
	}
}

// An empty file, and a file with no attribution section at all, are the two
// states every new user is in.
func TestARuleCanBeAddedToAFileThatHasNoDictionary(t *testing.T) {
	for _, body := range []string{"", "browser:\n  ignore:\n    - api.example.invalid\n"} {
		f, path := load(t, body)
		if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/widgets"}); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if err := f.Save(); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("%q: the written file does not parse: %v\n%s", body, err, f.Text())
		}
		if len(cfg.Attribution.Projects) != 1 {
			t.Errorf("%q: projects are %+v", body, cfg.Attribution.Projects)
		}
		if body != "" && len(cfg.Browser.Ignore) != 1 {
			t.Errorf("the browser section was lost:\n%s", f.Text())
		}
	}
}

// The write itself: atomic, a copy kept once, permissions unchanged.
func TestSavingKeepsACopyAndThePermissions(t *testing.T) {
	_, path := load(t, withComments)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/two"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Errorf("mode is %04o, want 0640", perm)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no copy was kept: %v", err)
	}
	if string(backup) != withComments {
		t.Error("the copy is not the file that was there")
	}

	// A second save in the same session does not overwrite the copy: the
	// copy is what the file looked like before spoor touched it, and one more
	// save must not turn it into what spoor already wrote.
	if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/three"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	backup, _ = os.ReadFile(path + ".bak")
	if string(backup) != withComments {
		t.Error("the second save overwrote the copy of the original")
	}
}

// An editor open in another window is the normal case. Writing over what it
// saved would lose whatever was typed there, silently.
func TestAFileThatChangedOnDiskIsNotWrittenTo(t *testing.T) {
	f, path := load(t, withComments)
	if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/two"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(withComments+"\n# somebody else was here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.Save()
	if err == nil {
		t.Fatal("the file was overwritten")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("the message does not say what happened: %v", err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "somebody else was here") {
		t.Error("the other edit was lost anyway")
	}
}

// The hash is what tells a confirmed day that the rules have moved under it.
// It has to answer for the dictionary and for nothing else, or it becomes a
// warning people learn to ignore.
func TestTheHashCoversTheDictionaryAndNothingElse(t *testing.T) {
	_, path := load(t, withComments)
	before, err := AttributionHash(path)
	if err != nil {
		t.Fatal(err)
	}

	// A comment, and a list reflowed: the same rules said differently.
	same := strings.Replace(withComments,
		"keys: [widgets.example.invalid, wid.example.invalid]",
		"keys:\n        - widgets.example.invalid\n        - wid.example.invalid", 1)
	same = strings.Replace(same, "# a sixth of all browsing", "# still a sixth", 1)
	if err := os.WriteFile(path, []byte(same), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := AttributionHash(path); got != before {
		t.Error("reflowing a list changed the hash, so every confirmed day now says the rules moved")
	}

	// A different section: nothing to do with the dictionary.
	other := strings.Replace(withComments, "api.example.invalid", "other.example.invalid", 1)
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := AttributionHash(path); got != before {
		t.Error("a change to the browser section changed the dictionary hash")
	}

	// And a real change to a rule.
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(Addition{Project: "Widgets", Kind: Paths, Value: "~/src/two"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if got, _ := AttributionHash(path); got == before {
		t.Error("adding a rule did not change the hash")
	}
}

// The old flat "never: [a, b]" cannot have a path added to it without deciding
// what the file meant. Refused with the fix, rather than rewritten underneath
// somebody.
func TestTheOldFlatNeverListIsRefusedWithAWayOut(t *testing.T) {
	f, _ := load(t, "attribution:\n  never: [www.example.invalid]\n")
	err := f.Add(Addition{List: Never, Kind: Paths, Value: "~/scratch"})
	if err == nil {
		t.Fatal("the flat form was written to")
	}
	if !strings.Contains(err.Error(), "keys:") {
		t.Errorf("the message does not say what to do: %v", err)
	}
}

func reload(t *testing.T, body string) config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("what was written does not parse: %v\n%s", err, body)
	}
	return cfg
}

// A refused addition leaves nothing behind. The edit is made in several steps
// — the section, the project entry, its list, the item — and the step that
// refuses is not the first one.
//
// The failure this guards is quiet and arrives later: the caller reports the
// error, the person tries something else, and that answer's Save commits the
// half-written entry from the refused one. An empty project entry is the rule
// that parses, looks right and never fires.
func TestARefusedAdditionChangesNothing(t *testing.T) {
	// A list in brackets across two lines. spoor will not edit one, and says
	// so — after ensuring the section and the entry above it.
	const original = `attribution:
  projects:
    - name: widget
      work: true
      paths: [
        ~/src/widget]
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	err = f.Add(Addition{Project: "widget", Kind: Paths, Value: "~/src/other"})
	if err == nil {
		t.Fatal("a bracket list across two lines was edited after all")
	}
	if f.Text() != original {
		t.Errorf("the refused addition left the file changed:\n%s\nwant:\n%s", f.Text(), original)
	}

	// And the refusal did not cost the file its next, legitimate edit: the
	// node tree has to still describe the text it was put back to.
	if err := f.Add(Addition{Project: "other", Kind: Paths, Value: "~/src/other"}); err != nil {
		t.Fatalf("the file could not be edited after a refusal: %v", err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "name: other") ||
		strings.Count(string(saved), "~/src/other") != 1 {
		t.Errorf("what was saved is not the one edit that succeeded:\n%s", saved)
	}
}

// A comment is found the same way everywhere, which means quotes are tracked.
// A "#" inside a value is part of the value.
func TestAHashInsideAValueIsNotAComment(t *testing.T) {
	const original = `attribution:
  projects:
    - name: widget
      titles: 'release # notes' # the announcement thread
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(Addition{Project: "widget", Kind: Titles, Value: "^Widget "}); err != nil {
		t.Fatal(err)
	}
	got := f.Text()
	// The value kept whole, its quoting untouched, and the comment still on
	// the key line where its author put it.
	if !strings.Contains(got, "- 'release # notes'") {
		t.Errorf("the value was cut at the hash inside it:\n%s", got)
	}
	if !strings.Contains(got, "# the announcement thread") {
		t.Errorf("the comment was lost:\n%s", got)
	}
	// And what came out is still a config file.
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("what was written no longer parses: %v", err)
	}
}

// A config linked into place from somewhere else stays a link, and the file it
// points at is the one that gains the rule.
//
// This is the ordinary arrangement for a hand-written file: it lives in a
// dotfiles repository and is linked into ~/.config. Renaming onto the link
// replaces it with a regular file — the repository copy then stops receiving
// anything, keeps whatever it last said, and nothing anywhere says the two
// have parted.
func TestASymlinkedConfigIsWrittenThrough(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "spoor.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("attribution:\n  fallback: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this filesystem has no symbolic links: %v", err)
	}

	f, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(Addition{Project: "widget", Kind: Paths, Value: "~/src/widget"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a regular file")
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "~/src/widget") {
		t.Errorf("the file behind the link did not get the rule:\n%s", body)
	}
	// And the copy kept before the first write is beside the real file, which
	// is where somebody looking for it would go.
	if _, err := os.Stat(target + ".bak"); err != nil {
		t.Errorf("no copy was kept beside the file that was written: %v", err)
	}
}

// The preview is every line the file gains, not only the item asked for.
//
// Adding the first rule of a project writes the section, the entry, its name,
// its `work:` line and the list before the item has anywhere to go. Those are
// lines somebody is about to have in their config; showing one of five is how
// a file gets edited by a tool nobody agreed to.
func TestThePreviewIsEverythingTheFileGains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("attribution:\n  fallback: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	work := true
	add := Addition{Project: "widget", Kind: Paths, Value: "~/src/widget", Work: &work}

	shown, err := f.Preview(add)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Add(add); err != nil {
		t.Fatal(err)
	}

	// What was shown is exactly what the file gained — same lines, same order.
	// Written out by hand rather than by calling gained(), which is the
	// function under test here: comparing it against itself passes for every
	// bug it could have.
	before := splitLines("attribution:\n  fallback: none\n")
	var got []string
	for _, line := range f.lines {
		if !slices.Contains(before, line) {
			got = append(got, line)
		}
	}
	if strings.Join(got, "\n") != shown {
		t.Errorf("the preview showed\n%s\n\nand the file gained\n%s",
			shown, strings.Join(got, "\n"))
	}
	for _, want := range []string{"name: widget", "work: true", "paths:", "~/src/widget"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the preview does not mention %q, which is being written:\n%s", want, shown)
		}
	}
}

// A preview of an addition into a list that has to be rewritten shows the
// rewrite, and nothing below it.
//
// A textual edit can turn `paths: ~/one` into three lines, and a naive diff
// then calls every line after that point new — so the preview offers a screen
// of text nobody is changing, which is the same failure as showing too little.
func TestThePreviewStopsAtWhatChanged(t *testing.T) {
	const original = `attribution:
  projects:
    - name: widget
      paths: ~/src/widget
      subjects:
        - name: review
          titles: ['^Merge request']
    - name: reading
      keys: ops.example.invalid
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Into the single-value list at the top, which has a whole project below it.
	shown, err := f.Preview(Addition{Project: "widget", Kind: Paths, Value: "~/src/other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, untouched := range []string{"name: review", "Merge request", "name: reading", "ops.example.invalid"} {
		if strings.Contains(shown, untouched) {
			t.Errorf("the preview claims %q is being added, and it is not:\n%s", untouched, shown)
		}
	}
	for _, want := range []string{"~/src/widget", "~/src/other"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the preview does not show %q, which is on a line that changes:\n%s", want, shown)
		}
	}
}
