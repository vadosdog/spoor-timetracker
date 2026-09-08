// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package browser

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vadosdog/spoor-timetracker/internal/store"
)

// Everything below builds history databases from scratch. No test ever reads
// a real profile: the schemas here are the published ones, written out by
// hand, and the visits are invented.

// chromeSchema is the part of Chrome's History that spoor reads. The real
// table has some thirty more columns; leaving them out proves the query does
// not depend on them.
const chromeSchema = `
CREATE TABLE urls (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url LONGVARCHAR,
	title LONGVARCHAR,
	visit_count INTEGER DEFAULT 0 NOT NULL,
	last_visit_time INTEGER NOT NULL,
	hidden INTEGER DEFAULT 0 NOT NULL
);
CREATE TABLE visits (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	url INTEGER NOT NULL,
	visit_time INTEGER NOT NULL,
	from_visit INTEGER,
	transition INTEGER DEFAULT 0 NOT NULL,
	visit_duration INTEGER DEFAULT 0 NOT NULL
);`

const firefoxSchema = `
CREATE TABLE moz_places (
	id INTEGER PRIMARY KEY,
	url LONGVARCHAR,
	title LONGVARCHAR,
	rev_host LONGVARCHAR,
	visit_count INTEGER DEFAULT 0,
	last_visit_date INTEGER
);
CREATE TABLE moz_historyvisits (
	id INTEGER PRIMARY KEY,
	from_visit INTEGER,
	place_id INTEGER,
	visit_date INTEGER,
	visit_type INTEGER
);`

// hit is one visit to write into a fixture.
type hit struct {
	at         time.Time
	url        string
	title      string
	transition int64 // Chrome transition, or Firefox visit_type
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "spoor.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// chromeProfile writes a Chrome-shaped history and returns the profile.
func chromeProfile(t *testing.T, dir string, hits ...hit) Profile {
	t.Helper()
	path := filepath.Join(dir, chromeHistoryFile)
	db := create(t, path, chromeSchema)
	defer func() { _ = db.Close() }()

	appendChrome(t, db, hits...)
	return Profile{Flavour: "chrome", Name: filepath.Base(dir), Path: path}
}

func appendChrome(t *testing.T, db *sql.DB, hits ...hit) {
	t.Helper()
	for _, h := range hits {
		micros := h.at.UnixMicro() + chromeEpoch
		res, err := db.Exec(
			`INSERT INTO urls (url, title, last_visit_time) VALUES (?,?,?)`,
			h.url, h.title, micros)
		if err != nil {
			t.Fatal(err)
		}
		urlID, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO visits (url, visit_time, transition) VALUES (?,?,?)`,
			urlID, micros, h.transition); err != nil {
			t.Fatal(err)
		}
	}
}

// firefoxProfile writes a Firefox-shaped history and returns the profile.
func firefoxProfile(t *testing.T, dir string, hits ...hit) Profile {
	t.Helper()
	path := filepath.Join(dir, firefoxHistoryFile)
	db := create(t, path, firefoxSchema)
	defer func() { _ = db.Close() }()

	for i, h := range hits {
		placeID := int64(i + 1)
		if _, err := db.Exec(
			`INSERT INTO moz_places (id, url, title, last_visit_date) VALUES (?,?,?,?)`,
			placeID, h.url, nullable(h.title), h.at.UnixMicro()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(
			`INSERT INTO moz_historyvisits (id, place_id, visit_date, visit_type) VALUES (?,?,?,?)`,
			placeID, placeID, h.at.UnixMicro(), h.transition); err != nil {
			t.Fatal(err)
		}
	}
	return Profile{Flavour: "firefox", Name: filepath.Base(dir), Path: path}
}

// nullable turns an empty title into SQL NULL, which is what Firefox actually
// stores for a page that never reported one.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func create(t *testing.T, path, schema string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

// openFixture reopens a fixture for a test that wants to add visits to it.
func openFixture(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rowsOf reads back one column of every browser event, in time order.
func rowsOf(t *testing.T, st *store.Store, column string) []string {
	t.Helper()
	rows, err := st.DB().Query(
		`SELECT ` + column + ` FROM events WHERE source = 'browser' ORDER BY ts, external_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func at(hhmm string) time.Time {
	t, err := time.Parse(time.RFC3339, "2026-08-25T"+hhmm+":00Z")
	if err != nil {
		panic(err)
	}
	return t
}
