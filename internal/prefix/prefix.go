// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package prefix extracts digest prefixes used to partition index entries.
package prefix

import (
	"encoding/binary"
	"fmt"
)

// Extract returns the first bits digest bits as an unsigned integer.
func Extract(digest []byte, bits uint8) (uint32, error) {
	if bits == 0 || bits > 32 || len(digest) < int((bits+7)/8) {
		return 0, fmt.Errorf("prefix width %d for %d-byte digest: %w", bits, len(digest), ErrInvalidPrefixWidth)
	}

	var first [4]byte
	copy(first[:], digest)

	// Prefixes are read most-significant-bit first, so one big-endian load and shift replaces per-bit work.
	return binary.BigEndian.Uint32(first[:]) >> (32 - bits), nil
}
