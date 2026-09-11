// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/config"
	"github.com/vadosdog/spoor-timetracker/internal/configfile"
	"github.com/vadosdog/spoor-timetracker/internal/confirm"
	"github.com/vadosdog/spoor-timetracker/internal/export"
	"github.com/vadosdog/spoor-timetracker/internal/paths"
	"github.com/vadosdog/spoor-timetracker/internal/report"
	"github.com/vadosdog/spoor-timetracker/internal/store"
	"github.com/vadosdog/spoor-timetracker/internal/tui"
)

// Everything the terminal does is here as a flag, and not by being written
// twice: both go through one session in internal/confirm. What is here is the
// parsing of what somebody typed and the printing of what came back.

func runConfirm(args []string, stdout, stderr io.Writer, tty bool) error {
	fs := flag.NewFlagSet("confirm", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dbPath := fs.String("db", "", "database file (default: XDG data dir)")
	cfgPath := fs.String("config", "", "config file (default: XDG config dir)")
	var day dateFlag
	fs.Var(&day, "day", "the day to confirm: --day for today, --day=YYYY-MM-DD for another")
	list := fs.Bool("list", false, "list the confirmed days, and which of them the rules have moved under")
	questions := fs.Bool("questions", false, "print the queue instead of opening the terminal")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	yes := fs.Bool("yes", false, "confirm the day as it stands, without asking anything")
	reconfirm := fs.Bool("reconfirm", false, "reopen a day that was already confirmed")
	countBackground := fs.Bool("count-background", false, "count the agent's own time in the totals")
	windows := fs.Bool("windows", false, "list the pauses between blocks, and what has been said about them")
	window := fs.String("window", "", "one pause, as `HH:MM-HH:MM`, to answer with --worked")
	worked := fs.String("worked", "", "for --window: was that pause work? true or false")
	windowProject := fs.String("window-project", "", "for --window --worked=true: what it was")
	windowSubject := fs.String("window-subject", "", "for --window --worked=true: the subject inside it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q; a date goes with an equals sign, as in --day=%s",
			fs.Arg(0), time.Now().Format(time.DateOnly))
	}
	// A flag that does nothing looks exactly like a flag that is broken, and
	// this one prints a document: somebody asking for JSON and getting a
	// sentence has to be told why rather than left to wonder.
	if *asJSON && !*questions && !*windows {
		return errors.New("--json prints a document, so it goes with --questions or --windows")
	}
	if (*window != "") != (*worked != "") {
		return errors.New("--window says which pause and --worked says what it was; both or neither")
	}
	// Accepted and dropped is how a person comes to believe they said
	// something they did not. Every other impossible combination in these
	// commands is refused with a sentence; so is this one.
	if *worked == "false" && (*windowProject != "" || *windowSubject != "") {
		return errors.New("--window-project and --window-subject say what a pause was; " +
			"they go with --worked=true")
	}
	if (*windowProject != "" || *windowSubject != "") && *window == "" {
		return errors.New("--window-project and --window-subject describe one pause; " +
			"they go with --window")
	}

	st, closeDB, err := openStoreForReading(*dbPath)
	if err != nil {
		return err
	}
	defer closeDB()

	if *list {
		return listConfirmed(st, *cfgPath, stdout)
	}

	path, err := configPath(*cfgPath)
	if err != nil {
		return err
	}
	date := midnight(day.dateOr(time.Now()))
	opts, err := reportOptions(path)
	if err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "count-background" {
			opts.CountBackground = *countBackground
		}
	})

	name := date.Format(time.DateOnly)
	// Reopening is for the flags that can write a new snapshot, and for no
	// others. Printing the queue, listing the pauses and answering one all
	// read a confirmed day perfectly well, and opening those as if the day
	// were unconfirmed would recompute a day somebody has already filed. The
	// snapshot itself is not touched here at all — see confirm.Reopen: it is
	// replaced by the confirmation that succeeds it, and not a moment sooner.
	reopening := *reconfirm && !*questions && !*windows && *window == ""
	if reopening {
		fmt.Fprintf(stderr, "%s is open again; confirming it will write a new snapshot\n", name)
	} else if *reconfirm {
		fmt.Fprintf(stderr,
			"%s stays confirmed: --reconfirm reopens a day for answering, and this "+
				"only reads it\n", name)
	}

	start := confirm.Open
	if reopening {
		start = confirm.Reopen
	}
	session, err := start(st, path, Version, date, opts)
	if err != nil {
		return err
	}
	doc := session.Document()
	// A confirmed day is closed to everything that would move it — but the
	// pauses are not one of those things, so the same command still opens, on
	// the one screen that is still answerable. Making somebody know a flag to
	// get there is how a question stops being asked.
	pausesOnly := false
	if doc.Confirmed && !*reconfirm && !*windows && *window == "" && !*questions && !*yes {
		if !tty {
			return fmt.Errorf("%s is confirmed already — `spoor confirm --day=%s --reconfirm` "+
				"reopens it; --windows lists the pauses, which are still answerable", name, name)
		}
		fmt.Fprintf(stderr,
			"%s is confirmed: its numbers are frozen and only its pauses can still be "+
				"answered. `--reconfirm` reopens the whole day.\n", name)
		pausesOnly = true
	}

	switch {
	case pausesOnly:
		// Only ever reached on a terminal: the branch that sets it has already
		// returned otherwise.
		answered, err := tui.RunPauses(session, nil, stdout)
		switch {
		case tui.NoPauses(err):
			fmt.Fprintf(stdout, "%s has no pauses between blocks\n", name)
			return nil
		case tui.AllAnswered(err):
			fmt.Fprintf(stdout,
				"every pause on %s has been answered — `--windows` lists them, "+
					"and `--window=HH:MM-HH:MM --worked=...` changes one\n", name)
			return nil
		case err != nil:
			return err
		}
		// Said out loud, because the interface leaves the moment the last
		// pause is answered and a screen that vanishes with no word reads as a
		// crash.
		fmt.Fprintf(stdout, "%d of %d pauses answered; no measured number changed\n",
			answered, len(session.Document().Windows))
		return nil
	case *windows:
		return writeWindows(stdout, session, *asJSON)
	case *window != "":
		return answerWindow(stdout, session, *window, *worked, *windowProject, *windowSubject)
	case *questions:
		return writeQuestions(stdout, session, *asJSON)
	case *yes:
		if doc.Confirmed && !*reconfirm {
			return fmt.Errorf("%s is confirmed already — `spoor confirm --day=%s --reconfirm` reopens it",
				name, name)
		}
		confirmed, err := session.Confirm()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, confirm.Summary(confirmed))
		return nil
	}

	if !tty {
		// A terminal interface with nothing to draw on. Refused rather than
		// half-run: the alternative is a program that looks as if it hung.
		return errors.New("confirm opens a terminal interface and this is not a terminal; " +
			"--questions prints the queue, --yes confirms the day as it stands")
	}
	confirmed, done, err := tui.Run(session, stdout)
	if err != nil {
		return err
	}
	if done {
		fmt.Fprintln(stdout, confirm.Summary(confirmed))
	}
	return nil
}

