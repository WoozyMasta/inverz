// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package releaselock

import "errors"

// Sentinel errors classify process-shared release mutation failures.
var (
	// ErrReleasePathRequired reports that no release path was supplied to Acquire.
	ErrReleasePathRequired = errors.New("release path is required")
	// ErrReleaseBusy reports that another process is mutating the same release.
	ErrReleaseBusy = errors.New("release is being modified by another process")
)
