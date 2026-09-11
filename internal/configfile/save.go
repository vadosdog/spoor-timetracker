// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package configfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Save writes the file back.
//
// Three things happen and each of them is about the same worry — this file is
// the only copy of a dictionary somebody built by hand over weeks:
//
//   - a file that changed on disk since it was read is refused outright. An
//     editor open in another window is the normal case, not an exotic one;
//   - the copy that was there is kept once per session, beside the file;
//   - the write is a temporary file and a rename, so a crash halfway leaves
//     the old file rather than half of a new one.
//
// The permissions are the ones the file had. A config can hold the paths of
// everything somebody works on, and a tool that widens them while saving would
// be doing it at the moment its owner is least likely to look.
func (f *File) Save() error {
	if f.existed {
		current, err := os.ReadFile(f.real)
		if err != nil {
			return fmt.Errorf("read %s: %w", f.path, err)
		}
		if sum(current) != f.sum {
			return fmt.Errorf(
				"%s changed on disk since spoor read it — nothing was written. "+
					"Save it in whatever else has it open, then run this again",
				f.path)
		}
		if !f.backedUp {
			// Removed first: os.WriteFile ignores the mode of a file that
			// already exists, so a .bak left behind with wider permissions
			// would keep them while receiving a fresh copy of the config.
			if err := os.Remove(f.real + ".bak"); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("replace the copy of %s: %w", f.path, err)
			}
			if err := os.WriteFile(f.real+".bak", current, f.mode); err != nil {
				return fmt.Errorf("keep a copy of %s: %w", f.path, err)
			}
			f.backedUp = true
		}
	} else {
		// The file was not there when it was read. If it is there now,
		// somebody wrote one while this was open — an editor, or the three
		// lines `add-calendar` prints — and renaming over it would lose that
		// with no copy kept, which is the one case nothing else guards.
		if _, err := os.Stat(f.real); err == nil {
			return fmt.Errorf(
				"%s appeared while spoor had it open — nothing was written. "+
					"Run this again and spoor will add to what is there now", f.path)
		}
		if dir := filepath.Dir(f.real); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("create config directory: %w", err)
			}
		}
	}

	data := []byte(f.Text())
	tmp, err := os.CreateTemp(filepath.Dir(f.real), ".spoor-config-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op once the rename succeeded

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	// Durable before it is visible: a rename onto the real name is atomic, but
	// only for content that has actually reached the disk.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	if err := os.Chmod(name, f.mode); err != nil {
		return fmt.Errorf("set permissions on %s: %w", f.path, err)
	}
	if err := os.Rename(name, f.real); err != nil {
		return fmt.Errorf("replace %s: %w", f.path, err)
	}

	f.existed = true
	f.sum = sum(data)
	return nil
}

// KeepBackup carries the "a copy has been kept" flag across a re-read.
//
// The copy is what the file looked like before spoor touched it, once per
// session. Re-reading the file after a write — which is what happens after
// every answer — would otherwise let the next write copy spoor's own output
// over the original, and the one artefact somebody would reach for after a
// mistake would be a copy of the mistake.
func (f *File) KeepBackup(from *File) {
	if from != nil && from.backedUp {
		f.backedUp = true
	}
}

// AttributionHash identifies the dictionary a day was confirmed against.
//
// It covers the attribution section and nothing else: a change to the browser
// ignore list or to a calendar address does not make yesterday's numbers
// suspect, and a hash that said it did would be a warning people learn to
// ignore. Comments and formatting are dropped before hashing for the same
// reason — reflowing a list is not a change to what it says.
func AttributionHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return hashOf(nil), nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	var root *yaml.Node
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	section := mapValue(root, "attribution")
	if section == nil {
		return hashOf(nil), nil
	}
	canonical(section)
	out, err := yaml.Marshal(section)
	if err != nil {
		return "", err
	}
	return hashOf(out), nil
}

func hashOf(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// canonical strips everything that is not what the rules say: comments, and
// the choice between writing a list in brackets and writing it over lines.
func canonical(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	n.Style = 0
	n.Line, n.Column = 0, 0
	for _, c := range n.Content {
		canonical(c)
	}
}
