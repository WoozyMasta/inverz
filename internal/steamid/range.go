// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package steamid validates and converts protocol SteamID64 values.
package steamid

import "fmt"

const (
	// SteamIDBase is the SteamID64 value corresponding to AccountID zero.
	SteamIDBase uint64 = 76561197960265728
	// ProtocolMinSteamID is the lowest SteamID64 accepted by the protocol.
	ProtocolMinSteamID uint64 = SteamIDBase
	// ProtocolMaxSteamID is the highest SteamID64 representable by the protocol.
	ProtocolMaxSteamID uint64 = 76561202255233023
)

// Range describes an inclusive SteamID64 range.
type Range struct {
	Min uint64 // First SteamID64 in the range.
	Max uint64 // Last SteamID64 in the range.
}

// AccountID converts a supported SteamID64 to its 32-bit account identifier.
func AccountID(id uint64) (uint32, error) {
	if id < ProtocolMinSteamID || id > ProtocolMaxSteamID {
		return 0, fmt.Errorf("SteamID64 %d: %w", id, ErrSteamIDOutOfRange)
	}

	// The protocol range starts at AccountID zero, so subtraction gives the exact account value.
	return uint32(id - SteamIDBase), nil
}

// SteamID64 converts an AccountID to SteamID64.
func SteamID64(id uint32) uint64 { return SteamIDBase + uint64(id) }

// Validate checks that the inclusive range is within protocol bounds.
func (r Range) Validate() error {
	// Check protocol membership first so invalid endpoints have a stable error category.
	if r.Min < ProtocolMinSteamID ||
		r.Min > ProtocolMaxSteamID ||
		r.Max < ProtocolMinSteamID ||
		r.Max > ProtocolMaxSteamID {
		return fmt.Errorf("SteamID64 range [%d, %d]: %w", r.Min, r.Max, ErrRangeOutOfBounds)
	}

	if r.Min > r.Max {
		return fmt.Errorf("minimum SteamID64 %d exceeds maximum %d: %w", r.Min, r.Max, ErrReversedRange)
	}

	return nil
}

// Count returns the inclusive number of entries after validation.
func (r Range) Count() (uint64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}

	return r.Max - r.Min + 1, nil
}
