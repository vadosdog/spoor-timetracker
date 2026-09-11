// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Assignment is one thing a person said about one stretch of one day, where no
// rule could say it. See the assignment table in schema.sql.
type Assignment struct {
	Day      string // local date, YYYY-MM-DD
	From, To time.Time
	Project  string
	Subject  string
	// ClearSubject distinguishes "leave the subject alone" from "it has none".
	ClearSubject bool
	// OneOff marks an answer that could not have become a rule, and Reason
	// says why not — in words, because the point of counting these is that
	// somebody reads them later.
	OneOff bool
	Reason string
}

// SaveAssignment records an answer, replacing any earlier one about exactly
// the same stretch. Somebody answering twice about one stretch has changed
// their mind, and two rows saying different things would leave the report
// picking one.
func (s *Store) SaveAssignment(a Assignment) error {
	_, err := s.db.Exec(`
		INSERT INTO assignment (day, from_ts, to_ts, project, subject,
		                        clear_subject, one_off, reason, made_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (day, from_ts, to_ts) DO UPDATE SET
			project = excluded.project,
			subject = excluded.subject,
			clear_subject = excluded.clear_subject,
			one_off = excluded.one_off,
			reason = excluded.reason,
			made_at = excluded.made_at`,
		a.Day, formatTS(a.From), formatTS(a.To), a.Project, a.Subject,
		a.ClearSubject, a.OneOff, a.Reason, s.now().UTC().Format(time.RFC3339))
	return err
}

