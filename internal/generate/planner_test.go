// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"errors"
	"testing"

	"github.com/woozymasta/inverz/internal/steamid"
)

// TestPlanSegmentsUsesGlobalAccountBoundaries checks ranges split on aligned boundaries.
func TestPlanSegmentsUsesGlobalAccountBoundaries(t *testing.T) {
	base := steamid.SteamIDBase
	for _, bits := range []uint8{7, 8, 9, 27, 28, 29} {
		size := uint32(1) << bits
		rangeValue := steamid.Range{Min: base + uint64(size) - 2, Max: base + uint64(size) + 2}
		got, err := PlanSegments(rangeValue, bits)
		if err != nil {
			t.Fatalf("bits %d: %v", bits, err)
		}

		if len(got) != 2 ||
			got[0].MinAccountID != size-2 ||
			got[0].MaxAccountID != size-1 ||
			got[1].MinAccountID != size ||
			got[1].MaxAccountID != size+2 {
			t.Fatalf("bits %d: unexpected plan %#v", bits, got)
		}
	}
}

// TestPlanDefaultProductionRangeHasEightFullSegments checks the default range's segment plan.
func TestPlanDefaultProductionRangeHasEightFullSegments(t *testing.T) {
	defaultMax := steamid.SteamIDBase + (1<<31 - 1)
	segments, err := PlanSegments(steamid.Range{Min: steamid.SteamIDBase, Max: defaultMax}, 28)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 8 {
		t.Fatalf("default production range has %d segments, want 8", len(segments))
	}

	segmentSize := uint64(1) << 28
	for i, got := range segments {
		expectedMin := uint32(uint64(i) * segmentSize)
		expectedMax := uint32(uint64(i+1)*segmentSize - 1)
		if got.Index != uint32(i) || got.MinAccountID != expectedMin || got.MaxAccountID != expectedMax {
			t.Fatalf("segment %d = %#v, want index=%d range=%08x..%08x", i, got, i, expectedMin, expectedMax)
		}
	}
}

// TestPlanSegmentsPartialAndAlignedRanges checks clipping at the requested outer bounds.
func TestPlanSegmentsPartialAndAlignedRanges(t *testing.T) {
	base := steamid.SteamIDBase
	for _, tc := range []struct {
		name string // Scenario label.
		min  uint64 // Inclusive SteamID64 lower bound.
		max  uint64 // Inclusive SteamID64 upper bound.
		want int    // Expected number of segments.
	}{
		{name: "partial", min: base + 3, max: base + 20, want: 2},
		{name: "aligned", min: base + 16, max: base + 31, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PlanSegments(steamid.Range{Min: tc.min, Max: tc.max}, 4)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d segments, want %d", len(got), tc.want)
			}
		})
	}
}

// TestPlanSegmentsRejectsUnsupportedRangeAndCount checks invalid widths, counts, and IDs.
func TestPlanSegmentsRejectsUnsupportedRangeAndCount(t *testing.T) {
	base := steamid.SteamIDBase
	if _, err := PlanSegments(steamid.Range{Min: base, Max: base + 1}, 0); !errors.Is(err, ErrInvalidSegmentBits) {
		t.Fatal("accepted zero segment bits")
	}
	if _, err := PlanSegments(steamid.Range{Min: base, Max: base + 513}, 1); !errors.Is(err, ErrSegmentCountLimit) {
		t.Fatal("accepted a plan exceeding the segment limit")
	}
	if _, err := PlanSegments(steamid.Range{Min: base, Max: steamid.ProtocolMaxSteamID + 1}, 28); err == nil {
		t.Fatal("accepted a range above the SteamID protocol ceiling")
	}
}
