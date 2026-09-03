// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package claudecode

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vadosdog/spoor-timetracker/internal/event"
	"github.com/vadosdog/spoor-timetracker/internal/store"
)

const (
	// batchSize is how many events go into one transaction.
	batchSize = 2000

	// seamSize is how many bytes before the read offset are hashed to tell
	// whether the path still holds the content that offset was measured
	// against.
	seamSize = 256

	// maxLine caps how much is buffered for a single JSONL line. The longest
	// line measured on real data is under 1.5 MB; this leaves two orders of
	// magnitude of room and still keeps a file with no newline in it from
	// being loaded whole into memory.
	maxLine = 32 << 20
)

// Report is what one import run did. Printed by the CLI and asserted on
// by the tests.
type Report struct {
	FilesSeen    int
	FilesRead    int
	FilesSkipped int // unchanged since the previous run
	LinesRead    int
	Events       int // lines that became events
	NewEvents    int // of those, rows that were not already in the database
	SkippedState int // no timestamp or no uuid: session state, expected
	Malformed    int // not JSON, or an unparseable timestamp
	Oversized    int // a single line longer than maxLine, dropped
	Errors       []string
}

// Ingest reads every *.jsonl under root and writes the events it finds.
//
// Files are read incrementally: JSONL is append-only, so an unchanged file is
// skipped outright and a grown one is read from where the previous run stopped.
// A file that has vanished is simply not seen — its events stay in the
// database, which is the entire point of accumulating rather than rebuilding.
func Ingest(st *store.Store, root string) (Report, error) {
	var rep Report

	files, err := findSessionFiles(root, &rep.Errors)
	if err != nil {
		return rep, err
	}
	rep.FilesSeen = len(files)

	for _, path := range files {
		if err := ingestFile(st, path, &rep); err != nil {
			// One unreadable file must not cost us the rest of the import.
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", path, err))
		}
	}
	return rep, nil
}

// findSessionFiles lists session logs in a stable order. A missing root is not
// an error: it just means this source has nothing to offer on this machine.
func findSessionFiles(root string, problems *[]string) ([]string, error) {
	var files []string
	var denied []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing root means the source is simply not present on this
			// machine. A directory we may not read is different: those are
			// hours lost silently, so say so.
			if errors.Is(err, fs.ErrPermission) {
				denied = append(denied, path)
				return nil
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	for _, path := range denied {
		*problems = append(*problems, fmt.Sprintf("%s: permission denied, skipped", path))
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func ingestFile(st *store.Store, path string, rep *Report) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // deleted between listing and reading; nothing to do
		}
		return err
	}

	prev, known, err := st.FileState(SourceName, path)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Resume where we stopped, but only if this is still the same file that
	// was read to that point. A shrunken file, an offset past the end, or a
	// seam that no longer hashes the same all mean the path now holds
	// different content: read it from the start and let the dedup key throw
	// away what is already stored.
	offset := int64(0)
	if known && info.Size() >= prev.Size && prev.ReadOffset <= info.Size() {
		seam, err := seamHash(f, prev.ReadOffset)
		if err != nil {
			return err
		}
		if seam == prev.SeamHash {
			offset = prev.ReadOffset

			// Unchanged since last time: same size, same mtime, same seam.
			// Nothing was appended, so there is nothing to read.
			if prev.Size == info.Size() && prev.MTime.Equal(info.ModTime()) {
				rep.FilesSkipped++
				return nil
			}
		}
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	rep.FilesRead++
	consumed, err := readLines(st, f, offset, rep)
	if err != nil {
		return err
	}

	seam, err := seamHash(f, consumed)
	if err != nil {
		return err
	}

	// The offset is saved only after the events are committed. Crash in
	// between and the next run re-reads the tail, which dedup makes free.
	return st.SaveFileState(SourceName, path, store.FileState{
		Size:       info.Size(),
		MTime:      info.ModTime(),
		ReadOffset: consumed,
		SeamHash:   seam,
	})
}

// seamHash fingerprints the bytes immediately before offset — up to seamSize
// of them — and leaves the file position where it found it.
//
// Appending never touches those bytes, so the hash survives a growing log and
// changes the moment the content behind the offset is not what was read.
func seamHash(f *os.File, offset int64) (string, error) {
	start := offset - seamSize
	if start < 0 {
		start = 0
	}

	buf := make([]byte, offset-start)
	if len(buf) > 0 {
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
	}

	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// readLines imports complete lines and returns the offset just past the last
// one. A trailing fragment (the file is being written to right now) is left
// for the next run.
func readLines(st *store.Store, f *os.File, offset int64, rep *Report) (int64, error) {
	reader := bufio.NewReaderSize(f, 1<<20)
	batch := make([]event.Event, 0, batchSize)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := st.InsertEvents(batch)
		if err != nil {
			return err
		}
		rep.NewEvents += n
		batch = batch[:0]
		return nil
	}

	for {
		line, consumed, oversized, err := readLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break // whatever is left has no newline yet: not our line
			}
			return offset, err
		}

		offset += consumed
		rep.LinesRead++

		if oversized {
			rep.Oversized++
			continue
		}

		ev, outcome := ParseLine(line)
		switch outcome {
		case Parsed:
			rep.Events++
			batch = append(batch, ev)
			if len(batch) >= batchSize {
				if err := flush(); err != nil {
					return offset, err
				}
			}
		case SkippedState:
			rep.SkippedState++
		case Malformed:
			rep.Malformed++
		}
	}

	return offset, flush()
}

// readLine returns the next complete line and how many bytes it occupied.
//
// A line longer than maxLine is consumed but not kept: one pathological line
// must not be able to allocate the whole file. An unterminated tail returns
// io.EOF and no byte count, so the offset does not move past it.
func readLine(r *bufio.Reader) (line []byte, consumed int64, oversized bool, err error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		consumed += int64(len(chunk))

		if !oversized {
			if int64(len(buf))+int64(len(chunk)) > maxLine {
				oversized, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}

		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue // the line spans more than one buffer
		case err != nil:
			return nil, 0, false, err
		}
		return buf, consumed, oversized, nil
	}
}
