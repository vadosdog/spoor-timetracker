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
	"time"
)

// chromeEpoch is the offset between Chrome's clock and the Unix one: Chrome
// counts microseconds from 1601-01-01, Unix counts seconds from 1970-01-01.
const chromeEpoch = 11644473600000000

// chromeReader reads the History database Chrome and its relatives keep per
// profile.
//
// Only the visits are read, and of the urls table only the address and the
// title. Chrome also records visit_duration, and spoor deliberately does not
// store it: it measures the time until the tab navigated somewhere else, so a
// tab left open overnight reports a visit lasting all night. Summed over two
// weeks on real data it came to twenty-five times the length of the period.
// It is a real number that answers a different question, and putting it in a
// column called duration invites the reporting stage to add it up.
type chromeReader struct{}

func (chromeReader) flavour() string { return "chrome" }

// query returns visits at or after the watermark. The bound is inclusive so
// that a visit sharing its microsecond with the newest imported one is read
// again rather than lost; the dedup key makes reading it again free.
func (chromeReader) query(db *sql.DB, since time.Time) (*sql.Rows, error) {
	from := int64(0)
	if !since.IsZero() {
		from = since.UnixMicro() + chromeEpoch
	}
	// A LEFT JOIN rather than an inner one: a visit whose urls row has already
	// been expired still has to be counted as a row that was read, not
	// disappear between the count and the report.
	return db.Query(`
		SELECT v.id, v.visit_time, v.transition, u.url, u.title
		FROM visits v LEFT JOIN urls u ON u.id = v.url
		WHERE v.visit_time >= ?
		ORDER BY v.visit_time`, from)
}

// scan reads one row. Every column but the two keys is nullable in practice —
// this is somebody else's database — so a NULL costs the row and nothing more.
// Returning an error here would abort the whole profile, discard the batch
// that had already been read, and leave the watermark where it was, so a
// single bad row would mean that profile is never imported again.
func (chromeReader) scan(rows *sql.Rows) (visit, error) {
	var (
		v          visit
		micros     sql.NullInt64
		transition sql.NullInt64
		address    sql.NullString
		title      sql.NullString
	)
	if err := rows.Scan(&v.ID, &micros, &transition, &address, &title); err != nil {
		return visit{}, err
	}
	if !micros.Valid || !address.Valid {
		return visit{ID: v.ID}, nil // no time or no address: unusable, counted
	}
	v.Time = time.UnixMicro(micros.Int64 - chromeEpoch).UTC()
	v.URL = address.String
	v.Title = title.String
	// The low byte is the core transition; the high bits are qualifiers
	// (was it a redirect chain, did it come from the address bar) that say
	// nothing about when the person was at the keyboard.
	v.Transition = chromeTransitions[transition.Int64&0xFF]
	return v, nil
}

// chromeTransitions names the core transition types. Storing the number would
// keep the database honest and unreadable at the same time; the point of the
// schema is that a person can open it and understand it. An unlisted value
// yields an empty subtype rather than a guess.
var chromeTransitions = map[int64]string{
	0:  "link",
	1:  "typed",
	2:  "bookmark",
	3:  "auto_subframe",
	4:  "manual_subframe",
	5:  "generated",
	6:  "start_page",
	7:  "form_submit",
	8:  "reload",
	9:  "keyword",
	10: "keyword_generated",
}
