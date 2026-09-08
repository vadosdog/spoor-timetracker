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

	"gopkg.in/yaml.v3"
)

// Config is the whole file. It grows one section per stage; today only the
// browser source has anything to configure.
type Config struct {
	Browser Browser `yaml:"browser"`
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
