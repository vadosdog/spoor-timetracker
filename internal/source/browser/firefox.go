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

// firefoxReader reads places.sqlite, the database Firefox keeps per profile.
//
// Firefox counts microseconds from the Unix epoch, so no epoch arithmetic is
// needed, and it reports no per-visit duration at all — which is the honest
// answer, and the same one spoor gives.
//
// Unlike the Chrome half of this source, this one has not been run against a
// real profile: there is no Firefox on the machine spoor was written on. It
// is written against the documented schema and covered by tests on synthetic
// databases, which is not the same as having been used.
type firefoxReader struct{}

func (firefoxReader) flavour() string { return "firefox" }

// query returns visits at or after the watermark, inclusive for the same
// reason as in the Chrome reader.
func (firefoxReader) query(db *sql.DB, since time.Time) (*sql.Rows, error) {
	from := int64(0)
	if !since.IsZero() {
		from = since.UnixMicro()
	}
	// LEFT JOIN, and the NULL check is in scan rather than in the WHERE, for
	// the same reasons as in the Chrome reader: a row that cannot be used is
	// still a row that was read.
	return db.Query(`
		SELECT v.id, v.visit_date, v.visit_type, p.url, p.title
		FROM moz_historyvisits v LEFT JOIN moz_places p ON p.id = v.place_id
		WHERE v.visit_date >= ?
		ORDER BY v.visit_date`, from)
}

func (firefoxReader) scan(rows *sql.Rows) (visit, error) {
	var (
		v         visit
		micros    sql.NullInt64
		visitType sql.NullInt64
		address   sql.NullString
		title     sql.NullString
	)
	if err := rows.Scan(&v.ID, &micros, &visitType, &address, &title); err != nil {
		return visit{}, err
	}
	if !micros.Valid || !address.Valid {
		return visit{ID: v.ID}, nil // no time or no address: unusable, counted
	}
	v.Time = time.UnixMicro(micros.Int64).UTC()
	v.URL = address.String
	v.Title = title.String
	v.Transition = firefoxVisitTypes[visitType.Int64]
	return v, nil
}

// firefoxVisitTypes names the visit types. The vocabulary is close to
// Chrome's but not identical — Firefox tells a permanent redirect from a
// temporary one, Chrome does not — and each browser's own names are kept
// rather than flattened into a shared invention.
var firefoxVisitTypes = map[int64]string{
	1: "link",
	2: "typed",
	3: "bookmark",
	4: "embed",
	5: "redirect_permanent",
	6: "redirect_temporary",
	7: "download",
	8: "framed_link",
	9: "reload",
}
