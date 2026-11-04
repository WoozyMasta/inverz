// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package prefix

import (
	"errors"
	"testing"
)

// TestExtract checks MSB-first extraction and validation at supported boundaries.
func TestExtract(t *testing.T) {
	d := []byte{0xAB, 0xCD, 0xEF, 0x12}
	for _, tc := range []struct {
		bits uint8  // Number of most-significant bits to extract.
		want uint32 // Expected prefix value.
	}{{1, 1}, {8, 0xAB}, {16, 0xABCD}, {18, 0x2AF37}, {24, 0xABCDEF}, {32, 0xABCDEF12}} {
		got, err := Extract(d, tc.bits)
		if err != nil || got != tc.want {
			t.Fatalf("Extract(%d)=%x, %v; want %x", tc.bits, got, err, tc.want)
		}
	}

	for _, tc := range []struct {
		name   string // Failure case label.
		digest []byte // Digest bytes supplied to Extract.
		bits   uint8  // Invalid prefix width for the case.
	}{
		{name: "zero width", digest: d, bits: 0},
		{name: "over-wide", digest: d, bits: 33},
		{name: "short digest", digest: d[:1], bits: 9},
		{name: "empty digest", digest: nil, bits: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Extract(tc.digest, tc.bits); !errors.Is(err, ErrInvalidPrefixWidth) {
				t.Fatalf("error = %v, want ErrInvalidPrefixWidth", err)
			}
		})
	}
}
