// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vadosdog/spoor-timetracker/internal/confirm"
	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Every key, through the key.
//
// The reason this file is written the way it is: "the key did nothing" and
// "the key did the right thing" look identical until somebody looks at the
// screen. A test calling the function under a key would pass in both cases,
// and this interface is the largest surface in the program for exactly that
// failure — one path per key, and no compiler to notice a missing wire.
//
// So nothing here calls a method on the model. Bytes go in, frames come out,
// and the config file and the database are read afterwards to see what really
// happened.

// press runs the interface over a script of keystrokes and returns everything
// it drew. The script always ends in a key that leaves, or the program would
// wait for input that is not coming.
//
// One key per argument, and one key per read: bubbletea gathers whatever
// arrives in one read into a single key message, so "2q" handed over at once
// is one keystroke called "2q" and matches nothing. That is a property of a
// scripted reader rather than of a keyboard, and getting it wrong makes every
// test here hang rather than fail — which is worth the four lines below.
func press(t *testing.T, s *confirm.Session, keys ...string) string {
	t.Helper()
	var out bytes.Buffer
	if _, _, err := RunWith(s, &script{keys: keys}, &out); err != nil {
		t.Fatalf("the interface failed: %v", err)
	}
	return out.String()
}

// script hands over one keystroke per read.
type script struct {
	keys []string
	at   int
}

func (s *script) Read(p []byte) (int, error) {
	if s.at >= len(s.keys) {
		return 0, io.EOF
	}
	// A pause between keystrokes, because these tests are about what somebody
	// sees. The renderer draws about sixty times a second and skips frames
	// that are already stale, so a whole script delivered inside one frame
	// shows only its last screen — and a test asserting on a screen nobody
	// would ever have seen is not testing the interface.
	time.Sleep(30 * time.Millisecond)
	n := copy(p, s.keys[s.at])
	s.at++
	return n, nil
}

// typed is a script that types a word one character at a time and then does
// whatever comes next.
func typed(word string, then ...string) []string {
	out := make([]string, 0, len(word)+len(then))
	for _, r := range word {
		out = append(out, string(r))
	}
	return append(out, then...)
}

