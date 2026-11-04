// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package steamid

import "errors"

// Sentinel errors identify invalid SteamID64 values and ranges.
var (
	// ErrSteamIDOutOfRange means a SteamID64 lies outside the protocol's representable interval.
	ErrSteamIDOutOfRange = errors.New("SteamID64 is outside protocol range")
	// ErrRangeOutOfBounds means a range endpoint lies outside the protocol's representable interval.
	ErrRangeOutOfBounds = errors.New("SteamID64 range is outside protocol bounds")
	// ErrReversedRange means the range minimum is greater than its maximum.
	ErrReversedRange = errors.New("minimum SteamID64 exceeds maximum")
)
