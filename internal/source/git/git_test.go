// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package git

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Every fixture here is written by hand. Not one byte comes from a real
// repository: a reflog carries branch names and an author's address, and this
// repository holds neither.

const (
	shaA = "1111111111111111111111111111111111111111"
	shaB = "2222222222222222222222222222222222222222"
	shaC = "3333333333333333333333333333333333333333"
	zero = "0000000000000000000000000000000000000000"

	who = "A Developer <dev@example.invalid>"
)

// line builds one reflog line the way git writes it.
func line(old, next string, at time.Time, msg string) string {
	return fmt.Sprintf("%s %s %s %d +0000\t%s", old, next, who, at.Unix(), msg)
}

// repo writes a fake repository and returns its working tree.
func repo(t *testing.T, logs map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	for ref, lines := range logs {
		path := filepath.Join(root, ".git", "logs", filepath.FromSlash(ref))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 3, 4, hour, minute, 0, 0, time.UTC)
}

func collect(t *testing.T, root string) ([]eventLike, Report) {
	t.Helper()
	events, rep := Collect([]Repo{{ID: "fixture", Path: root}},
		at(0, 0), at(23, 59))
	out := make([]eventLike, 0, len(events))
	for _, e := range events {
		out = append(out, eventLike{e.ExternalID, e.TS, e.Subtype, e.GitBranch, e.RawText, e.Project, e.CWD})
	}
	return out, rep
}

type eventLike struct {
	id, ts, subtype, branch, raw, project, cwd string
}

// TestOneCommitIsOneEvent is the case the whole collapse rule exists for. A
// single `git commit` is written to logs/HEAD and to the branch's own log with
// the same second and the same resulting sha; two rows would be the failure
// this project cares most about, quietly more than there was.
func TestOneCommitIsOneEvent(t *testing.T) {
	root := repo(t, map[string][]string{
		"HEAD":                     {line(shaA, shaB, at(10, 0), "commit: work")},
		"refs/heads/main":          {line(shaA, shaB, at(10, 0), "commit: work")},
		"refs/remotes/origin/main": {line(shaA, shaB, at(10, 0), "update by push")},
	})

	events, rep := collect(t, root)
	if len(events) != 1 {
		t.Fatalf("want one event for one commit, got %d: %+v", len(events), events)
	}
	if rep.Collapsed != 2 {
		t.Errorf("want 2 collapsed copies, got %d", rep.Collapsed)
	}
	// And the copy that survives is the one naming a branch, because "HEAD" is
	// not a name any rule can be written against.
	if events[0].branch != "main" {
		t.Errorf("want the branch's copy to win, got branch %q", events[0].branch)
	}
	if events[0].subtype != "commit" {
		t.Errorf("want subtype %q, got %q", "commit", events[0].subtype)
	}
}

// TestAnEntryOnlyHEADSawSurvives is the other half of the same rule: a
// checkout is written to logs/HEAD alone, and collapsing must not eat it.
func TestAnEntryOnlyHEADSawSurvives(t *testing.T) {
	root := repo(t, map[string][]string{
		"HEAD": {
			line(shaA, shaB, at(10, 0), "commit: work"),
			line(shaB, shaC, at(11, 0), "checkout: moving from main to spike"),
		},
		"refs/heads/main": {line(shaA, shaB, at(10, 0), "commit: work")},
	})

	events, _ := collect(t, root)
	if len(events) != 2 {
		t.Fatalf("want two events, got %d: %+v", len(events), events)
	}
	if events[1].subtype != "checkout" || events[1].branch != "HEAD" {
		t.Errorf("want the checkout under HEAD, got %+v", events[1])
	}
}

// TestTheMessageIsNeverStored is rule 8 for this source. Everything after the
// first ": " is a commit message, which is content — and a clone's message is
// an address that can carry a credential.
func TestTheMessageIsNeverStored(t *testing.T) {
	secret := "s3cr3t-token-do-not-store"
	root := repo(t, map[string][]string{
		"HEAD": {
			line(shaA, shaB, at(9, 0), "commit: fix the thing nobody should read"),
			line(zero, shaA, at(8, 0), "clone: from https://user:"+secret+"@example.invalid/x.git"),
			line(shaB, shaC, at(12, 0), "merge feature/private-name: Fast-forward"),
		},
	})

	events, _ := collect(t, root)
	for _, e := range events {
		whole := strings.Join([]string{e.id, e.ts, e.subtype, e.branch, e.raw, e.project, e.cwd}, "|")
		for _, forbidden := range []string{secret, "nobody should read", "private-name"} {
			if strings.Contains(whole, forbidden) {
				t.Errorf("%q reached the event: %s", forbidden, whole)
			}
		}
	}
	// And the verb still arrives, because grouping by it is the whole use.
	want := map[string]bool{"clone": true, "commit": true, "merge": true}
	for _, e := range events {
		if !want[e.subtype] {
			t.Errorf("unexpected verb %q", e.subtype)
		}
		delete(want, e.subtype)
	}
	if len(want) > 0 {
		t.Errorf("verbs never arrived: %v", want)
	}
}

