// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/woozymasta/inverz"
)

// profileMissingError suggests the exact synchronization command for missing local data.
func profileMissingError(algorithm inverz.Algorithm, p inverz.Profile, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	return fmt.Errorf("index %s/%s is not installed; run `inverz pull %s`: %w", algorithm, p.String(), algorithm, err)
}

// ensureDataDirectory creates the shared application data root with private permissions.
func ensureDataDirectory(path string) error {
	if path == "" {
		return errors.New("application data directory is required")
	}

	return ensureDir(path)
}

// ensureDir creates a data directory and all missing parents.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0700)
}

// formatBytes formats an integer byte count with a compact binary unit.
func formatBytes(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}

	div, exp := uint64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

// formatCacheLimit formats the positive signed byte limit accepted by SQLite.
func formatCacheLimit(value int64) string {
	if value <= 0 {
		return "0 B"
	}

	for _, unit := range []struct {
		size   int64
		suffix string
	}{{1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if value >= unit.size {
			if value%unit.size == 0 {
				return fmt.Sprintf("%d %s", value/unit.size, unit.suffix)
			}

			return fmt.Sprintf("%.1f %s", float64(value)/float64(unit.size), unit.suffix)
		}
	}

	return fmt.Sprintf("%d B", value)
}
