// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package cache defines positive identity cache contracts and implementations.
package cache

import "github.com/woozymasta/inverz/internal/cache/api"

const (
	// DayZ selects the SHA-256 identifier digest.
	DayZ = api.DayZ
	// Battleye selects the BattlEye identifier digest.
	Battleye = api.Battleye
)

// Kind identifies which raw digest is used for a cache lookup.
type Kind = api.Kind

// Cache is the positive identity cache contract.
type Cache interface {
	api.Cache
	// Close releases the cache resources.
	Close() error
}
