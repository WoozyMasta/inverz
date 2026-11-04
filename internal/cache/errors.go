// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cache

import "errors"

// Sentinel errors identify invalid cache inputs and persisted cache state.
var (
	// ErrInvalidMaxBytes reports a non-positive cache size limit.
	ErrInvalidMaxBytes = errors.New("cache maximum size must be positive")
	// ErrInvalidDigestLength means a digest does not match its algorithm's fixed size.
	ErrInvalidDigestLength = errors.New("invalid cache digest length")
	// ErrInvalidKind means a cache operation selected an unsupported digest algorithm.
	ErrInvalidKind = errors.New("invalid cache digest kind")
	// ErrInvalidSteamID means a SteamID64 stored in or supplied to the cache is invalid.
	ErrInvalidSteamID = errors.New("invalid cache SteamID64")
	// ErrSQLiteIntegerRange means a SteamID64 cannot be represented by SQLite's signed INTEGER type.
	ErrSQLiteIntegerRange = errors.New("SteamID64 exceeds SQLite INTEGER range")
	// ErrUnsupportedSchemaVersion means the cache uses a schema version this package cannot read.
	ErrUnsupportedSchemaVersion = errors.New("unsupported SQLite cache schema version")
)