// aSession builds a day with two traces nothing names, and opens it.
func aSession(t *testing.T) (*confirm.Session, string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	// The guess stays on, which is the default and the interesting case: the
	// block has a name nobody wrote, so the question is "is that name right"
	// rather than "there is nothing here at all".
	if err := os.WriteFile(cfg, []byte("attribution:\n  fallback: cwd-basename\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "spoor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	date := time.Date(2026, 9, 8, 0, 0, 0, 0, time.Local)
	var events []event.Event
	at := func(h, m int) string {
		return time.Date(2026, 9, 8, h, m, 0, 0, time.Local).UTC().Format("2006-01-02T15:04:05.000Z")
	}
	for i, clock := range [][2]int{{9, 0}, {9, 4}, {9, 8}} {
		events = append(events, event.Event{
			Source: "claude-code", ExternalID: fmt.Sprintf("p%d", i), TS: at(clock[0], clock[1]),
			Type: "user", RawText: "prompt", Project: "widget",
			CWD: "/home/u/src/widget", SessionID: "s-1", Entrypoint: "cli",
		})
	}
	for i, clock := range [][2]int{{11, 0}, {11, 3}} {
		events = append(events, event.Event{
			Source: "browser", ExternalID: fmt.Sprintf("v%d", i), TS: at(clock[0], clock[1]),
			Type: "visit", Host: "ops.example.invalid", PathHead: fmt.Sprintf("s%d", i),
			Title: "Widget board", Entrypoint: "chrome",
		})
	}
	if _, err := st.InsertEvents(events); err != nil {
		t.Fatal(err)
	}

	session, err := confirm.Open(st, cfg, "test", date, report.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return session, cfg, st
}

// reopen is a second session on the same day, for a test that presses keys,
// looks at what was written, and then presses more.
func reopen(t *testing.T, st *store.Store, cfg string) *confirm.Session {
	t.Helper()
	s, err := confirm.Open(st, cfg, "test", time.Date(2026, 9, 8, 0, 0, 0, 0, time.Local), report.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func configBody(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// The question screen. What it shows is a proposal with the evidence under it,
// and the four things the roadmap says have to be there.
func TestTheQuestionScreenShowsWhatTheAnswerRestsOn(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, "q")

	for _, want := range []string{
		"under question", // the budget in the header
		"counts for",     // what the trace leaks into now
		"WHERE",          // the places in the day
		"PROJECT",        // the two slots
		"SUBJECT",
		"n names nothing", // the trash answer, one key
		"? more",          // everything the four answers are not
	} {
		if !strings.Contains(frames, want) {
			t.Errorf("the question screen never showed %q:\n%s", want, frames)
		}
	}
}

// enter takes the answer under the cursor, and then the scope question — which
// is asked every time, because what an edit is cannot be worked out from it.
func TestEnterAsksWhatTheEditIsAndThenWritesTheRule(t *testing.T) {
	s, cfg, _ := aSession(t)
	// Down to a candidate, enter, then "2": a rule.
	frames := press(t, s, "\r", "2", "q")

	if !strings.Contains(frames, "what is this edit?") {
		t.Fatalf("enter did not ask what the edit is:\n%s", frames)
	}
	body := configBody(t, cfg)
	if !strings.Contains(body, "/home/u/src/widget") {
		t.Errorf("no rule was written:\n%s", body)
	}
}

// The same keystroke, answered "3": a rule that starts today.
func TestTheThirdOutcomeWritesADatedRule(t *testing.T) {
	s, cfg, _ := aSession(t)
	press(t, s, "\r", "3", "q")
	body := configBody(t, cfg)
	if !strings.Contains(body, "since: 2026-09-08") {
		t.Errorf("the dated rule was not written:\n%s", body)
	}
}

// And "1": this block only. Nothing goes into the config, and something goes
// into the database.
func TestTheFirstOutcomeTouchesNoRule(t *testing.T) {
	s, cfg, st := aSession(t)
	before := configBody(t, cfg)
	press(t, s, "\r", "1", "q")

	if got := configBody(t, cfg); got != before {
		t.Errorf("a one-off wrote to the config:\n%s", got)
	}
	saved, err := st.Assignments("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || !saved[0].OneOff {
		t.Errorf("the answer was not stored as a one-off: %+v", saved)
	}
}

// The trash answer costs one key, and it is the same key wherever it is
// pressed. Anything dearer and marking a day stops being worth doing.
func TestOneKeyRefusesATrace(t *testing.T) {
	s, cfg, _ := aSession(t)
	press(t, s, "x", "2", "q")
	body := configBody(t, cfg)
	if !strings.Contains(body, "never") {
		t.Errorf("[x] wrote no never entry:\n%s", body)
	}
}

// And one key says the trace names a subject and never the same one twice.
func TestOneKeySaysNoRuleCanNameIt(t *testing.T) {
	s, cfg, _ := aSession(t)
	press(t, s, "a", "2", "q")
	if body := configBody(t, cfg); !strings.Contains(body, "ambiguous") {
		t.Errorf("[a] wrote no ambiguous entry:\n%s", body)
	}
}

// [n] creates a project without leaving the interface. Not "make it, restart,
// assign" — here, while looking at the block.
func TestANewProjectIsCreatedWithoutLeaving(t *testing.T) {
	s, cfg, _ := aSession(t)
	press(t, s, typed("NWidgets", "\r", "2", "q")...)
	body := configBody(t, cfg)
	if !strings.Contains(body, "name: Widgets") {
		t.Errorf("[n] did not create the project:\n%s", body)
	}
	if !strings.Contains(body, "/home/u/src/widget") {
		t.Errorf("the new project got no rule:\n%s", body)
	}
}

// And a subject, in the same way and in the same place.
func TestANewSubjectIsCreatedWithoutLeaving(t *testing.T) {
	s, cfg, st := aSession(t)
	press(t, s, typed("NWidgets", "\r", "2", "q")...)
	press(t, reopen(t, st, cfg), typed("\tNrewrite", "\r", "2", "q")...)
	body := configBody(t, cfg)
	if !strings.Contains(body, "subjects") || !strings.Contains(body, "name: rewrite") {
		t.Errorf("[tab] then [n] did not create a subject:\n%s", body)
	}
}

// [e] edits the line before it is written. What lands in the file is what was
// on the screen.
func TestTheLineCanBeEditedBeforeItIsWritten(t *testing.T) {
	s, cfg, _ := aSession(t)
	// The rule as offered is the directory; cut it back to its parent.
	press(t, s, "e", "\x7f", "\x7f", "\x7f", "\x7f", "\x7f", "\x7f", "\x7f", "\r", "2", "q")
	body := configBody(t, cfg)
	// Seven characters off "/home/u/src/widget" is "/home/u/src", and that is
	// what has to be in the file. The earlier form of this test asked for a
	// string and its own prefix at once, which is a condition no file can
	// meet, so it asserted nothing at all: [e] could have been dropped
	// entirely and it would still have passed.
	if !strings.Contains(body, "- /home/u/src\n") {
		t.Errorf("[e] did not shorten the rule; the file has:\n%s", body)
	}
	if strings.Contains(body, "/home/u/src/widget") {
		t.Errorf("[e] wrote the rule as it was offered:\n%s", body)
	}
}

// [t] offers a title that was really seen, escaped. No expression is ever
// invented: one nobody wrote is one nobody can check afterwards.
func TestTheTitleAnswerWritesATitleRuleAndNoKeyRule(t *testing.T) {
	s, cfg, _ := aSession(t)
	// Past the directory question to the one about the host, where this
	// answer is the one that matters: the repository name is in the title and
	// in no column spoor stores.
	press(t, s, append([]string{"s", "t"}, typed("Widget board", "\r", "2", "q")...)...)

	body := configBody(t, cfg)
	if !strings.Contains(body, "titles") || !strings.Contains(body, "Widget board") {
		t.Fatalf("[t] wrote no title rule:\n%s", body)
	}
	// And no key rule beside it: this file cannot say "this host and this
	// title", so two rules would be two things that can disagree.
	if strings.Contains(body, "keys") {
		t.Errorf("a key rule was written as well:\n%s", body)
	}
}

// [s] skips: the question comes back tomorrow. That is the whole difference
// between the third answer and the fourth.
func TestSkipLeavesTheQuestionAlone(t *testing.T) {
	s, cfg, _ := aSession(t)
	before := configBody(t, cfg)
	frames := press(t, s, "s", "s", "q")
	if got := configBody(t, cfg); got != before {
		t.Error("[s] wrote something")
	}
	// Past the questions, onto the pauses. Asserting on the date instead says
	// nothing about where the walk stopped: it is in every header, so the
	// first frame satisfies it before a key is pressed.
	if !strings.Contains(frames, "another pause") {
		t.Errorf("skipping past the last question went nowhere:\n%s", frames)
	}
}

// [b] is the way to a block that spoor named with complete confidence. If the
// interface can only answer questions, the stage is not done.
func TestAnyBlockCanBeOpenedAndChanged(t *testing.T) {
	s, cfg, st := aSession(t)
	// Name the directory first, so the block is one spoor is sure about.
	press(t, s, typed("NWidgets", "\r", "2", "q")...)
	frames := press(t, reopen(t, st, cfg), "b", "q")
	if !strings.Contains(frames, "EVERY BLOCK OF THE DAY") {
		t.Fatalf("[b] did not open the blocks:\n%s", frames)
	}
	if !strings.Contains(frames, "Widgets") {
		t.Errorf("a block spoor named is not in the list:\n%s", frames)
	}

	// Open it and change its project, on a block that already has a rule.
	press(t, reopen(t, st, cfg), typed("b\rNSprockets", "\r", "1", "q")...)
	saved, err := st.Assignments("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) == 0 {
		t.Fatal("changing a named block recorded nothing")
	}
	if saved[0].Project != "Sprockets" {
		t.Errorf("the block was changed to %q, want Sprockets", saved[0].Project)
	}
}

// The background question: whether measured background counts, never whether
// it was work. The concept forbids the second by name.
func TestTheBackgroundQuestionAsksWhetherToCountIt(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, "g", "q")
	if !strings.Contains(frames, "does not guess") {
		t.Errorf("the background screen implies spoor knows:\n%s", frames)
	}
	if !strings.Contains(frames, "y count it") {
		t.Errorf("the background question has no answer keys:\n%s", frames)
	}
}

// And [y] and [n] on that screen change the number, which is the whole of what
// the question is for. Drawing the two keys is not the same as them working:
// this is the pair hard limit 6 puts opposite `confirm --count-background`, so
// both sides have to be exercised or the guarantee is a guess.
func TestTheBackgroundAnswerChangesWhatIsCounted(t *testing.T) {
	setting := func(answer string) bool {
		s, _, st := aSession(t)
		press(t, s, "g", answer, "c", "c")
		day, ok, err := st.Confirmed("2026-09-08")
		if err != nil || !ok {
			t.Fatalf("the day was not frozen (%v)", err)
		}
		return day.CountBackground
	}
	if !setting("y") {
		t.Error("[y] did not turn counting on")
	}
	if setting("n") {
		t.Error("[n] did not turn counting off")
	}
}

// [c] then [c] confirms the day, and the day is frozen in the database.
func TestConfirmingFreezesTheDay(t *testing.T) {
	s, _, st := aSession(t)
	press(t, s, "c", "c")

	got, ok, err := st.Confirmed("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("[c] did not confirm the day")
	}
	if len(got.Stretches) == 0 {
		t.Error("the frozen day holds no partition, so its timeline cannot be read back")
	}
	if got.QuestionsTotal == 0 {
		t.Error("the frozen day does not say how much was asked")
	}
}

// [q] leaves without confirming, and nothing is frozen. Leaving has to be
// free, or somebody in a hurry will confirm a day they have not read.
func TestQuittingConfirmsNothing(t *testing.T) {
	s, _, st := aSession(t)
	press(t, s, "q")
	if _, ok, _ := st.Confirmed("2026-09-08"); ok {
		t.Error("[q] confirmed the day")
	}
}

// esc backs out of the scope question, and the answer is dropped rather than
// half-applied.
func TestEscapeFromTheScopeQuestionWritesNothing(t *testing.T) {
	s, cfg, _ := aSession(t)
	before := configBody(t, cfg)
	press(t, s, "\r", "\x1b", "q")
	if got := configBody(t, cfg); got != before {
		t.Errorf("escaping the scope question still wrote:\n%s", got)
	}
}

// The delta after a write, including the one that says nothing moved — which
// is a rule that is either shadowed or written where nobody touches.
func TestTheDeltaIsShownAfterAWrite(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, typed("NWidgets", "\r", "2", "q")...)
	if !strings.Contains(frames, "Widgets +") && !strings.Contains(frames, "no time moved") {
		t.Errorf("nothing said what the rule did:\n%s", frames)
	}
}

// An encounter: one visit to a trace already settled as one no rule can name.
//
// It has its own screen and its own key, and it is counted in the header
// budget — a question in the count that no key could ever answer would make
// "9 of 11 answered" mean nothing.
func TestAnEncounterCanBeAnswered(t *testing.T) {
	s, cfg, st := aSession(t)
	// Decide about the trace once: no rule can name the subject on this host.
	press(t, s, "s", "a", "2", "q")
	if body := configBody(t, cfg); !strings.Contains(body, "ambiguous") {
		t.Fatalf("the trace was not decided about:\n%s", body)
	}

	// The rest of the dictionary, written by hand: a project to be in, a
	// subject to choose from, and min_split at zero — which the threshold in the config says has to
	// mean zero rather than "the default", so that "ask me about every one of
	// them" is expressible. The visits in this fixture are three minutes
	// apart, so without it there is nothing to answer about.
	if err := os.WriteFile(cfg, []byte(
		"attribution:\n  fallback: cwd-basename\n"+
			"  ambiguous:\n    min_split: 0s\n    keys: [ops.example.invalid]\n"+
			"  projects:\n    - name: Widgets\n      paths: /home/u/src/widget\n"+
			"      keys: [ops.example.invalid]\n"+
			"      subjects:\n        - name: routing\n          paths: /home/u/src/widget\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	again := reopen(t, st, cfg)
	if len(again.Document().Encounters) == 0 {
		t.Fatal("min_split: 0s left no encounter to answer, so zero did not mean zero")
	}
	frames := press(t, again, "\r", "q")
	if !strings.Contains(frames, "no rule can name the subject here") {
		t.Fatalf("the encounter screen never appeared:\n%s", frames)
	}
	saved, err := st.Assignments("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) == 0 {
		t.Fatal("answering an encounter recorded nothing")
	}
	// It is not hand marking of the counted kind: the decision that no rule
	// could name this was itself a rule, written once.
	if saved[0].OneOff {
		t.Error("an encounter was counted as an answer that could not become a rule")
	}
}

// The header counts what can be answered. A question in the total that no key
// reaches would make the line the confirmed day carries a lie.
func TestEveryQuestionInTheBudgetHasAScreen(t *testing.T) {
	s, _, _ := aSession(t)
	doc := s.Document()
	// Not "the budget equals the sum it is defined as" — that is the
	// definition restated, and it holds however wrong both sides are. What has
	// to hold is that the number is a real count of real questions, each of
	// which a person can actually reach.
	if doc.Budget.Questions == 0 {
		t.Fatal("this day raises no questions at all; the test proves nothing")
	}
	// Walking off the end of the questions reaches the encounters and then the
	// day, rather than skipping anything countable.
	frames := press(t, s, "s", "s", "s", "s", "s", "s", "s", "s", "q")
	if !strings.Contains(frames, "another pause") {
		t.Errorf("skipping past everything never left the questions:\n%s", frames)
	}
	// Every countable question was on a screen on the way: the trace of each
	// one appears in the frames, which is what "reachable by a key" means.
	for _, q := range doc.Questions {
		if !strings.Contains(frames, q.Trace.Value) {
			t.Errorf("question %q was counted and never shown:\n%s", q.Trace.Value, frames)
		}
	}
	for _, e := range doc.Encounters {
		if !strings.Contains(frames, e.Trace.Value) {
			t.Errorf("encounter %q was counted and never shown:\n%s", e.Trace.Value, frames)
		}
	}
}

// A shortcut is a place on the keyboard, not a letter.
//
// Half of this person's day is typed in Cyrillic, and switching layouts to
// press "t" — and switching back — is a tax on the one loop that has to fit in
// two minutes. So the Cyrillic letter printed on the same physical key does
// the same thing, and the test presses the real bytes a terminal would send.
func TestTheKeysWorkOnACyrillicLayout(t *testing.T) {
	// "ч" is the key "x" is printed on: the answer that refuses a trace.
	s, cfg, _ := aSession(t)
	press(t, s, "ч", "2", "й") // x, scope "a rule", q
	if body := configBody(t, cfg); !strings.Contains(body, "never") {
		t.Errorf("Cyrillic \"ч\" did not do what \"x\" does:\n%s", body)
	}

	// And a screen reached by another letter, so that this is a rule rather
	// than one mapping that happens to work.
	s2, _, _ := aSession(t)
	frames := press(t, s2, "и", "й") // b, q
	if !strings.Contains(frames, "EVERY BLOCK OF THE DAY") {
		t.Errorf("Cyrillic \"и\" did not open the blocks:\n%s", frames)
	}
}

// Typed text is not translated. A project called "модель ЗП" has to be
// typeable, and it would not be if the line editor read letters as shortcuts.
func TestTypedTextKeepsItsAlphabet(t *testing.T) {
	s, cfg, _ := aSession(t)
	press(t, s, typed("Nмодель ЗП", "\r", "2", "q")...)
	body := configBody(t, cfg)
	// Exactly, and with its one space. A name that arrives with a letter
	// translated or a space doubled is a different project from the one that
	// was typed: it is a separate key in the config, in every assignment and
	// in every confirmed row, and nothing afterwards can tell the two apart.
	if !strings.Contains(body, "name: модель ЗП\n") {
		t.Errorf("the name did not survive being typed, character for character:\n%s", body)
	}
}

// The pauses screen. It is a measuring instrument: it asks the one thing no
// trace can answer, records it, and moves no measured number. Both halves are checked
// here, because a question that quietly changed hours would be worse than no
// question at all.
func TestAPauseIsAnsweredAndNothingMoves(t *testing.T) {
	s, _, st := aSession(t)
	before := s.Document().Day.Attention

	frames := press(t, s, "w", "\r", "q")
	if !strings.Contains(frames, "no rule can be written on a pause") {
		t.Fatalf("[w] did not open the pauses:\n%s", frames)
	}
	if !strings.Contains(frames, "not work") {
		t.Errorf("the screen does not offer the four answers:\n%s", frames)
	}

	answers, err := st.WindowAnswers("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || !answers[0].Worked {
		t.Fatalf("the answer was not recorded: %+v", answers)
	}
	// Attached to something, not just called work. An hour attached to nothing
	// answers no question anybody asks.
	if answers[0].Project == "" {
		t.Error("the pause was called work and attached to nothing")
	}
	// It kept what was on either side, because the signal being tested is
	// whether those two agreeing predicted the answer at the time.
	if answers[0].Left == "" && answers[0].Right == "" {
		t.Error("the answer records nothing about what was around it")
	}

	// Attention is what it was: a pause is not active time and does not become
	// any. What the answer buys is a line of its own.
	again := reopen(t, st, cfgOf(t, s))
	if got := again.Document().Day.Attention; got != before {
		t.Errorf("answering a pause moved attention from %s to %s", before, got)
	}
	if again.Document().Day.Claimed == 0 {
		t.Error("the answer bought no line of its own")
	}
}

func cfgOf(t *testing.T, s *confirm.Session) string {
	t.Helper()
	return s.ConfigPath()
}

// The header has to move while somebody works, or the interface reads as
// hung. An answered question leaves the queue, so an index into it stands
// still while the list gets shorter — which is exactly what "the counter does
// not move" looked like.
func TestTheHeaderMovesAsQuestionsAreAnswered(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, "\r", "2", "q")
	if !strings.Contains(frames, "0 answered") {
		t.Errorf("the header never showed the starting count:\n%s", frames)
	}
	if !strings.Contains(frames, "1 answered") {
		t.Errorf("the header did not move after an answer:\n%s", frames)
	}
}

// A key question on a repository host is unanswerable without the titles: the
// repository is a path segment spoor does not store, and the name is in the
// title and nowhere else.
func TestTheQuestionShowsWhatThePagesWereCalled(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, "s", "q") // past the directory, to the host
	if !strings.Contains(frames, "SEEN AS") {
		t.Fatalf("the titles are not on the screen:\n%s", frames)
	}
	if !strings.Contains(frames, "Widget board") {
		t.Errorf("the titles are empty, so the question cannot be answered:\n%s", frames)
	}
}

// The pauses are part of closing a day, not an errand beside it. Walking off
// the end of the questions reaches them, and only then the day — a question
// you can only get to by knowing a key is a question most days never get
// asked.
func TestTheWalkReachesThePausesBeforeTheDay(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, "s", "s", "q")
	if !strings.Contains(frames, "no rule can be written on a pause") {
		t.Fatalf("skipping the questions went past the pauses:\n%s", frames)
	}
	// And through them to the day.
	s2, _, _ := aSession(t)
	frames = press(t, s2, "s", "s", "s", "s", "s", "s", "q")
	if !strings.Contains(frames, "September 2026") {
		t.Errorf("skipping everything did not reach the day:\n%s", frames)
	}
}

// A day whose pauses have all been answered does not stop on them again.
func TestAnsweredPausesAreNotAskedAgain(t *testing.T) {
	s, cfg, st := aSession(t)
	// Answer every pause, then walk the day again.
	doc := s.Document()
	if len(doc.Windows) == 0 {
		t.Fatal("no pauses in this fixture")
	}
	for _, w := range doc.Windows {
		if err := s.AnswerWindow(w, false, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	frames := press(t, reopen(t, st, cfg), "s", "s", "q")
	if strings.Contains(frames, "no rule can be written on a pause") {
		t.Errorf("a day with every pause answered stopped on them again:\n%s", frames)
	}
}

// The four things a pause can be, and nothing else. In particular there is no
// "what kind of edit is this": a pause has no trace, so no rule could be
// written on it, and offering the choice would offer an answer that cannot be
// carried out.
func TestAPauseIsNeverAskedToBecomeARule(t *testing.T) {
	s, cfg, st := aSession(t)
	before := configBody(t, cfg)

	frames := press(t, s, "w", "\r", "q")
	if strings.Contains(frames, "what is this edit?") {
		t.Error("a pause was asked to become a rule")
	}
	if got := configBody(t, cfg); got != before {
		t.Errorf("answering a pause wrote to the config:\n%s", got)
	}
	if answers, _ := st.WindowAnswers("2026-09-08"); len(answers) == 0 {
		t.Error("the answer went nowhere")
	}
}

// Not work is one key, and it attaches to nothing.
func TestAPauseCanBeNotWork(t *testing.T) {
	s, _, st := aSession(t)
	press(t, s, "w", "n", "q")
	answers, err := st.WindowAnswers("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Worked {
		t.Fatalf("the answer was not recorded as not work: %+v", answers)
	}
	if answers[0].Project != "" {
		t.Errorf("a pause that was not work was attached to %q", answers[0].Project)
	}
}

// A project can be created from the pauses screen, without leaving it.
func TestANewProjectCanBeMadeFromAPause(t *testing.T) {
	s, cfg, st := aSession(t)
	before := configBody(t, cfg)
	press(t, s, append([]string{"w", "N"}, typed("Errands", "\r", "q")...)...)

	answers, err := st.WindowAnswers("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) == 0 || answers[0].Project != "Errands" {
		t.Fatalf("the new name did not reach the pause: %+v", answers)
	}
	// And it is still not a rule.
	if got := configBody(t, cfg); got != before {
		t.Errorf("naming a pause wrote a rule:\n%s", got)
	}
}

// Answering the last pause ends the walk. Walking by index sat on the last one
// and re-answered it on every keystroke, which from the outside is a program
// going round in a circle — and was.
func TestAnsweringEveryPauseEndsTheWalk(t *testing.T) {
	s, _, st := aSession(t)
	n := len(s.Document().Windows)
	if n == 0 {
		t.Fatal("no pauses in this fixture")
	}

	// One "enter" per pause, and one more than there are: the extra keystroke
	// is the one that used to answer the last pause a second time.
	script := []string{"w"}
	for i := 0; i <= n; i++ {
		script = append(script, "\r")
	}
	script = append(script, "q")
	press(t, s, script...)

	answers, err := st.WindowAnswers("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != n {
		t.Errorf("%d pauses answered, want %d — the walk did not visit each one once",
			len(answers), n)
	}
}

// A day whose pauses are all answered does not open the screen at all. Opening
// on a question already answered, and leaving the moment it is answered again,
// is what "it exits after the first enter" looked like.
func TestAWalkWithNothingToAskDoesNotOpen(t *testing.T) {
	s, cfg, st := aSession(t)
	for _, w := range s.Document().Windows {
		if err := s.AnswerWindow(w, false, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	// No keys at all: if it opens, this hangs.
	answered, err := RunPauses(reopen(t, st, cfg), &script{}, &out)
	if !AllAnswered(err) {
		t.Fatalf("want the all-answered reason, got %v", err)
	}
	if answered != 0 || out.Len() != 0 {
		t.Errorf("something was drawn or answered anyway: %d, %q", answered, out.String())
	}
}

// Backwards, for changing an answer given too fast.
func TestAPauseCanBeLookedAtAgain(t *testing.T) {
	s, _, st := aSession(t)
	if len(s.Document().Windows) < 2 {
		t.Skip("this fixture has one pause")
	}
	// Answer the first, move on, then step back and answer it differently.
	press(t, s, "w", "\r", "left", "n", "q")
	answers, err := st.WindowAnswers("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 {
		t.Fatalf("%d answers, want 1: stepping back made a second one", len(answers))
	}
	if answers[0].Worked {
		t.Error("the second answer did not replace the first")
	}
}

// Opened for its pauses alone, it leaves when there is nothing left to ask
// rather than sitting on the last question for ever.
func TestThePausesOnlyWalkFinishes(t *testing.T) {
	s, _, st := aSession(t)
	n := len(s.Document().Windows)
	var out bytes.Buffer
	script := &script{}
	for i := 0; i < n; i++ {
		script.keys = append(script.keys, "\r")
	}
	// No quit key at all: if the walk does not end by itself, this hangs and
	// the test times out, which is the failure being pinned.
	answered, err := RunPauses(s, script, &out)
	if err != nil {
		t.Fatal(err)
	}
	if answered != n {
		t.Errorf("%d answered, want %d", answered, n)
	}
	if got, _ := st.WindowAnswers("2026-09-08"); len(got) != n {
		t.Errorf("%d stored, want %d", len(got), n)
	}
}

// Every handler hands back the same kind of model. bubbletea keeps whatever it
// is given, so one handler returning a pointer makes the final model a
// different type from all the others — the program keeps running and
// everything read off it afterwards is silently empty, which is how the last
// one was found.
func TestEveryHandlerReturnsTheSameKindOfModel(t *testing.T) {
	s, _, _ := aSession(t)
	m := New(s)
	for _, k := range []string{"\r", "2", "tab", "n", "x", "a", "t", "e", "s", "b",
		"g", "w", "c", "y", "esc", "up", "down", "1", "3"} {
		got, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if _, ok := got.(Model); !ok {
			t.Fatalf("%q handed back %T, not a Model", k, got)
		}
		m, _ = got.(Model)
	}
}

// One meaning per letter, on every screen. A capital is "new" and a lowercase
// letter is "nothing"; the same letter meaning "new project" here and "not
// work" one screen along is how somebody presses the wrong one at speed.
func TestALetterMeansTheSameThingOnEveryScreen(t *testing.T) {
	// N makes something, on the question screen and on the pauses screen.
	s, cfg, _ := aSession(t)
	press(t, s, typed("NWidgets", "\r", "2", "q")...)
	if !strings.Contains(configBody(t, cfg), "name: Widgets") {
		t.Error("N did not make a project on the question screen")
	}

	s2, _, st2 := aSession(t)
	press(t, s2, append([]string{"w", "N"}, typed("Errands", "\r", "q")...)...)
	if a, _ := st2.WindowAnswers("2026-09-08"); len(a) == 0 || a[0].Project != "Errands" {
		t.Error("N did not make a project on the pauses screen")
	}

	// n says "nothing", on both.
	s3, cfg3, _ := aSession(t)
	press(t, s3, "n", "2", "q")
	if !strings.Contains(configBody(t, cfg3), "never") {
		t.Error("n did not refuse the trace on the question screen")
	}

	s4, _, st4 := aSession(t)
	press(t, s4, "w", "n", "q")
	a, _ := st4.WindowAnswers("2026-09-08")
	if len(a) == 0 || a[0].Worked {
		t.Error("n did not mark the pause as not work")
	}
}

// And the same on a Cyrillic layout, where case has to survive the mapping:
// "Т" is the key "N" is printed on, not the key "n" is.
func TestCaseSurvivesTheLayoutMapping(t *testing.T) {
	s, cfg, _ := aSession(t)
	press(t, s, append([]string{"Т"}, typed("Widgets", "\r", "2", "q")...)...)
	if !strings.Contains(configBody(t, cfg), "name: Widgets") {
		t.Errorf("Cyrillic \"Т\" did not do what \"N\" does:\n%s", configBody(t, cfg))
	}

	s2, cfg2, _ := aSession(t)
	press(t, s2, "т", "2", "q")
	if !strings.Contains(configBody(t, cfg2), "never") {
		t.Errorf("Cyrillic \"т\" did not do what \"n\" does:\n%s", configBody(t, cfg2))
	}
}

// Everything the four answers are not is behind one key, and it says what each
// of the others does.
func TestTheRestOfTheKeysAreExplainedBehindOneKey(t *testing.T) {
	s, _, _ := aSession(t)
	frames := press(t, s, "?", "q")
	if !strings.Contains(frames, "the rest of it") {
		t.Fatalf("[?] opened nothing:\n%s", frames)
	}
	for _, want := range []string{
		"no rule can name the subject here",  // a
		"name it by the page title",          // t
		"edit the rule before it is written", // e
		"every block of the day",             // b
		"the agent's own time",               // g
		"the pauses between blocks",          // w
	} {
		if !strings.Contains(frames, want) {
			t.Errorf("the help does not explain %q:\n%s", want, frames)
		}
	}
}

// A project name is a string, so a typo makes a second project silently, and
// the two look like two different things for ever. The guard warns and stays
// put: a second enter is a decision rather than a keystroke nobody noticed.
func TestATypoInAProjectNameIsQueriedOnce(t *testing.T) {
	s, cfg, _ := aSession(t)
	// Make one, then try to make the same name with different case.
	press(t, s, typed("NWidgets", "\r", "2", "q")...)
	if !strings.Contains(configBody(t, cfg), "name: Widgets") {
		t.Fatal("the first project was not created")
	}

	s2, cfg2, st := aSession(t)
	press(t, s2, typed("NWidgets", "\r", "2", "q")...)
	// One enter: warned, and nothing written.
	// esc leaves the line editor; "q" inside it would be typed rather than
	// pressed, which is the point of the editor not translating anything.
	frames := press(t, reopen(t, st, cfg2), append([]string{"N"}, typed("widgets", "\r", "\x1b", "q")...)...)
	if !strings.Contains(frames, "already a project called") {
		t.Fatalf("no warning about the near-identical name:\n%s", frames)
	}
	if strings.Contains(configBody(t, cfg2), "name: widgets") {
		t.Error("the near-identical name was written on the first enter")
	}

	// Two enters: the person meant it.
	press(t, reopen(t, st, cfg2), append([]string{"N"}, typed("widgets", "\r", "\r", "2", "q")...)...)
	if !strings.Contains(configBody(t, cfg2), "name: widgets") {
		t.Errorf("a second enter did not create it:\n%s", configBody(t, cfg2))
	}
}

// A block takes the same four answers as a question and a pause. It used to
// take only a typed name, so attaching a block to a project you already had
// meant spelling it out again — and the documentation said otherwise.
func TestABlockTakesTheSameFourAnswers(t *testing.T) {
	s, cfg, st := aSession(t)
	// Give the day a project to attach to.
	press(t, s, typed("NWidgets", "\r", "2", "q")...)

	frames := press(t, reopen(t, st, cfg), "b", "\r", "q")
	for _, want := range []string{"PROJECT", "SUBJECT", "attach it to this", "N new one", "n names nothing"} {
		if !strings.Contains(frames, want) {
			t.Errorf("the block screen never showed %q:\n%s", want, frames)
		}
	}

	// And enter attaches, rather than opening a name editor.
	press(t, reopen(t, st, cfg), "b", "\r", "\r", "1", "q")
	saved, err := st.WindowAnswers("2026-09-08")
	_ = saved
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := st.Assignments("2026-09-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) == 0 {
		t.Fatal("enter on a block attached nothing")
	}
	if assigned[0].Project == "" {
		t.Errorf("the block was attached to nothing: %+v", assigned[0])
	}
}

// The scope screen shows the exact text before it is written.
//
// This is the one moment a person agrees to an edit of a file they wrote by
// hand. The promise is in the configfile package's opening comment and it was
// kept only on the command line: the interface that actually writes the config
// showed three words and no text at all.
func TestTheScopeScreenShowsWhatWillBeWritten(t *testing.T) {
	s, cfg, _ := aSession(t)
	frames := press(t, s, typed("NWidgets", "\r", "2", "q")...)
	at := strings.Index(frames, "what is this edit?")
	if at < 0 {
		t.Fatalf("the scope question was never asked:\n%s", frames)
	}
	// From the scope screen onwards. Nothing after it prints a config entry,
	// so a match here is the preview and not a later screen.
	shown := frames[at:]
	for _, want := range []string{"name: Widgets", "paths:", "/home/u/src/widget"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the screen does not show %q, which it is about to write:\n%s", want, shown)
		}
	}

	// And what it showed is what the file got.
	body := configBody(t, cfg)
	for _, want := range []string{"name: Widgets", "/home/u/src/widget"} {
		if !strings.Contains(body, want) {
			t.Errorf("the config does not contain %q, which was shown:\n%s", want, body)
		}
	}
}