// TestTheQualifierSurvivesTheVerb keeps "commit (amend)" apart from "commit".
func TestTheQualifierSurvivesTheVerb(t *testing.T) {
	for _, c := range []struct {
		msg, want string
		known     bool
	}{
		{"commit: x", "commit", true},
		{"commit (amend): x", "commit (amend)", true},
		{"commit (initial): x", "commit (initial)", true},
		{"rebase (finish): returning to refs/heads/main", "rebase (finish)", true},
		{"merge some/branch: Merge made by the 'ort' strategy.", "merge", true},
		{"checkout: moving from a to b", "checkout", true},
		// No message at all: `git update-ref` writes such a line. It is
		// otherVerb rather than "" so that the column agrees with the note
		// printed about it.
		{"", otherVerb, false},
	} {
		got, known := verbOf(c.msg)
		if got != c.want || known != c.known {
			t.Errorf("verbOf(%q) = %q,%v, want %q,%v", c.msg, got, known, c.want, c.known)
		}
	}
}

// TestAnUnknownActionNeverReachesTheColumn is the boundary, not a nicety.
//
// A reflog message is not always written by git: `git update-ref -m`,
// GIT_REFLOG_ACTION and a hook can each put arbitrary text there, and
// `git stash` does it by itself. Taking the first word would put the first
// word of something a person typed into a stored column, and a commit message
// carries names and ticket numbers.
func TestAnUnknownActionNeverReachesTheColumn(t *testing.T) {
	// Nothing a person typed may come back, whether the action is recognised
	// or not. Where the verb itself is unknown the answer is "other"; where
	// only the qualifier is junk, the qualifier is dropped and the verb stays.
	for _, c := range []struct {
		msg, want string
	}{
		{"On main: ABC-123 rework the login screen", otherVerb},
		{"WIP on main: 1a2b3c4 ABC-123 rework the login screen", otherVerb},
		{"SECRET-TICKET: whatever follows", otherVerb},
		{"Переписал выгрузку по клиенту: x", otherVerb},
		{"\x1b]0;title\x07evil: x", otherVerb},
		{"commit (SECRET): the qualifier is not a free field either", "commit"},
		{"commit ABC-123: x", "commit"},
	} {
		got, _ := verbOf(c.msg)
		if got != c.want {
			t.Errorf("verbOf(%q) = %q, want %q", c.msg, got, c.want)
		}
		for _, leak := range []string{"ABC-123", "SECRET", "login", "выгрузку", "evil"} {
			if strings.Contains(got, leak) {
				t.Errorf("verbOf(%q) = %q — %q reached the column", c.msg, got, leak)
			}
		}
	}
}

// TestAnUnknownActionIsCounted — a thing silently dropped looks exactly like a
// thing that works, so the run has to say how often it happened.
func TestAnUnknownActionIsCounted(t *testing.T) {
	root := repo(t, map[string][]string{
		"refs/heads/main": {
			line(shaA, shaB, at(10, 0), "commit: fine"),
			line(shaB, shaC, at(10, 5), "On main: something somebody typed"),
		},
	})
	_, rep := collect(t, root)
	if rep.UnknownVerbs != 1 {
		t.Errorf("want 1 unknown action, got %d", rep.UnknownVerbs)
	}
	var found bool
	for _, n := range rep.Notes() {
		if strings.Contains(n, "does not know") {
			found = true
		}
	}
	if !found {
		t.Errorf("the count never reached the notes: %q", rep.Notes())
	}
}

// TestARepositoryWithNoReflogSaysSo — a bare clone keeps no reflog unless
// core.logAllRefUpdates is set, and this path was written down by hand, so
// "read it and found nothing" must not read as "read it".
func TestARepositoryWithNoReflogSaysSo(t *testing.T) {
	// A real repository directory with no logs/ under it — which is what a
	// bare clone, and anything with core.logAllRefUpdates off, looks like.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, rep := collect(t, root)
	if rep.Silent != 1 {
		t.Errorf("want 1 silent repository, got %d", rep.Silent)
	}
	var found bool
	for _, n := range rep.Notes() {
		if strings.Contains(n, "keep no reflog") {
			found = true
		}
	}
	if !found {
		t.Errorf("the count never reached the notes: %q", rep.Notes())
	}
}

