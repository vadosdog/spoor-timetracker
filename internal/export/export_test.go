// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package export

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// An export is the first thing that leaves this tool. It goes into a work log,
// an email, a shared folder — somewhere the rules of this project no longer
// apply — so the one thing it must never carry out with it is a trace.
//
// This is the exporter's half of TestNeitherRendererPrintsATitleOrALabel in
// internal/report: same idea, same shape, on the one output that is written to
// a file rather than to a terminal.
func TestAnExportCarriesNoTrace(t *testing.T) {
	const secret = "SECRET-QUERY-THAT-MUST-NOT-APPEAR"

	at := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	day := store.ConfirmedDay{
		Day:         "2026-05-04",
		ConfirmedAt: at,
		Claimed:     30 * time.Minute,
		OneOffCount: 1,
		Rows: []store.ConfirmedRow{
			{Project: "Widgets", Attention: time.Hour, Claimed: 30 * time.Minute},
			{Project: "Widgets", Subject: "rewrite", Attention: 20 * time.Minute},
			{Project: "", Attention: 10 * time.Minute},
		},
		Stretches: []store.ConfirmedStretch{
			{From: at, To: at.Add(time.Hour), Project: "Widgets", Subject: "rewrite",
				Kind: "attention", Ground: "rule"},
		},
		// The places a trace could get in by accident: an assignment's reason,
		// and a project or subject name. A name is usually the person's own
		// words — but a subject with no name takes it from a capture group,
		// which is the one case where the column holds matched text rather
		// than typed text, so that case is exercised too.
		OneOffs: []store.ConfirmedOneOff{
			{From: at, To: at.Add(time.Minute), Project: "Widgets",
				Reason: "all the keys here are on the never list: " + secret},
		},
	}

	for _, timeline := range []bool{false, true} {
		var out bytes.Buffer
		if err := (Markdown{Options{Timeline: timeline}}).Export(day, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), secret) {
			t.Errorf("timeline=%v: the export carries a trace:\n%s", timeline, out.String())
		}
	}
}

// Two exports of one day are the same bytes. A document that changes between
// two identical runs cannot be diffed, filed or trusted — and the rows come
// out of a map somewhere upstream, so the order has to be imposed rather than
// inherited.
func TestTwoExportsOfOneDayAreTheSameBytes(t *testing.T) {
	at := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	work := true
	day := store.ConfirmedDay{
		Day: "2026-05-04", ConfirmedAt: at,
		Rows: []store.ConfirmedRow{
			{Project: "Beta", Attention: time.Hour, Work: &work},
			{Project: "Alpha", Attention: time.Hour},
			{Project: "Alpha", Subject: "one", Attention: 30 * time.Minute},
			{Project: "Alpha", Subject: "two", Attention: 30 * time.Minute},
		},
	}
	var first, second bytes.Buffer
	for _, w := range []*bytes.Buffer{&first, &second} {
		if err := (Markdown{}).Export(day, w); err != nil {
			t.Fatal(err)
		}
	}
	if first.String() != second.String() {
		t.Errorf("two exports disagree:\n%s\n---\n%s", first.String(), second.String())
	}
	// Two rows with the same time are ordered by name, not by luck.
	if strings.Index(first.String(), "Alpha") > strings.Index(first.String(), "Beta") {
		t.Errorf("equal rows are not in name order:\n%s", first.String())
	}
}

// The lines that are somebody's answer rather than a measurement say so, and
// are not folded into anything.
func TestTheExportKeepsAnswersApartFromMeasurements(t *testing.T) {
	at := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	day := store.ConfirmedDay{
		Day: "2026-05-04", ConfirmedAt: at, Claimed: 30 * time.Minute,
		Rows: []store.ConfirmedRow{{Project: "Widgets", Attention: time.Hour, Claimed: 30 * time.Minute}},
	}
	var out bytes.Buffer
	if err := (Markdown{}).Export(day, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Pauses called work 0:30") {
		t.Errorf("the claimed pauses have no line:\n%s", got)
	}
	if !strings.Contains(got, "not part of the time above") {
		t.Errorf("nothing says the claimed pauses are outside the day:\n%s", got)
	}
	// One hour of attention, half an hour of pause: the project's time column
	// must be the hour.
	if !strings.Contains(got, "| Widgets | 1:00 |") {
		t.Errorf("the pause was folded into the project's time:\n%s", got)
	}
}

// An unconfirmed day says so before it says anything else.
func TestAProvisionalExportIsMarkedFirst(t *testing.T) {
	var out bytes.Buffer
	if err := (Markdown{Options{Unconfirmed: true}}).Export(store.ConfirmedDay{Day: "2026-05-04"}, &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "Not confirmed") {
		t.Fatalf("nothing says it is provisional:\n%s", body)
	}
	if strings.Index(body, "Not confirmed") > strings.Index(body, "Attention") {
		t.Errorf("the warning comes after the numbers it is about:\n%s", body)
	}
}

// An unknown format is refused by name rather than falling back to the only
// one there is.
func TestAnUnknownFormatIsRefused(t *testing.T) {
	if _, err := For("csv", Options{}); err == nil {
		t.Fatal("an unknown format was accepted")
	}
	e, err := For("", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if e.Name() != "markdown" {
		t.Errorf("the default format is %q", e.Name())
	}
}

// The one thing in an export that comes from a directory, and it is a
// decision rather than an oversight: a project no rule names keeps the guess
// its working directory gave it, and that guess is the last element of a path.
//
// Pinned here so that it stays a decision. It is the name the project has in
// every other view, and an export that renamed it would be a different
// document about the same day; turning the guess off, or naming the project,
// is one line of config either way.
func TestAGuessedProjectNameIsPrintedAsItIs(t *testing.T) {
	var out bytes.Buffer
	if err := (Markdown{}).Export(store.ConfirmedDay{
		Day:  "2026-05-04",
		Rows: []store.ConfirmedRow{{Project: "widget", Attention: time.Hour}},
	}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "widget") {
		t.Errorf("the project's own name was not printed:\n%s", out.String())
	}
}
