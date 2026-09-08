// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// File names, and the fact that the name decides how a file is read. A
// Chromium fork or a Firefox fork keeps the same file under the same name in
// a different directory, so pointing spoor at one is how you would try it
// without the tool having to know that browser exists. Whether it then reads
// is untested: only Chrome has been run against a real profile.
const (
	chromeHistoryFile  = "History"
	firefoxHistoryFile = "places.sqlite"
)

// Profile is one browser profile's history database.
type Profile struct {
	// Flavour is "chrome" or "firefox": which schema the file has.
	Flavour string
	// Name is the profile directory's own name, e.g. "Profile 1". It is part
	// of the dedup key, because two profiles number their visits separately.
	Name string
	// Path is the history database itself.
	Path string
}

// supportedOS lists the systems this source knows where to look on. On
// anything else it declares itself absent and says nothing, rather than
// failing or inventing a path.
var supportedOS = map[string]bool{
	"linux":   true,
	"darwin":  true,
	"windows": true,
}

// Supported reports whether the browser source runs on this system at all.
func Supported() bool { return supportedOS[runtime.GOOS] }

// Discover finds the browser profiles on this machine.
//
// A directory that does not exist is not an error — most people have one
// browser, not four — and neither is finding nothing at all. On an
// unsupported system it returns nothing without looking.
func Discover() []Profile {
	if !Supported() {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var found []Profile
	for _, root := range chromeRoots(home) {
		found = append(found, profilesUnder(root, "chrome", chromeHistoryFile)...)
	}
	for _, root := range firefoxRoots(home) {
		found = append(found, profilesUnder(root, "firefox", firefoxHistoryFile)...)
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
	return found
}

// profilesUnder lists the profile directories directly under root that hold a
// history database of the given name.
func profilesUnder(root, flavour, file string) []Profile {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil // absent, or not ours to read: this browser is not here
	}

	var found []Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), file)
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		found = append(found, Profile{Flavour: flavour, Name: e.Name(), Path: path})
	}
	return found
}

// ProfileAt describes a history database the user named explicitly, deciding
// how to read it from the file name. It exists so that a browser spoor has
// never heard of can be pointed at rather than being out of reach — not as a
// claim that it will read, which nobody has checked.
//
// The second result is false when the name means nothing to this source.
func ProfileAt(path string) (Profile, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := Profile{Name: filepath.Base(filepath.Dir(abs)), Path: abs}
	switch filepath.Base(abs) {
	case chromeHistoryFile:
		p.Flavour = "chrome"
	case firefoxHistoryFile:
		p.Flavour = "firefox"
	default:
		return Profile{}, false
	}
	return p, true
}

// The locations below are where each browser keeps its profiles. Only the
// Linux ones have been run against a real installation; the macOS and Windows
// paths come from each browser's own documentation and are unverified, which
// is why finding nothing there is silence rather than an error.
func chromeRoots(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Google", "Chrome")}
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return []string{filepath.Join(dir, "Google", "Chrome", "User Data")}
		}
		return nil
	default:
		return []string{
			filepath.Join(home, ".config", "google-chrome"),
			// Flatpak gives every application its own home.
			filepath.Join(home, ".var", "app", "com.google.Chrome", "config", "google-chrome"),
		}
	}
}

func firefoxRoots(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles")}
	case "windows":
		if dir := os.Getenv("APPDATA"); dir != "" {
			return []string{filepath.Join(dir, "Mozilla", "Firefox", "Profiles")}
		}
		return nil
	default:
		return []string{
			filepath.Join(home, ".mozilla", "firefox"),
			// Snap and Flatpak, which is how Firefox arrives on most
			// distributions now.
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
			filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		}
	}
}