// listConfirmed prints the days that are frozen, and says which of them the
// dictionary has moved under since. A day that has quietly disagreed with the
// rules for a year, with nowhere to find that out, is the failure this line
// exists to prevent.
func listConfirmed(st *store.Store, cfgPath string, stdout io.Writer) error {
	path, err := configPath(cfgPath)
	if err != nil {
		return err
	}
	hash, err := configfile.AttributionHash(path)
	if err != nil {
		return err
	}
	days, err := st.ConfirmedDays()
	if err != nil {
		return err
	}
	if len(days) == 0 {
		fmt.Fprintln(stdout, "no confirmed days yet")
		return nil
	}
	for _, d := range days {
		line := fmt.Sprintf("%s  confirmed %s  %d/%d answered",
			d.Day, d.ConfirmedAt.Local().Format("2006-01-02 15:04"),
			d.QuestionsAnswered, d.QuestionsTotal)
		if d.OneOffCount > 0 {
			line += fmt.Sprintf("  %d not a rule", d.OneOffCount)
		}
		if d.ConfigHash != hash {
			line += "  — the rules have changed since"
		}
		fmt.Fprintln(stdout, line)
	}
	return nil
}

// writeQuestions prints the queue as a document. It is the whole of what the
// terminal shows, in a form that can be read by a program or by somebody who
// thinks the terminal is wrong.
func writeQuestions(w io.Writer, s *confirm.Session, asJSON bool) error {
	doc := s.Document()
	if asJSON {
		return writeJSONDocument(w, doc)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %d questions, %s under question, %s in the day\n",
		doc.Date.Format(time.DateOnly), doc.Budget.Questions,
		hm(doc.Budget.UnderQuestion), hm(doc.Budget.InTheDay))
	for i, q := range doc.Questions {
		fmt.Fprintf(&b, "\n%d. %s\n", i+1, q.Trace)
		fmt.Fprintf(&b, "   %d events, %d of them you, %s, called %q now\n",
			q.Events, q.Touches, hm(q.Time), calledNow(q.CalledNow))
		if len(q.Folded) > 0 {
			fmt.Fprintf(&b, "   stands for %d keys of this host\n", len(q.Folded))
		}
		for _, p := range q.Places {
			fmt.Fprintf(&b, "   %s–%s  %s\n",
				p.From.Format("15:04"), p.To.Format("15:04"), placeOf(p))
		}
		for _, c := range q.Project {
			mark := " "
			if c.Default {
				mark = ">"
			}
			fmt.Fprintf(&b, "   %s %s (%s)\n", mark, c.Name, c.Why)
		}
	}
	for _, e := range doc.Encounters {
		fmt.Fprintf(&b, "\n%s · %s–%s · in %s\n", e.Trace,
			e.From.Format("15:04"), e.To.Format("15:04"), e.Project)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func calledNow(name string) string {
	if name == "" {
		return "(no project)"
	}
	return name
}

func placeOf(p confirm.Place) string {
	switch {
	case p.Project != "":
		return "in " + p.Project
	case p.Left != "" && p.Right != "":
		return fmt.Sprintf("no name; %s to the left, %s to the right", p.Left, p.Right)
	case p.Left != "":
		return fmt.Sprintf("no name; %s to the left, nothing to the right", p.Left)
	case p.Right != "":
		return fmt.Sprintf("no name; nothing to the left, %s to the right", p.Right)
	}
	return "no name, and nothing named anywhere near it"
}

// The JSON document. Byte for byte deterministic, like every other --json in
// the program: it is what somebody diffs when the terminal looks wrong.
type jsonDocument struct {
	Day           string          `json:"day"`
	Questions     int             `json:"questions"`
	UnderQuestion int64           `json:"under_question_ms"`
	InTheDay      int64           `json:"in_the_day_ms"`
	Confirmed     bool            `json:"confirmed"`
	RulesMoved    bool            `json:"rules_moved"`
	Queue         []jsonQuestion  `json:"queue"`
	Encounters    []jsonEncounter `json:"encounters"`
	Blocks        []jsonBlock     `json:"blocks"`
	Background    jsonBackground  `json:"background"`
}

type jsonQuestion struct {
	Trace      string          `json:"trace"`
	Folded     []string        `json:"folded"`
	Events     int             `json:"events"`
	Touches    int             `json:"touches"`
	TimeMS     int64           `json:"time_ms"`
	FirstToday bool            `json:"first_today"`
	CalledNow  string          `json:"called_now"`
	Places     []jsonPlace     `json:"places"`
	Project    []jsonCandidate `json:"project"`
	Subject    []jsonCandidate `json:"subject"`
}

type jsonPlace struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Project string `json:"project"`
	Origin  string `json:"project_from"`
	Left    string `json:"left"`
	Right   string `json:"right"`
}

type jsonCandidate struct {
	Name    string `json:"name"`
	Why     string `json:"why"`
	Default bool   `json:"default"`
}

type jsonEncounter struct {
	Trace   string          `json:"trace"`
	From    string          `json:"from"`
	To      string          `json:"to"`
	Project string          `json:"project"`
	Choices []jsonCandidate `json:"choices"`
}

type jsonBlock struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	Project      string   `json:"project"`
	Subject      string   `json:"subject"`
	Ground       string   `json:"ground"`
	Events       int      `json:"events"`
	Traces       []string `json:"traces"`
	AttentionMS  int64    `json:"attention_ms"`
	BackgroundMS int64    `json:"background_ms"`
}

