// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package appdirs resolves Inverz application data directories.
package appdirs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

var (
	// ErrHomeUnavailable reports that no suitable user data directory is available.
	ErrHomeUnavailable = errors.New("cannot determine application data directory")
)

// Paths contains the default product data root and an optional dotenv override.
type Paths struct {
	DataRoot string // Application data directory.
	DotEnv   string // Optional dotenv file override; empty selects .env in the working directory.
}

// Default resolves application paths from the current process environment.
func Default() (Paths, error) {
	return Resolve(os.LookupEnv, os.UserHomeDir)
}

// Resolve computes paths for the current operating system using injected environment and home lookups.
func Resolve(lookup func(string) (string, bool), userHome func() (string, error)) (Paths, error) {
	home, homeErr := userHome()
	localAppData, _ := lookup("LOCALAPPDATA")
	dataHome, _ := lookup("XDG_DATA_HOME")
	var result Paths

	switch runtime.GOOS {
	case "windows":
		if !filepath.IsAbs(localAppData) {
			return Paths{}, fmt.Errorf("LOCALAPPDATA: %w", ErrHomeUnavailable)
		}
		result.DataRoot = filepath.Join(localAppData, "inverz")

	case "darwin":
		if homeErr != nil || home == "" {
			return Paths{}, fmt.Errorf("user home: %w", ErrHomeUnavailable)
		}

		base := filepath.Join(home, "Library", "Application Support", "inverz")
		result.DataRoot = base

	case "linux":
		if filepath.IsAbs(dataHome) {
			result.DataRoot = filepath.Join(dataHome, "inverz")
		} else {
			if homeErr != nil || home == "" {
				return Paths{}, fmt.Errorf("user home: %w", ErrHomeUnavailable)
			}

			result.DataRoot = filepath.Join(home, ".local", "share", "inverz")
		}

	default:
		return Paths{}, fmt.Errorf("unsupported operating system %q", runtime.GOOS)
	}

	return result, nil
}
