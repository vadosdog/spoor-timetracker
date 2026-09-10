// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package calendar

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The address of a private feed is a password. These tests are about the ways
// it escapes without anybody writing a line of code to make it.

// secretURL is the shape Google hands out: the token is in the path, so
// anything that prints a URL prints the credential.
const secretURL = "https://calendar.example.invalid/ical/someone%40example.invalid/private-0123456789abcdef/basic.ics"

const secretPart = "private-0123456789abcdef"

// net/url's Error prints as `%s %q: %s` — the middle one is the whole URL. So
// the ordinary fmt.Errorf("...: %w", err) puts the credential on stderr the
// first time a connection is refused, and from there into a pasted issue.
func TestTransportErrorsDoNotCarryTheURLPath(t *testing.T) {
	for _, cause := range []error{
		errors.New("dial tcp 203.0.113.1:443: connect: connection refused"),
		errors.New("x509: certificate has expired"),
		io.ErrUnexpectedEOF,
	} {
		err := scrub(&url.Error{Op: "Get", URL: secretURL, Err: cause})
		if err == nil {
			t.Fatal("scrub returned nil")
		}
		if strings.Contains(err.Error(), secretPart) {
			t.Errorf("error %q carries the credential", err)
		}
		if !strings.Contains(err.Error(), cause.Error()) {
			t.Errorf("error %q lost the cause %q, which is the useful half", err, cause)
		}
	}
}

// Go sets Referer to the previous URL — path and all — before calling
// CheckRedirect. One hop would hand the credential to the next host in a
// header nobody looks at.
func TestRedirectDropsTheReferer(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://elsewhere.example.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Referer", secretURL)

	prev, _ := http.NewRequest(http.MethodGet, secretURL, nil)
	if err := checkRedirect(req, []*http.Request{prev}); err != nil {
		t.Fatalf("an ordinary redirect was refused: %v", err)
	}
	if got := req.Header.Get("Referer"); got != "" {
		t.Errorf("Referer = %q, want it removed", got)
	}
}

func TestRedirectRefusesPlainHTTPAndTooManyHops(t *testing.T) {
	plain, _ := http.NewRequest(http.MethodGet, "http://elsewhere.example.invalid/x", nil)
	if err := checkRedirect(plain, nil); !errors.Is(err, errBadScheme) {
		t.Errorf("a redirect to http gave %v, want it refused", err)
	}

	secure, _ := http.NewRequest(http.MethodGet, "https://elsewhere.example.invalid/x", nil)
	via := make([]*http.Request, maxRedirects)
	if err := checkRedirect(secure, via); err == nil {
		t.Errorf("%d redirects were allowed", maxRedirects)
	}
}

// net/http asks for gzip and unwraps it, so Content-Length describes the
// compressed body and a small response can expand without limit.
func TestBodyIsCappedAfterDecompression(t *testing.T) {
	if _, err := readCapped(strings.NewReader(strings.Repeat("x", 1024))); err != nil {
		t.Errorf("an ordinary body was refused: %v", err)
	}
	// A reader that never ends, which is what a decompression bomb looks like
	// by the time it reaches here.
	endless := endlessReader{}
	if _, err := readCapped(endless); err == nil {
		t.Error("an unbounded body was read to the end")
	}
}

type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// Refused before a packet is sent, and without echoing what was given.
func TestFetchRefusesAnythingButHTTPS(t *testing.T) {
	for _, raw := range []string{
		"http://calendar.example.invalid/ical/" + secretPart + "/basic.ics",
		"file:///etc/passwd",
		"webcal://calendar.example.invalid/" + secretPart,
	} {
		_, err := Fetch(context.Background(), raw)
		if err == nil {
			t.Errorf("%s was accepted", raw)
			continue
		}
		if strings.Contains(err.Error(), secretPart) {
			t.Errorf("error %q carries the credential", err)
		}
	}
}

func TestReadSecret(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "url")
	if err := os.WriteFile(path, []byte("# the work calendar\n\n"+secretURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSecret(path)
	if err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}
	if got != secretURL {
		t.Errorf("read %q, want the URL", got)
	}

	// An error names the path, which is not a secret, and never the contents,
	// which are.
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("# nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ReadSecret(empty)
	if err == nil {
		t.Fatal("a file with no URL was accepted")
	}
	if !strings.Contains(err.Error(), empty) {
		t.Errorf("error %q does not name the file", err)
	}

	if _, err := ReadSecret(filepath.Join(dir, "absent")); err == nil {
		t.Error("a missing file was accepted")
	}
}

// The failure that would be invisible: a calendar that cannot be read must
// cost that calendar, and must not be mistaken for a calendar with no
// meetings.
func TestOneBadCalendarDoesNotHideBehindSuccess(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.ics")
	if err := os.WriteFile(good, wrap(`
UID:a@example.invalid
SUMMARY:Kept
DTSTART:20260901T090000Z
DTEND:20260901T100000Z`), 0o600); err != nil {
		t.Fatal(err)
	}

	var rep Report
	for _, c := range []Calendar{
		{ID: "good", File: good},
		{ID: "missing", File: filepath.Join(dir, "nope.ics")},
	} {
		data, err := read(context.Background(), c)
		if err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			continue
		}
		ms, _, err := Parse(data, window("2026-08-01T00:00:00", "2026-10-01T00:00:00"))
		if err != nil {
			t.Fatal(err)
		}
		rep.Events += len(ms)
	}
	if rep.Events != 1 {
		t.Errorf("imported %d meetings, want the one from the readable calendar", rep.Events)
	}
	if len(rep.Errors) != 1 {
		t.Errorf("errors = %v, want exactly one", rep.Errors)
	}
}