// Assignments returns what was said about one day, oldest answer first.
func (s *Store) Assignments(day string) ([]Assignment, error) {
	rows, err := s.db.Query(`
		SELECT from_ts, to_ts, project, subject, clear_subject, one_off, reason
		FROM assignment WHERE day = ? ORDER BY made_at, from_ts, to_ts`, day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Assignment
	for rows.Next() {
		a := Assignment{Day: day}
		var from, to string
		if err := rows.Scan(&from, &to, &a.Project, &a.Subject,
			&a.ClearSubject, &a.OneOff, &a.Reason); err != nil {
			return nil, err
		}
		if a.From, err = time.Parse(time.RFC3339, from); err != nil {
			return nil, fmt.Errorf("unreadable assignment time %q: %w", from, err)
		}
		if a.To, err = time.Parse(time.RFC3339, to); err != nil {
			return nil, fmt.Errorf("unreadable assignment time %q: %w", to, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ConfirmedDay is a day whose result has been frozen.
type ConfirmedDay struct {
	Day             string
	ConfirmedAt     time.Time
	ConfigHash      string
	Version         string
	ClusterGap      time.Duration
	AttentionWindow time.Duration
	Head, Tail      time.Duration
	CountBackground bool
	// OneNeighbour is how much of the day rests on a block with a named
	// neighbour on one side and nothing on the other — the weakest rule here.
	OneNeighbour time.Duration
	// Claimed is time between blocks the person said was work. Its own line,
	// never part of the day.
	Claimed           time.Duration
	QuestionsTotal    int
	QuestionsAnswered int
	OneOffCount       int

	Stretches []ConfirmedStretch
	Rows      []ConfirmedRow
	OneOffs   []ConfirmedOneOff
}

// ConfirmedStretch is one piece of the partition of a confirmed day.
type ConfirmedStretch struct {
	From, To time.Time
	Project  string
	Subject  string
	Kind     string
	Ground   string
}

// ConfirmedRow is one line of a confirmed day's table. A row with an empty
// subject is the project's own line.
type ConfirmedRow struct {
	Project string
	Subject string
	Work    *bool
	// Events is how many traces carry the row. The count is copied into the
	// snapshot; the evidence behind it — which hosts, which directories — is
	// not. See the column comment in schema.sql.
	Events    int
	Attention time.Duration
	// Claimed is time between blocks the person said belonged to this row.
	// Never part of attention or background: nothing measured it.
	Claimed    time.Duration
	Background time.Duration
	Padding    time.Duration
	Agent      time.Duration
	Wall       time.Duration
}

// ConfirmedOneOff is an assignment in a confirmed day that could not have been
// a rule.
type ConfirmedOneOff struct {
	From, To time.Time
	Project  string
	Subject  string
	Reason   string
}

// Confirm writes a day's snapshot, replacing whatever was there.
//
// All of it in one transaction: a day whose rows were written and whose
// stretches were not would be a day that adds up differently depending on
// which table you read, which is worse than no snapshot at all.
func (s *Store) Confirm(d ConfirmedDay) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range []string{"confirmed_stretch", "confirmed_row", "confirmed_one_off", "confirmed_day"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE day = ?`, d.Day); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO confirmed_day (day, confirmed_at, config_hash, spoor_version,
			cluster_gap_ms, attention_window_ms, head_ms, tail_ms, count_background,
			one_neighbour_ms, claimed_ms, questions_total, questions_answered, one_off_count)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.Day, d.ConfirmedAt.UTC().Format(time.RFC3339), d.ConfigHash, d.Version,
		d.ClusterGap.Milliseconds(), d.AttentionWindow.Milliseconds(),
		d.Head.Milliseconds(), d.Tail.Milliseconds(), d.CountBackground,
		d.OneNeighbour.Milliseconds(), d.Claimed.Milliseconds(),
		d.QuestionsTotal, d.QuestionsAnswered, d.OneOffCount); err != nil {
		return err
	}
	for _, st := range d.Stretches {
		if _, err := tx.Exec(`
			INSERT INTO confirmed_stretch (day, from_ts, to_ts, project, subject, kind, ground)
			VALUES (?,?,?,?,?,?,?)`,
			d.Day, formatTS(st.From), formatTS(st.To), st.Project, st.Subject,
			st.Kind, st.Ground); err != nil {
			return err
		}
	}
	for _, r := range d.Rows {
		var work any
		if r.Work != nil {
			work = *r.Work
		}
		if _, err := tx.Exec(`
			INSERT INTO confirmed_row (day, project, subject, work, events,
				attention_ms, claimed_ms, background_ms, padding_ms, agent_ms, wall_ms)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			d.Day, r.Project, r.Subject, work, r.Events,
			r.Attention.Milliseconds(), r.Claimed.Milliseconds(), r.Background.Milliseconds(),
			r.Padding.Milliseconds(), r.Agent.Milliseconds(), r.Wall.Milliseconds()); err != nil {
			return err
		}
	}
	for _, o := range d.OneOffs {
		if _, err := tx.Exec(`
			INSERT INTO confirmed_one_off (day, from_ts, to_ts, project, subject, reason)
			VALUES (?,?,?,?,?,?)`,
			d.Day, formatTS(o.From), formatTS(o.To), o.Project, o.Subject, o.Reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Confirmed reads a day's snapshot back. The second value is false when the
// day has not been confirmed.
func (s *Store) Confirmed(day string) (ConfirmedDay, bool, error) {
	var (
		d                       ConfirmedDay
		confirmedAt             string
		gap, window, head, tail int64
		oneNeighbour, claimed   int64
	)
	err := s.db.QueryRow(`
		SELECT day, confirmed_at, config_hash, spoor_version, cluster_gap_ms,
		       attention_window_ms, head_ms, tail_ms, count_background,
		       one_neighbour_ms, claimed_ms, questions_total, questions_answered, one_off_count
		FROM confirmed_day WHERE day = ?`, day).Scan(
		&d.Day, &confirmedAt, &d.ConfigHash, &d.Version, &gap, &window, &head, &tail,
		&d.CountBackground, &oneNeighbour, &claimed,
		&d.QuestionsTotal, &d.QuestionsAnswered, &d.OneOffCount)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ConfirmedDay{}, false, nil
	case err != nil:
		return ConfirmedDay{}, false, err
	}
	if d.ConfirmedAt, err = time.Parse(time.RFC3339, confirmedAt); err != nil {
		return ConfirmedDay{}, false, fmt.Errorf("unreadable confirmation time %q: %w", confirmedAt, err)
	}
	d.ClusterGap = time.Duration(gap) * time.Millisecond
	d.AttentionWindow = time.Duration(window) * time.Millisecond
	d.Head = time.Duration(head) * time.Millisecond
	d.Tail = time.Duration(tail) * time.Millisecond
	d.OneNeighbour = time.Duration(oneNeighbour) * time.Millisecond
	d.Claimed = time.Duration(claimed) * time.Millisecond

	stretches, err := s.db.Query(`
		SELECT from_ts, to_ts, project, subject, kind, ground
		FROM confirmed_stretch WHERE day = ? ORDER BY from_ts`, day)
	if err != nil {
		return ConfirmedDay{}, false, err
	}
	for stretches.Next() {
		var st ConfirmedStretch
		var from, to string
		if err := stretches.Scan(&from, &to, &st.Project, &st.Subject, &st.Kind, &st.Ground); err != nil {
			_ = stretches.Close()
			return ConfirmedDay{}, false, err
		}
		if st.From, err = time.Parse(time.RFC3339, from); err != nil {
			_ = stretches.Close()
			return ConfirmedDay{}, false, err
		}
		if st.To, err = time.Parse(time.RFC3339, to); err != nil {
			_ = stretches.Close()
			return ConfirmedDay{}, false, err
		}
		d.Stretches = append(d.Stretches, st)
	}
	// Close returns the driver's error, never the one that ended the loop, so
	// an iteration that broke partway is invisible without this. A confirmed
	// day with fewer stretches than it was written with is a partition that no
	// longer tiles the day — silently, and reported as a successful read.
	if err := stretches.Err(); err != nil {
		_ = stretches.Close()
		return ConfirmedDay{}, false, err
	}
	if err := stretches.Close(); err != nil {
		return ConfirmedDay{}, false, err
	}

	// Ordered so that the table comes out the same way twice: busiest first is
	// a decision for whoever prints it, and a stable order here is what makes
	// two exports of one day byte for byte the same.
	rows, err := s.db.Query(`
		SELECT project, subject, work, events, attention_ms, claimed_ms,
		       background_ms, padding_ms, agent_ms, wall_ms
		FROM confirmed_row WHERE day = ? ORDER BY project, subject`, day)
	if err != nil {
		return ConfirmedDay{}, false, err
	}
	for rows.Next() {
		var r ConfirmedRow
		var work sql.NullBool
		var attention, claimed, background, padding, agent, wall int64
		if err := rows.Scan(&r.Project, &r.Subject, &work, &r.Events,
			&attention, &claimed, &background, &padding, &agent, &wall); err != nil {
			_ = rows.Close()
			return ConfirmedDay{}, false, err
		}
		if work.Valid {
			w := work.Bool
			r.Work = &w
		}
		r.Attention = time.Duration(attention) * time.Millisecond
		r.Claimed = time.Duration(claimed) * time.Millisecond
		r.Background = time.Duration(background) * time.Millisecond
		r.Padding = time.Duration(padding) * time.Millisecond
		r.Agent = time.Duration(agent) * time.Millisecond
		r.Wall = time.Duration(wall) * time.Millisecond
		d.Rows = append(d.Rows, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ConfirmedDay{}, false, err
	}
	if err := rows.Close(); err != nil {
		return ConfirmedDay{}, false, err
	}

	offs, err := s.db.Query(`
		SELECT from_ts, to_ts, project, subject, reason
		FROM confirmed_one_off WHERE day = ? ORDER BY from_ts`, day)
	if err != nil {
		return ConfirmedDay{}, false, err
	}
	defer func() { _ = offs.Close() }()
	for offs.Next() {
		var o ConfirmedOneOff
		var from, to string
		if err := offs.Scan(&from, &to, &o.Project, &o.Subject, &o.Reason); err != nil {
			return ConfirmedDay{}, false, err
		}
		if o.From, err = time.Parse(time.RFC3339, from); err != nil {
			return ConfirmedDay{}, false, err
		}
		if o.To, err = time.Parse(time.RFC3339, to); err != nil {
			return ConfirmedDay{}, false, err
		}
		d.OneOffs = append(d.OneOffs, o)
	}
	return d, true, offs.Err()
}

// ConfirmedDays lists every confirmed day, newest first, without the detail.
func (s *Store) ConfirmedDays() ([]ConfirmedDay, error) {
	rows, err := s.db.Query(`
		SELECT day, confirmed_at, config_hash, spoor_version, questions_total,
		       questions_answered, one_off_count
		FROM confirmed_day ORDER BY day DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ConfirmedDay
	for rows.Next() {
		var d ConfirmedDay
		var at string
		if err := rows.Scan(&d.Day, &at, &d.ConfigHash, &d.Version,
			&d.QuestionsTotal, &d.QuestionsAnswered, &d.OneOffCount); err != nil {
			return nil, err
		}
		if d.ConfirmedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ConfirmedDaysDisturbed names the confirmed days whose evidence has moved
// since they were frozen.
//
// A frozen day is an answer somebody has acted on, and until this stage
// nothing could take events out from under one. The calendar can: a meeting
// that moved, was renamed or was cancelled is *restated* on the next import,
// and restating deletes. An appending source can reach backwards too — a
// history database is written in place, and a device that synced late brings
// visits dated last week.
//
// The count is the fingerprint. Every event of a day carries exactly one
// project, so the project rows of a snapshot add up to the events the day had
// when it was frozen; comparing that with the events it has now says whether
// anything was added or taken away, whoever did it and however long ago. It
// cannot say which event, and it is not meant to: the point is to say out loud
// that a number somebody filed no longer describes the day underneath it.
func (s *Store) ConfirmedDaysDisturbed(loc *time.Location) ([]string, error) {
	// Driven by confirmed_day, not by confirmed_row. A day that held nothing
	// when it was frozen has no rows at all — a day away, or a day confirmed
	// before the browser history had been imported — and driving the loop off
	// the rows would leave exactly that day out of the comparison. It is the
	// case the check is most obviously for: the next import lands four hundred
	// visits on it and the report goes on saying nothing was recorded.
	rows, err := s.db.Query(`
		SELECT d.day, COALESCE(SUM(r.events), 0)
		FROM confirmed_day d
		LEFT JOIN confirmed_row r ON r.day = d.day AND r.subject = ''
		GROUP BY d.day ORDER BY d.day`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	frozen := map[string]int{}
	var days []string
	for rows.Next() {
		var day string
		var events int
		if err := rows.Scan(&day, &events); err != nil {
			return nil, err
		}
		frozen[day] = events
		days = append(days, day)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []string
	for _, day := range days {
		at, err := time.ParseInLocation(time.DateOnly, day, loc)
		if err != nil {
			return nil, err
		}
		now, err := s.CountEvents("", at, at.AddDate(0, 0, 1))
		if err != nil {
			return nil, err
		}
		if now != frozen[day] {
			out = append(out, day)
		}
	}
	return out, nil
}

// WindowAnswer is one pause somebody has said was, or was not, work.
//
// No measured number moves because of one — see the window_answer table in
// schema.sql. What it buys is a line of its own.
type WindowAnswer struct {
	Day      string
	From, To time.Time
	Worked   bool
	// Project and Subject are what it was, when it was work.
	Project string
	Subject string
	Left    string
	Right   string
}

// SaveWindowAnswer records one, replacing every earlier answer it overlaps.
//
// Overlaps, not exact matches. Block boundaries move whenever a setting does
// or an import lands, so the pause somebody is answering today may be a
// slightly different stretch from the one they answered last week — and
// keeping both would leave two answers about one piece of an afternoon, with
// the older one winning the minutes they share. Worse for a retraction: "this
// was not work" would be stored beside "this was", and the sum would keep the
// claim.
//
// Somebody answering twice about overlapping time has changed their mind. The
// later answer is the one they meant, which is the rule assignments already
// follow.
func (s *Store) SaveWindowAnswer(a WindowAnswer) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		DELETE FROM window_answer
		WHERE day = ? AND from_ts < ? AND to_ts > ?`,
		a.Day, formatTS(a.To), formatTS(a.From)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO window_answer (day, from_ts, to_ts, worked, project, subject,
		                           left_project, right_project, answered_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (day, from_ts, to_ts) DO UPDATE SET
			worked = excluded.worked,
			project = excluded.project,
			subject = excluded.subject,
			left_project = excluded.left_project,
			right_project = excluded.right_project,
			answered_at = excluded.answered_at`,
		a.Day, formatTS(a.From), formatTS(a.To), a.Worked, a.Project, a.Subject,
		a.Left, a.Right, s.now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// WindowAnswers returns what has been said about one day's pauses.
func (s *Store) WindowAnswers(day string) ([]WindowAnswer, error) {
	rows, err := s.db.Query(`
		SELECT from_ts, to_ts, worked, project, subject, left_project, right_project
		FROM window_answer WHERE day = ? ORDER BY from_ts`, day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []WindowAnswer
	for rows.Next() {
		a := WindowAnswer{Day: day}
		var from, to string
		if err := rows.Scan(&from, &to, &a.Worked, &a.Project, &a.Subject,
			&a.Left, &a.Right); err != nil {
			return nil, err
		}
		if a.From, err = time.Parse(time.RFC3339, from); err != nil {
			return nil, err
		}
		if a.To, err = time.Parse(time.RFC3339, to); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetClaimed refreshes the one number in a confirmed day that is not a
// measurement: time between blocks the person said was work.
//
// A confirmed day does not move, and this is not it moving. Everything the
// snapshot measured stays exactly as it was; what changes is a line that was
// never measured in the first place and is never summed into anything. Locking
// it would mean the one screen that moves no measured number could only be
// reached by throwing the day away and computing it again.
//
// The rows go with the total. Refreshing one and leaving the other where it was
// is how a snapshot stops adding up to itself — and the rows are where "which
// project was that pause" lives, which is the whole reason the answer carries a
// project at all.
func (s *Store) SetClaimed(day string, claimed time.Duration, byRow map[[2]string]time.Duration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE confirmed_day SET claimed_ms = ? WHERE day = ?`,
		claimed.Milliseconds(), day); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE confirmed_row SET claimed_ms = 0 WHERE day = ?`, day); err != nil {
		return err
	}
	for key, held := range byRow {
		// A project a pause was attached to may have no measured time at all
		// on this day, and then it has no row to update. It gets one: an hour
		// somebody attached to something is not a reason to lose the
		// something.
		res, err := tx.Exec(`UPDATE confirmed_row SET claimed_ms = ? WHERE day = ? AND project = ? AND subject = ?`,
			held.Milliseconds(), day, key[0], key[1])
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			if _, err := tx.Exec(`
				INSERT INTO confirmed_row (day, project, subject, events,
					attention_ms, claimed_ms, background_ms, padding_ms, agent_ms, wall_ms)
				VALUES (?,?,?,0,0,?,0,0,0,0)`,
				day, key[0], key[1], held.Milliseconds()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
