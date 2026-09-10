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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vadosdog/spoor-timetracker/internal/text"
)

// This is the only code in spoor that opens a socket, and the address it opens
// it to is a password.
//
// A private iCalendar URL is a bearer credential with no expiry and no scope:
// whoever holds it reads every meeting in the calendar — titles, descriptions,
// attendees, locations — which is far more than this program stores. It cannot
// be narrowed and it is revoked only by resetting it, which breaks every other
// subscription at the same time.
//
// So the rules here are about one thing: the URL must not leave this machine
// by any path except the request itself, and it must not be written anywhere a
// person might later copy. Three of Go's defaults work against that, and each
// is turned off below rather than hoped about.

const (
	// maxFeedBytes caps the response after decompression. A year of a busy
	// calendar is a couple of megabytes; this is generous by two orders of
	// magnitude and still finite.
	//
	// The cap is on the decompressed stream on purpose. net/http asks for
	// gzip and unwraps it transparently, so Content-Length describes the
	// compressed body and says nothing about what it expands to. A few
	// kilobytes of response can become gigabytes of memory, and the server is
	// not ours.
	maxFeedBytes = 64 << 20

	// maxRedirects is well under Go's default of ten. A calendar feed that
	// needs more than a couple of hops is not a calendar feed.
	maxRedirects = 3

	// fetchTimeout bounds the whole exchange, connection through last byte,
	// so a server that answers one byte a minute cannot hold a run open.
	fetchTimeout = 60 * time.Second
)

// errBadScheme is returned before anything is sent.
var errBadScheme = errors.New("a calendar URL must be https")

// Fetch downloads a feed.
//
// The URL's path is never returned inside an error, never logged and never
// written to disk. Callers get a description of what went wrong.
//
// The host can still appear, because a dial or certificate failure names it
// ("lookup calendar.example: no such host") and that is the half of the message
// worth having. For the feeds this is aimed at the host is generic and the
// whole secret is in the path — but the promise is stated as the code keeps it,
// not as it would be nicer to keep.
func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		// Deliberately not %w and deliberately not the value: url.Parse puts
		// what it was given into its error text.
		return nil, errors.New("the calendar URL could not be parsed")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, errBadScheme
	}

	client := &http.Client{
		Timeout:       fetchTimeout,
		CheckRedirect: checkRedirect,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("the calendar URL could not be used for a request")
	}
	req.Header.Set("Accept", "text/calendar, text/plain;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, scrub(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The status and nothing else. A 404 here usually means the address
		// was reset in the calendar's settings; saying which address would
		// put it in the terminal, which is where it must never be.
		// Through Printable: net/http does not validate the reason phrase, it
		// takes whatever bytes came off the wire up to the newline. Those
		// bytes then go to a terminal, and a direction override in them
		// misrepresents the message the same way it would misrepresent a title.
		return nil, fmt.Errorf("the calendar server answered %s", text.Printable(resp.Status))
	}

	body, err := readCapped(resp.Body)
	if err != nil {
		return nil, scrub(err)
	}
	return body, nil
}

// checkRedirect is the client's redirect policy, and it exists as a named
// function so that a test can call it without a network.
//
// Go fills in Referer from the previous request before calling this, and
// Referer is the whole previous URL — path included. One redirect, to
// anywhere, would hand the secret to the next host in a plain header.
// Deleting it here works because net/http assigns the Referer immediately
// above its call to CheckRedirect, not after it.
func checkRedirect(req *http.Request, via []*http.Request) error {
	req.Header.Del("Referer")
	if !strings.EqualFold(req.URL.Scheme, "https") {
		return errBadScheme
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("more than %d redirects", maxRedirects)
	}
	return nil
}

// readCapped reads at most maxFeedBytes and refuses anything longer.
//
// One byte more than the cap is read, so that hitting the limit is
// distinguishable from a feed that happens to be exactly that long.
func readCapped(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxFeedBytes {
		return nil, fmt.Errorf("the calendar feed is larger than %d bytes", maxFeedBytes)
	}
	return body, nil
}

// scrub removes the URL from a transport error.
//
// net/url's Error prints as `%s %q: %s` — operation, *the whole URL*, cause —
// so the ordinary `fmt.Errorf("fetch: %w", err)` puts a private calendar
// address into stderr on the first flaky connection. From there it goes into
// a screenshot, a pasted issue, a CI log. What is kept is the cause, which is
// what actually helps: "connection refused", "no such host", "certificate has
// expired". Those may name the host; they cannot name the path.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		if ue.Timeout() {
			return fmt.Errorf("the calendar server did not answer within %s", fetchTimeout)
		}
		return fmt.Errorf("could not reach the calendar server: %w", ue.Err)
	}
	return err
}
