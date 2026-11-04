// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package api defines the cache contract shared by the resolver and cache adapters.
package api

import "context"

// Kind identifies which raw digest is used for a cache lookup.
type Kind uint8

const (
	// DayZ selects the SHA-256 identifier digest.
	DayZ Kind = 1
	// Battleye selects the BattlEye identifier digest.
	Battleye Kind = 2
)

// Cache is the positive identity cache contract used by index resolution.
type Cache interface {
	// Lookup returns candidate IDs matching one raw digest.
	Lookup(context.Context, Kind, []byte) ([]uint64, error)
	// PutMatch records an ID verified by a complete index scan.
	PutMatch(context.Context, Kind, uint64) error
}
