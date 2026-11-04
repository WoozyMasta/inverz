// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"math"
	"runtime"
	"testing"

	"github.com/woozymasta/inverz/internal/steamid"
)

// TestEstimateUsesProfileAndDoesNotRequireOutput checks profile defaults and the size estimate.
func TestEstimateUsesProfileAndDoesNotRequireOutput(t *testing.T) {
	for _, profile := range []struct {
		prefixBits uint8 // Digest prefix width for the estimate case.
		riceK      uint8 // Rice remainder width for the estimate case.
	}{{16, 15}, {18, 17}, {20, 19}} {
		config := Config{
			Range:       steamid.Range{Min: steamid.SteamIDBase + 3, Max: steamid.SteamIDBase + 102},
			PrefixBits:  profile.prefixBits,
			RiceK:       profile.riceK,
			RiceKSet:    true,
			SegmentBits: 8,
			Jobs:        2,
		}
		estimate, err := config.Estimate()
		if err != nil {
			t.Fatalf("estimate p%d/k%d: %v", profile.prefixBits, profile.riceK, err)
		}

		buckets := uint64(1) << profile.prefixBits
		emptyProbability := math.Pow(1-1/float64(buckets), float64(uint64(1)<<profile.riceK))
		bitsPerEntry := float64(profile.riceK) + 1 + emptyProbability/(1-emptyProbability)
		entries := float64(config.Range.Max - config.Range.Min + 1)
		fixedBytes := uint64(192) + (buckets+1)*4 + buckets*4
		wantBytes := fixedBytes + uint64(entries*bitsPerEntry/8)
		if estimate.ExpectedOutputBytes != wantBytes {
			t.Fatalf("p%d/k%d estimated bytes=%d, want %d",
				profile.prefixBits, profile.riceK, estimate.ExpectedOutputBytes, wantBytes)
		}

		segment := estimate.Segments[0]
		wantExpectedRAM := segment.RicePayloadBytes +
			segment.EncoderStateBytes +
			segment.HashBufferBytes +
			segment.DirectoryBytes +
			segment.PipelineOverheadBytes
		if segment.ExpectedRAMBytes != wantExpectedRAM {
			t.Fatalf("p%d expected RAM=%d, want component sum %d",
				profile.prefixBits, segment.ExpectedRAMBytes, wantExpectedRAM)
		}
		if segment.PeakRAMBytes < segment.ExpectedRAMBytes {
			t.Fatalf("p%d conservative RAM=%d is below expected %d",
				profile.prefixBits, segment.PeakRAMBytes, segment.ExpectedRAMBytes)
		}
		if segment.PeakRAMBytes < segment.ExpectedRAMBytes+segment.RicePayloadBytes {
			t.Fatalf("p%d conservative RAM=%d does not reserve a second payload copy",
				profile.prefixBits, segment.PeakRAMBytes)
		}
	}
}

// TestEstimateUsesHashWorkerBuffers checks buffer estimates follow the split job budget.
func TestEstimateUsesHashWorkerBuffers(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)

	const (
		entries = 1 << 20
		buckets = 1 << 18
		payload = 1 << 20
	)
	oneJob := estimateSegmentMemory(entries, buckets, payload, 1, 18)
	fourJobs := estimateSegmentMemory(entries, buckets, payload, 4, 18)
	if fourJobs.hashBuffers <= oneJob.hashBuffers {
		t.Fatalf("hash buffers with four jobs = %d, want more than one job %d",
			fourJobs.hashBuffers, oneJob.hashBuffers)
	}
	if fourJobs.encoderState != oneJob.encoderState || fourJobs.directory != oneJob.directory {
		t.Fatal("logical bucket state changed with the CPU job budget")
	}
}

// TestEstimateAllowsProtocolRangeAboveDefault verifies estimates cover high protocol IDs.
func TestEstimateAllowsProtocolRangeAboveDefault(t *testing.T) {
	config := Config{
		Range:       steamid.Range{Min: steamid.SteamIDBase + (1 << 31), Max: steamid.SteamIDBase + (1 << 31) + 7},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
	}
	if _, err := config.Estimate(); err != nil {
		t.Fatalf("estimate above default range: %v", err)
	}
}