// TestIdentityIsContentNotPosition is the idempotence rule. `HEAD@{0}` is
// renumbered by the next action, so a key made from a position would re-import
// the whole log under new identities the moment anybody committed.
func TestIdentityIsContentNotPosition(t *testing.T) {
	before := repo(t, map[string][]string{
		"refs/heads/main": {line(shaA, shaB, at(10, 0), "commit: one")},
	})
	first, _ := collect(t, before)

	// The same repository, one action later. The earlier entry has moved from
	// HEAD@{0} to HEAD@{1} and must keep its identity.
	after := repo(t, map[string][]string{
		"refs/heads/main": {
			line(shaA, shaB, at(10, 0), "commit: one"),
			line(shaB, shaC, at(10, 5), "commit: two"),
		},
	})
	second, _ := collect(t, after)

	if len(first) != 1 || len(second) != 2 {
		t.Fatalf("want 1 then 2 events, got %d then %d", len(first), len(second))
	}
	if first[0].id != second[0].id {
		t.Errorf("identity moved with position: %q became %q", first[0].id, second[0].id)
	}
}

// TestATruncatedReflogDoesNotChangeWhatWasRead is the other half: git prunes
// its own log, and what was imported has to keep the identity it had.
func TestATruncatedReflogDoesNotChangeWhatWasRead(t *testing.T) {
	full := repo(t, map[string][]string{
		"refs/heads/main": {
			line(shaA, shaB, at(10, 0), "commit: one"),
			line(shaB, shaC, at(10, 5), "commit: two"),
		},
	})
	whole, _ := collect(t, full)

	pruned := repo(t, map[string][]string{
		"refs/heads/main": {line(shaB, shaC, at(10, 5), "commit: two")},
	})
	rest, _ := collect(t, pruned)

	if len(rest) != 1 {
		t.Fatalf("want one surviving entry, got %d", len(rest))
	}
	if rest[0].id != whole[1].id {
		t.Errorf("truncation changed an identity: %q became %q", whole[1].id, rest[0].id)
	}
}

// TestNoEntryCarriesADuration is the promise of the stage: git adds names, not
// hours. A duration here would let the reporting stage turn a commit into an
// interval.
func TestNoEntryCarriesADuration(t *testing.T) {
	root := repo(t, map[string][]string{
		"refs/heads/main": {line(shaA, shaB, at(10, 0), "commit: one")},
	})
	events, _ := Collect([]Repo{{ID: "fixture", Path: root}}, at(0, 0), at(23, 59))
	for _, e := range events {
		if e.DurationMS != nil {
			t.Errorf("a reflog entry carried a duration of %d ms", *e.DurationMS)
		}
	}
}

// TestTheRangeIsTheInterface: a range in, events out, and nothing outside it.
func TestTheRangeIsTheInterface(t *testing.T) {
	root := repo(t, map[string][]string{
		"refs/heads/main": {
			line(shaA, shaB, at(8, 0), "commit: early"),
			line(shaB, shaC, at(12, 0), "commit: inside"),
			line(shaC, shaA, at(20, 0), "commit: late"),
		},
	})
	events, rep := Collect([]Repo{{ID: "fixture", Path: root}}, at(10, 0), at(14, 0))
	if len(events) != 1 {
		t.Fatalf("want one event in the window, got %d", len(events))
	}
	if rep.OutOfRange != 2 {
		t.Errorf("want 2 entries outside the window, got %d", rep.OutOfRange)
	}
	// Half-open, like every other range in this program: the lower bound is
	// in, the upper bound is out.
	if _, rep := Collect([]Repo{{ID: "fixture", Path: root}}, at(12, 0), at(20, 0)); rep.Events != 1 {
		t.Errorf("want the lower bound included and the upper excluded, got %d events", rep.Events)
	}
}

// TestTwoRepositoriesKeepTheirOwnIdentities. The same commit can sit in two
// checkouts — a worktree, a fork — and each is a place the person worked.
func TestTwoRepositoriesKeepTheirOwnIdentities(t *testing.T) {
	one := repo(t, map[string][]string{
		"refs/heads/main": {line(shaA, shaB, at(10, 0), "commit: one")},
	})
	two := repo(t, map[string][]string{
		"refs/heads/main": {line(shaA, shaB, at(10, 0), "commit: one")},
	})
	events, _ := Collect([]Repo{{ID: "alpha", Path: one}, {ID: "beta", Path: two}},
		at(0, 0), at(23, 59))
	if len(events) != 2 {
		t.Fatalf("want one event per repository, got %d", len(events))
	}
	if events[0].ExternalID == events[1].ExternalID {
		t.Errorf("two repositories produced one identity: %q", events[0].ExternalID)
	}
}

