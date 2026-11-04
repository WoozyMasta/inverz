// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package digest

import "errors"

// Sentinel errors identify malformed external identifier encodings.
var (
	// ErrInvalidDayZGUID means a value is not a valid padded DayZ Base64URL digest.
	ErrInvalidDayZGUID = errors.New("invalid DayZ GUID")
	// ErrInvalidBattleyeGUID means a value is not a valid BattlEye hexadecimal digest.
	ErrInvalidBattleyeGUID = errors.New("invalid BattlEye GUID")
)
