// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package steamid

import (
	"errors"
	"testing"
)

// TestRangeBoundaries checks conversions at protocol limits and inclusive range counts.
func TestRangeBoundaries(t *testing.T) {
	for _, id := range []uint64{ProtocolMinSteamID, ProtocolMaxSteamID} {
		account, err := AccountID(id)
		if err != nil || SteamID64(account) != id {
			t.Fatalf("round trip %d: account=%d err=%v", id, account, err)
		}
	}
	if _, err := AccountID(ProtocolMinSteamID - 1); !errors.Is(err, ErrSteamIDOutOfRange) {
		t.Fatalf("below-range error = %v, want ErrSteamIDOutOfRange", err)
	}
	if err := (Range{Min: SteamIDBase + 2, Max: SteamIDBase + 1}).Validate(); !errors.Is(err, ErrReversedRange) {
		t.Fatalf("reversed-range error = %v, want ErrReversedRange", err)
	}
	if err := (Range{Min: ProtocolMinSteamID - 1, Max: ProtocolMinSteamID}).Validate(); !errors.Is(err, ErrRangeOutOfBounds) {
		t.Fatalf("out-of-bounds error = %v, want ErrRangeOutOfBounds", err)
	}
	if err := (Range{Min: ProtocolMaxSteamID + 1, Max: ProtocolMaxSteamID}).Validate(); !errors.Is(err, ErrRangeOutOfBounds) {
		t.Fatalf("upper out-of-bounds error = %v, want ErrRangeOutOfBounds", err)
	}

	n, err := (Range{Min: SteamIDBase, Max: SteamIDBase + 9}).Count()
	if err != nil || n != 10 {
		t.Fatalf("Count() = %d, %v", n, err)
	}
}