// TestAMisspeltPathIsReportedRatherThanSkipped. Nobody discovered this path,
// somebody wrote it down — and a typo here means the repository is never read.
func TestAMisspeltPathIsReportedRatherThanSkipped(t *testing.T) {
	_, rep := Collect([]Repo{{ID: "gone", Path: filepath.Join(t.TempDir(), "nowhere")}},
		at(0, 0), at(23, 59))
	if len(rep.Errors) != 1 {
		t.Fatalf("want one error for a path that is not a repository, got %v", rep.Errors)
	}
	if rep.ReposRead != 0 {
		t.Errorf("want nothing read, got %d", rep.ReposRead)
	}
}

// TestAWorktreeIsFollowed: a worktree and a submodule keep a `.git` file
// pointing at the real directory.
func TestAWorktreeIsFollowed(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "elsewhere")
	tree := filepath.Join(root, "tree")
	if err := os.MkdirAll(filepath.Join(actual, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actual, "logs", "HEAD"),
		[]byte(line(shaA, shaB, at(10, 0), "commit: one")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+actual+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	events, rep := Collect([]Repo{{ID: "tree", Path: tree}}, at(0, 0), at(23, 59))
	if len(rep.Errors) > 0 {
		t.Fatalf("a worktree was not followed: %v", rep.Errors)
	}
	if len(events) != 1 {
		t.Fatalf("want one event through the .git file, got %d", len(events))
	}
	// The event still belongs to the working tree, which is what a rule is
	// written about.
	if events[0].CWD != strings.TrimRight(tree, "/") {
		t.Errorf("want cwd %q, got %q", tree, events[0].CWD)
	}
}

// TestSomethingThatIsNotAReflogLineCostsThatLine. A file under logs/ is
// somebody else's, and one bad line must not cost the repository.
func TestSomethingThatIsNotAReflogLineCostsThatLine(t *testing.T) {
	root := repo(t, map[string][]string{
		"refs/heads/main": {
			"this is not a reflog line",
			line(shaA, shaB, at(10, 0), "commit: one"),
			"zzzz " + shaB + " " + who + " 1772000000 +0000\tcommit: bad sha",
		},
	})
	events, rep := collect(t, root)
	if len(events) != 1 {
		t.Fatalf("want the one good line, got %d", len(events))
	}
	if rep.Malformed != 2 {
		t.Errorf("want 2 malformed lines counted, got %d", rep.Malformed)
	}
}

// TestCollectIsDeterministic. Everything downstream — which block an event
// lands in, which project owns a second — is decided by walking this slice.
func TestCollectIsDeterministic(t *testing.T) {
	root := repo(t, map[string][]string{
		"HEAD":               {line(shaA, shaB, at(10, 0), "checkout: moving from a to b")},
		"refs/heads/main":    {line(shaA, shaB, at(9, 0), "commit: one")},
		"refs/heads/feature": {line(shaB, shaC, at(9, 0), "commit: two")},
		"refs/stash":         {line(shaC, shaA, at(11, 0), "WIP on main")},
	})
	first, _ := Collect([]Repo{{ID: "fixture", Path: root}}, at(0, 0), at(23, 59))
	for i := 0; i < 5; i++ {
		again, _ := Collect([]Repo{{ID: "fixture", Path: root}}, at(0, 0), at(23, 59))
		if len(again) != len(first) {
			t.Fatalf("run %d produced %d events, first produced %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j] != first[j] {
				t.Fatalf("run %d differs at %d:\n%+v\n%+v", i, j, again[j], first[j])
			}
		}
	}
}

// TestSupportedOnEverySystemWeBuildFor. This source knows no paths of its own,
// so there is nothing here that could be wrong elsewhere — but the declaration
// has to exist, and it has to include the system the tests are running on.
func TestSupportedEverywhereAndVerifiedOnlyWhereItHasBeenRun(t *testing.T) {
	// Supported is true everywhere on purpose: this source has no path of its
	// own, so refusing to run would mean silently not reading repositories
	// somebody wrote down by hand.
	if !Supported() {
		t.Fatalf("the git source declares itself absent on %s, where it is being tested", runtime.GOOS)
	}
	// Verified is the declaration rule 7 asks for, and it is a real list: if
	// this ever becomes "true everywhere" as well, the declaration has stopped
	// saying anything.
	if Verified() != verifiedOS[runtime.GOOS] {
		t.Errorf("Verified() disagrees with the list on %s", runtime.GOOS)
	}
	for _, os := range []string{"plan9", "js", "aix"} {
		if verifiedOS[os] {
			t.Errorf("%s is claimed as verified; nobody has run it there", os)
		}
	}
}
