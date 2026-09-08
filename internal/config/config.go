// spoor — reconstruct the working day from digital traces already on disk.
// Copyright (C) 2026 spoor contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version. See the LICENSE file.

// Package config reads the YAML file spoor keeps under $XDG_CONFIG_HOME.
//
// There is no file by default and no file is ever written: an absent config
// means "nothing configured", not an error. Everything machine dependent
// lives here rather than in the source, which is why the repository contains
// no personal paths.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole file. It grows one section per stage.
type Config struct {
	Browser Browser `yaml:"browser"`
	Report  Report  `yaml:"report"`
}

// Report configures how events are turned into a day.
//
// Both thresholds are here rather than in a constant because both are
// measurements, and a measurement gets remeasured. The clustering one was
// taken from the density of pauses between events on two weeks of one
// person's data, and the buckets that decided it hold four to eleven
// observations each. Nobody should have to rebuild the binary to try another
// number.
type Report struct {
	// ClusterGap is how far apart two events can be and still belong to the
	// same block of work. Left out — or written as 0s — means the default,
	// ten minutes: a threshold of nothing would make every event a block of
	// its own, which nobody means by writing zero.
	ClusterGap Duration `yaml:"cluster_gap"`

	// AttentionWindow is the half-width of the window a moment of human
	// attention casts around itself: a prompt typed at 12:00 with a window of
	// 5m means the person was there from 11:55 to 12:05. Windows are merged,
	// and what is left of a block of work belongs to the agent rather than to
	// the person.
	//
	// Left out — or written as 0s — it follows ClusterGap: half of it. Not a
	// constant of its own, so that the two cannot drift apart when the
	// threshold is measured again.
	//
	// Unlike Head and Tail, zero here cannot mean zero: a window of nothing
	// would make every second of a block the agent's, and there would be no
	// way left to say "just use the default".
	AttentionWindow Duration `yaml:"attention_window"`

	// Head is how long writing a prompt takes, added before a block that opens
	// with one. Tail is how long reading the last answer takes, added after a
	// block that had a person in it anywhere. Neither is added to a block of
	// nothing but agent output: there was nobody there to write or to read.
	//
	// A pointer each, because leaving the key out has to mean the default
	// while writing "0s" has to mean "add nothing at all" — and this is the
	// one setting where somebody will genuinely want the second.
	Head *Duration `yaml:"head"`
	Tail *Duration `yaml:"tail"`

	// CountBackground adds the time the agent worked alone to the totals.
	// Off by default: the report says how long it was on a line of its own,
	// and whether that is your working time is your call, not the tool's.
	CountBackground bool `yaml:"count_background"`
}

// Duration is a time.Duration written the way a person writes one: "10m",
// "1h30m", "45s". yaml.v3 would otherwise want the number of nanoseconds.
type Duration time.Duration

// UnmarshalYAML reads a duration from a string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: want a duration like \"10m\", got %s", node.Line, node.Tag)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration like \"10m\"", node.Line, s)
	}
	if parsed < 0 {
		return fmt.Errorf("line %d: %q is negative", node.Line, s)
	}
	*d = Duration(parsed)
	return nil
}

// OrDefault is Or for a key that may legitimately be set to zero: an absent
// key gives def, and a present one gives whatever it says, including 0s.
func (d *Duration) OrDefault(def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return time.Duration(*d)
}

// Browser configures the browser history source.
type Browser struct {
	// Ignore lists domains that must never reach the database. An entry
	// matches the host itself and any subdomain of it, so "example.com"
	// covers "www.example.com" as well.
	//
	// The filter runs before the insert, not before the display. A visit to
	// an ignored domain is not stored, quietly kept and hidden — it is not
	// stored at all.
	Ignore []string `yaml:"ignore"`

	// History lists browser history databases to read on top of the ones
	// found automatically. Which browser a file belongs to is decided by its
	// name: "History" is read as Chrome, "places.sqlite" as Firefox. That is
	// how a Chromium fork or a Firefox fork in an unusual place can be tried
	// without spoor having to know it exists. Only Chrome has been run
	// against a real profile, so trying is all it is.
	History []string `yaml:"history"`
}

// Load reads the config file. A missing file yields a zero Config and no
// error: running without one has to keep working.
func Load(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}

	// KnownFields makes a misspelled key an error rather than a setting that
	// silently does nothing. An ignore list that is quietly not applied is
	// the worst possible failure for this particular file.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		// A file that holds only comments, or nothing at all, decodes to EOF.
		// That is an empty config, not a broken one.
		if errors.Is(err, io.EOF) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