type jsonBackground struct {
	TotalMS int64       `json:"total_ms"`
	Counted bool        `json:"counted"`
	Long    []jsonBlock `json:"long"`
}

func writeJSONDocument(w io.Writer, doc confirm.Document) error {
	out := jsonDocument{
		Day:           doc.Date.Format(time.DateOnly),
		Questions:     doc.Budget.Questions,
		UnderQuestion: doc.Budget.UnderQuestion.Milliseconds(),
		InTheDay:      doc.Budget.InTheDay.Milliseconds(),
		Confirmed:     doc.Confirmed,
		RulesMoved:    doc.RulesMoved,
		Queue:         []jsonQuestion{},
		Encounters:    []jsonEncounter{},
		Blocks:        []jsonBlock{},
	}
	for _, q := range doc.Questions {
		item := jsonQuestion{
			Trace: q.Trace.String(), Folded: []string{}, Events: q.Events,
			Touches: q.Touches, TimeMS: q.Time.Milliseconds(),
			FirstToday: q.FirstToday, CalledNow: q.CalledNow,
			Places: []jsonPlace{}, Project: []jsonCandidate{}, Subject: []jsonCandidate{},
		}
		for _, f := range q.Folded {
			item.Folded = append(item.Folded, f.String())
		}
		for _, p := range q.Places {
			item.Places = append(item.Places, jsonPlace{
				From: p.From.Format(time.RFC3339), To: p.To.Format(time.RFC3339),
				Project: p.Project, Origin: string(p.Origin), Left: p.Left, Right: p.Right,
			})
		}
		item.Project = candidates(q.Project)
		item.Subject = candidates(q.Subject)
		out.Queue = append(out.Queue, item)
	}
	for _, e := range doc.Encounters {
		out.Encounters = append(out.Encounters, jsonEncounter{
			Trace: e.Trace.String(), From: e.From.Format(time.RFC3339),
			To: e.To.Format(time.RFC3339), Project: e.Project,
			Choices: candidates(e.Choices),
		})
	}
	for _, b := range doc.Blocks {
		item := jsonBlock{
			From: b.From.Format(time.RFC3339), To: b.To.Format(time.RFC3339),
			Project: b.Project, Subject: b.Subject, Ground: string(b.Ground),
			Events: b.Events, Traces: []string{},
			AttentionMS: b.Attention.Milliseconds(), BackgroundMS: b.Background.Milliseconds(),
		}
		for _, t := range b.Traces {
			item.Traces = append(item.Traces, t.String())
		}
		out.Blocks = append(out.Blocks, item)
	}
	out.Background = jsonBackground{
		TotalMS: doc.Background.Total.Milliseconds(),
		Counted: doc.Background.Count,
		Long:    []jsonBlock{},
	}
	for _, s := range doc.Background.Long {
		out.Background.Long = append(out.Background.Long, jsonBlock{
			From: s.From.Format(time.RFC3339), To: s.To.Format(time.RFC3339),
			Project: s.Project, Subject: s.Subject, Ground: string(s.Ground),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func candidates(list []confirm.Candidate) []jsonCandidate {
	out := []jsonCandidate{}
	for _, c := range list {
		out = append(out, jsonCandidate{Name: c.Name, Why: c.Why, Default: c.Default})
	}
	return out
}

// writeWindows prints the pauses between blocks and what has been said about
// each. It is the flag half of the screen: whether a pause was work is a
// question nothing on this machine can answer, and the answers change no
// number — they are collected to settle whether a pause deserves a record.
func writeWindows(w io.Writer, s *confirm.Session, asJSON bool) error {
	list := s.Document().Windows
	if asJSON {
		type item struct {
			From     string `json:"from"`
			To       string `json:"to"`
			MS       int64  `json:"length_ms"`
			Left     string `json:"left"`
			Right    string `json:"right"`
			Answered bool   `json:"answered"`
			Worked   bool   `json:"worked"`
			Project  string `json:"project"`
			Subject  string `json:"subject"`
		}
		out := []item{}
		for _, x := range list {
			out = append(out, item{
				From: x.From.Format(time.RFC3339), To: x.To.Format(time.RFC3339),
				MS: x.Duration().Milliseconds(), Left: x.Left, Right: x.Right,
				Answered: x.Answered, Worked: x.Worked,
				Project: x.Project, Subject: x.Subject,
			})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s between blocks. Answers change no measured number.\n",
		count(len(list), "pause", "pauses"))
	for _, x := range list {
		said := "—"
		if x.Answered {
			said = workedWord(x.Worked, x.Project, x.Subject)
		}
		fmt.Fprintf(&b, "  %s-%s  %6s  %-30s %s\n",
			x.From.Format("15:04"), x.To.Format("15:04"), hm(x.Duration()),
			x.Left+" → "+x.Right, said)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// answerWindow records one, named by the clock the list prints.
func answerWindow(w io.Writer, s *confirm.Session, spec, worked, project, subject string) error {
	from, to, ok := strings.Cut(spec, "-")
	if !ok {
		return fmt.Errorf("--window: want HH:MM-HH:MM, got %q", spec)
	}
	was, err := parseBool(worked)
	if err != nil {
		return fmt.Errorf("--worked: %w", err)
	}
	for _, x := range s.Document().Windows {
		if x.From.Format("15:04") != strings.TrimSpace(from) ||
			x.To.Format("15:04") != strings.TrimSpace(to) {
			continue
		}
		if err := s.AnswerWindow(x, was, project, subject); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s-%s recorded as %s; no measured number changed\n",
			x.From.Format("15:04"), x.To.Format("15:04"), workedWord(was, project, subject))
		return nil
	}
	// Named rather than invented: a pause spoor does not know about is time
	// nothing recorded, and typing intervals out of nothing is the marking as
	// you go this project refuses.
	return fmt.Errorf("no pause of this day runs from %s to %s; "+
		"`confirm --day=%s --windows` lists them",
		strings.TrimSpace(from), strings.TrimSpace(to), s.Date().Format(time.DateOnly))
}

// count is a number with its noun. "1 pauses" is the sort of thing that makes
// a reader wonder what else was not looked at.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func workedWord(worked bool, project, subject string) string {
	if !worked {
		return "not work"
	}
	switch {
	case project != "" && subject != "":
		return project + " / " + subject
	case project != "":
		return project
	}
	return "work, attached to nothing"
}

// runAssign is one answer, given on the command line instead of in the
// terminal. It goes through the same session, so a rule written here is the
// same rule, written the same way, with the same checks in front of it.
func runAssign(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("assign", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dbPath := fs.String("db", "", "database file (default: XDG data dir)")
	cfgPath := fs.String("config", "", "config file (default: XDG config dir)")
	var day dateFlag
	fs.Var(&day, "day", "the day this is about: --day for today, --day=YYYY-MM-DD for another")
	trace := fs.String("trace", "", "the trace to write a rule on, as `kind:value` — path:~/src/thing or key:example.com/issues")
	from := fs.String("from", "", "start of the stretch this is about, HH:MM (with --to)")
	to := fs.String("to", "", "end of the stretch this is about, HH:MM (with --from)")
	project := fs.String("project", "", "what to call it")
	subject := fs.String("subject", "", "the accumulating thing inside it")
	title := fs.String("title", "", "write the rule on the page title instead of on the trace, as this expression")
	never := fs.Bool("never", false, "this trace names no project; the block around it decides")
	ambiguous := fs.Bool("ambiguous", false, "this trace names a subject and never the same one twice")
	oneOff := fs.Bool("one-off", false, "this stretch only — no rule, and no other day moves")
	since := fs.Bool("since", false, "write the rule as starting on the day given by --day, leaving earlier days as they are")
	work := fs.String("work", "", "for a project being created: true, false, or left out for \"not said\"")
	dryRun := fs.Bool("dry-run", false, "print what would be written and write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *oneOff && *since {
		return errors.New("--one-off and --since ask for different things: one changes this day, the other writes a rule")
	}

	st, closeDB, err := openStoreForReading(*dbPath)
	if err != nil {
		return err
	}
	defer closeDB()

	path, err := configPath(*cfgPath)
	if err != nil {
		return err
	}
	date := midnight(day.dateOr(time.Now()))
	opts, err := reportOptions(path)
	if err != nil {
		return err
	}
	session, err := confirm.Open(st, path, Version, date, opts)
	if err != nil {
		return err
	}
	if session.Document().Confirmed {
		return fmt.Errorf("%s is confirmed; `spoor confirm --day=%s --reconfirm` reopens it",
			date.Format(time.DateOnly), date.Format(time.DateOnly))
	}

	answer := confirm.Answer{
		Project: *project, Subject: *subject, Title: *title,
		Never: *never, Ambiguous: *ambiguous,
	}
	if *work != "" {
		w, err := parseBool(*work)
		if err != nil {
			return fmt.Errorf("--work: %w", err)
		}
		answer.Work = &w
	}
	switch {
	case *from != "" || *to != "":
		if *trace != "" {
			return errors.New("--trace writes a rule and --from/--to change one stretch of one day; pick one")
		}
		if answer.From, err = clockOn(date, *from); err != nil {
			return fmt.Errorf("--from: %w", err)
		}
		if answer.To, err = clockOn(date, *to); err != nil {
			return fmt.Errorf("--to: %w", err)
		}
		answer.Scope = confirm.ScopeOnce
		answer.Reason = "given on the command line for this stretch only"
		if !*oneOff {
			return errors.New("an answer about a stretch of one day cannot become a rule; " +
				"add --one-off to say so, or use --trace to write a rule")
		}
		// These two are decisions about a *trace*, and a stretch of a day is
		// not one. Accepted and ignored, they would read as "I said this host
		// names nothing and spoor agreed".
		if *never || *ambiguous {
			return errors.New("--never and --ambiguous are decisions about a trace; " +
				"they go with --trace, not with --from and --to")
		}
		if *project == "" && *subject == "" {
			return errors.New("an answer about a stretch has to say what it is: " +
				"--project, or --subject, or both")
		}
	case *trace != "":
		answer.Trace = parseTraceFlag(*trace)
		if answer.Trace.Value == "" {
			return fmt.Errorf("--trace: want kind:value, as in path:~/src/thing or key:example.com/issues; got %q", *trace)
		}
		answer.Scope = confirm.ScopeRule
		if *since {
			answer.Scope = confirm.ScopeSince
		}
	default:
		return errors.New("--trace writes a rule; --from and --to change one stretch of one day")
	}

	effect, err := session.Plan(answer)
	if err != nil {
		return err
	}
	for _, line := range effect.Preview {
		fmt.Fprintf(stdout, "%s\n", line)
	}
	for _, w := range effect.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	if *dryRun {
		return nil
	}
	res, err := session.Apply(answer)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, res.Delta.String())
	return nil
}

func parseTraceFlag(s string) confirm.Trace {
	i := strings.Index(s, ":")
	if i < 0 {
		return confirm.Trace{}
	}
	kind := confirm.TraceKind(s[:i])
	switch kind {
	case confirm.KindPath, confirm.KindKey, confirm.KindTitle, confirm.KindBranch:
	default:
		return confirm.Trace{}
	}
	return confirm.Trace{Kind: kind, Value: s[i+1:]}
}

func parseBool(s string) (bool, error) {
	switch s {
	case "true", "yes", "1":
		return true, nil
	case "false", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("want true or false, got %q", s)
}

// clockOn turns HH:MM into an instant on the day being confirmed. Only times
// that are already in the day can be named: intervals typed out of nothing are
// the marking-as-you-go this project refuses, and this is where somebody would
// start doing it.
func clockOn(date time.Time, hhmm string) (time.Time, error) {
	if hhmm == "" {
		return time.Time{}, errors.New("want HH:MM")
	}
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, fmt.Errorf("want HH:MM, got %q", hhmm)
	}
	return time.Date(date.Year(), date.Month(), date.Day(),
		t.Hour(), t.Minute(), 0, 0, date.Location()), nil
}

// runExport writes a confirmed day out.
func runExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stdout)
	dbPath := fs.String("db", "", "database file (default: XDG data dir)")
	cfgPath := fs.String("config", "", "config file (default: XDG config dir)")
	var day dateFlag
	fs.Var(&day, "day", "the day to export: --day for today, --day=YYYY-MM-DD for another")
	format := fs.String("format", "markdown", "what to write; there is markdown")
	out := fs.String("out", "", "write to this file instead of standard output")
	timeline := fs.Bool("timeline", false, "add the day as a schedule under the tables")
	unconfirmed := fs.Bool("unconfirmed", false, "export a day that has not been confirmed, marked as such")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	st, closeDB, err := openStoreForReading(*dbPath)
	if err != nil {
		return err
	}
	defer closeDB()

	date := midnight(day.dateOr(time.Now()))
	name := date.Format(time.DateOnly)
	snapshot, ok, err := st.Confirmed(name)
	if err != nil {
		return err
	}
	if !ok {
		if !*unconfirmed {
			return fmt.Errorf("%s has not been confirmed — `spoor confirm --day=%s` does that, "+
				"or --unconfirmed exports it as it stands", name, name)
		}
		if snapshot, err = provisional(st, *cfgPath, date); err != nil {
			return err
		}
	}

	exporter, err := export.For(*format, export.Options{Timeline: *timeline, Unconfirmed: !ok})
	if err != nil {
		return err
	}
	if *out == "" {
		return exporter.Export(snapshot, stdout)
	}
	// 0o600 on creation, and on a file that was already there: OpenFile's mode
	// applies only to a file it creates, so an export written twice into a
	// world-readable file left by an editor would stay world-readable. What is
	// in it is project and subject names — the dictionary — which is the same
	// reason the database is kept at 0600.
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("set permissions on %s: %w", *out, err)
	}
	if err := exporter.Export(snapshot, f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "written to %s\n", *out)
	return nil
}

// provisional is a day that has not been confirmed, shaped like one that has.
// It exists so that --unconfirmed is the same document with a warning on it
// rather than a second exporter that will drift.
func provisional(st *store.Store, cfgPath string, date time.Time) (store.ConfirmedDay, error) {
	path, err := configPath(cfgPath)
	if err != nil {
		return store.ConfirmedDay{}, err
	}
	opts, err := reportOptions(path)
	if err != nil {
		return store.ConfirmedDay{}, err
	}
	session, err := confirm.Open(st, path, Version, date, opts)
	if err != nil {
		return store.ConfirmedDay{}, err
	}
	// The error is returned rather than swallowed: snapshot() fails on an
	// unreadable stored time, and a day exported as empty because of one is a
	// day that reads as a quiet Sunday.
	return session.Snapshot()
}

// withGaps puts the pauses between blocks back between the runs, marking the
// ones somebody said were work.
func withGaps(runs []report.Run, pauses []store.WindowAnswer, loc *time.Location) []report.Run {
	claimed := map[string]store.WindowAnswer{}
	for _, p := range pauses {
		if p.Worked {
			claimed[p.From.In(loc).Format(time.RFC3339)] = p
		}
	}
	out := make([]report.Run, 0, len(runs)*2)
	for i, r := range runs {
		if i > 0 && r.From.After(out[len(out)-1].To) {
			gap := report.Run{From: out[len(out)-1].To, To: r.From, Gap: true}
			if p, ok := claimed[gap.From.Format(time.RFC3339)]; ok && p.To.In(loc).Equal(gap.To) {
				gap.Claimed, gap.Project = true, p.Project
			}
			out = append(out, gap)
		}
		out = append(out, r)
	}
	return out
}

func configPath(given string) (string, error) {
	if given != "" {
		return filepath.Clean(given), nil
	}
	return paths.ConfigPath()
}

// hm is the report's formatter, not a copy of it: a length printed here and
// the same length printed by `report` have to be the same string.
func hm(d time.Duration) string { return report.HM(d) }

// reportOptions is how a day is computed, from the config alone. The flags
// that change it belong to `report`; `confirm` takes the settings as they are
// so that the day it freezes is the day the report shows.
func reportOptions(configPath string) (report.Options, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return report.Options{}, err
	}
	// Normalised here, where all three commands go through: a confirmed day
	// stores these, and a stored zero is not a record of anything. For the two
	// thresholds a zero means "use the default", so a row saying
	// cluster_gap_ms = 0 reads as a threshold of nothing — which would mean
	// every event was a block of its own. Head and tail already resolve above,
	// and two of four settings recording what was in force while the other two
	// record its absence is worse than either.
	return report.Normalise(report.Options{
		ClusterGap:      time.Duration(cfg.Report.ClusterGap),
		AttentionWindow: time.Duration(cfg.Report.AttentionWindow),
		Head:            cfg.Report.Head.OrDefault(report.DefaultHead),
		Tail:            cfg.Report.Tail.OrDefault(report.DefaultTail),
		CountBackground: cfg.Report.CountBackground,
	}), nil
}

// isTerminal says whether there is a terminal to draw on. A terminal interface
// started without one is refused rather than half-run: the alternative looks
// exactly like a program that has hung.
func isTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// sayIfRulesMoved prints the one line a frozen day cannot work out for itself.
//
// A snapshot does not notice the dictionary changing under it, so without this
// a day can disagree with the rules for a year and there is nowhere to find
// that out. --recompute is what shows the difference; this is what says there
// is one.
func sayIfRulesMoved(stderr io.Writer, cfgPath string, d store.ConfirmedDay) error {
	path, err := configPath(cfgPath)
	if err != nil {
		return err
	}
	hash, err := configfile.AttributionHash(path)
	if err != nil {
		return err
	}
	if hash != d.ConfigHash {
		fmt.Fprintf(stderr,
			"the rules have changed since %s was confirmed — --recompute shows what they "+
				"would say now, `confirm --day=%s --reconfirm` freezes it again\n", d.Day, d.Day)
	}
	return nil
}

// fromSnapshot reads a confirmed day back into the shape the renderers take.
//
// One set of renderers rather than two: a table that only a confirmed day goes
// through would be a second place where a number is formatted, and the two
// would stop agreeing without anybody noticing.
func fromSnapshot(d store.ConfirmedDay, from, to time.Time, opts report.Options,
	pauses []store.WindowAnswer) report.Report {
	opts.CountBackground = d.CountBackground
	opts.ClusterGap = d.ClusterGap
	opts.AttentionWindow = d.AttentionWindow
	opts.Head, opts.Tail = d.Head, d.Tail

	day := report.Day{Date: from}
	subjects := map[string][]report.Subject{}
	for _, r := range d.Rows {
		if r.Subject != "" {
			subjects[r.Project] = append(subjects[r.Project], report.Subject{
				Name: r.Subject, Attention: r.Attention, Background: r.Background,
				Claimed: r.Claimed, Events: r.Events, Days: 1,
			})
			continue
		}
		day.Projects = append(day.Projects, report.Project{
			Name: r.Project, Work: r.Work, Events: r.Events,
			Attention: r.Attention, Background: r.Background, Claimed: r.Claimed,
			Agent: r.Agent, Wall: r.Wall,
		})
		day.Attention += r.Attention
		day.Background += r.Background
		day.Padding += r.Padding
		// Summed here rather than left at zero: the line "agent worked while
		// you were elsewhere" is gated on it, so a day would lose a whole line
		// of its report the moment it was confirmed.
		day.Agent += r.Agent
	}
	for i := range day.Projects {
		day.Projects[i].Subjects = subjects[day.Projects[i].Name]
		report.SortSubjects(day.Projects[i].Subjects)
	}
	// The snapshot is stored by name so that two exports of one day are the
	// same bytes. A report is read busiest-first with the unnamed row last,
	// and one set of renderers exists precisely so that the frozen day and the
	// live one cannot disagree — including about the order of their rows.
	report.SortProjects(day.Projects)
	day.Active = day.Attention + day.Background

	// The schedule and the span come out of the partition, which is the whole
	// reason a confirmed day is stored as one.
	for _, s := range d.Stretches {
		local := report.Run{
			From: s.From.In(from.Location()), To: s.To.In(from.Location()),
			Project: s.Project,
		}
		if s.Kind == string(report.KindBackground) {
			local.Background = s.To.Sub(s.From)
		} else {
			local.Attention = s.To.Sub(s.From)
		}
		if n := len(day.Runs); n > 0 && day.Runs[n-1].Project == local.Project &&
			!day.Runs[n-1].To.Before(local.From) {
			day.Runs[n-1].To = local.To
			day.Runs[n-1].Attention += local.Attention
			day.Runs[n-1].Background += local.Background
			continue
		}
		day.Runs = append(day.Runs, local)
	}
	if len(d.Stretches) > 0 {
		// Span runs block to block, padding included, because that is what the
		// coverage figure divides by. First and Last are labelled "traces" and
		// must not: the head and the tail are time the tool invented, and a
		// line that says "traces 08:58 to 11:42" where the live day says 09:00
		// to 11:40 moves a measurement by freezing it.
		first := d.Stretches[0].From.In(from.Location())
		last := d.Stretches[len(d.Stretches)-1].To.In(from.Location())
		day.Span = last.Sub(first)
		day.First, day.Last = first, last
		for _, s := range d.Stretches {
			if s.Kind != string(report.KindPadding) {
				day.First = s.From.In(from.Location())
				break
			}
		}
		for i := len(d.Stretches) - 1; i >= 0; i-- {
			if d.Stretches[i].Kind != string(report.KindPadding) {
				day.Last = d.Stretches[i].To.In(from.Location())
				break
			}
		}
	}
	day.OneNeighbour = d.OneNeighbour
	day.Claimed = d.Claimed
	// The pauses go back into the schedule. Stretches cover the blocks and
	// nothing else, so a day rebuilt from them has no gaps in it at all —
	// which would make a frozen day print a pause on its summary line and a
	// solid afternoon underneath, and Run says in as many words that a
	// timeline hiding them implies the day was solid.
	day.Runs = withGaps(day.Runs, pauses, from.Location())

	rep := report.Report{From: from, To: to, Options: opts, Days: []report.Day{day}}
	rep.Total = report.Totals{
		Attention: day.Attention, Background: day.Background, Active: day.Active,
		Span: day.Span, Padding: day.Padding, Agent: day.Agent,
		OneNeighbour: day.OneNeighbour, Claimed: day.Claimed, Projects: day.Projects,
	}
	return rep
}
