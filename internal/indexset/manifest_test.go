// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package indexset

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/steamid"
)

// TestManifestCanonicalRoundTrip checks stable JSON encoding and parse equivalence.
func TestManifestCanonicalRoundTrip(t *testing.T) {
	manifest := validManifest()
	first, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	second, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("manifest encoding is not deterministic")
	}

	golden := `{
  "algorithm": "dayz",
  "min_steamid": "76561197960265728",
  "max_steamid": "76561197960266239",
  "entry_count": "512",
  "segments": [
    {
      "file": "00000000-000000ff.ivz",
      "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "size": 193,
      "index": 0,
      "min_account_id": 0,
      "max_account_id": 255
    },
    {
      "file": "00000100-000001ff.ivz",
      "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "size": 194,
      "index": 1,
      "min_account_id": 256,
      "max_account_id": 511
    }
  ],
  "schema_version": 1,
  "index_format_version": 1,
  "prefix_bits": 4,
  "rice_k": 3,
  "segment_bits": 8
}
`
	if !bytes.Equal(first, []byte(golden)) {
		t.Fatalf("manifest bytes differ from canonical golden:\n%s", first)
	}

	got, err := Parse(first)
	if err != nil {
		t.Fatal(err)
	}
	if got.EntryCount != manifest.EntryCount || len(got.Segments) != len(manifest.Segments) {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

// TestManifestRejectsUnsafeAndInvalidCoverage checks path, ordering, digest, and range validation.
func TestManifestRejectsUnsafeAndInvalidCoverage(t *testing.T) {
	tests := []struct {
		name   string          // Invalid manifest condition.
		mutate func(*Manifest) // Applies the condition to a valid manifest.
		want   error           // Expected sentinel, or nil when the lower-level range validator reports it.
	}{
		{
			name:   "unsafe filename",
			mutate: func(m *Manifest) { m.Segments[0].File = "../segment.ivz" },
			want:   ErrUnsafeSegmentName,
		},
		{
			name:   "gap",
			mutate: func(m *Manifest) { m.Segments[1].MinAccountID++ },
			want:   ErrSegmentOrder,
		},
		{
			name:   "overlap",
			mutate: func(m *Manifest) { m.Segments[1].MinAccountID-- },
			want:   ErrSegmentCrossesBoundary,
		},
		{
			name:   "digest",
			mutate: func(m *Manifest) { m.Segments[0].SHA256 = "xyz" },
			want:   ErrInvalidSegmentSHA256,
		},
		{
			name:   "non-decimal SteamID",
			mutate: func(m *Manifest) { m.MinSteamID = "not-a-number" },
			want:   ErrSteamIDDecimal,
		},
		{name: "range", mutate: func(m *Manifest) { m.MaxSteamID = "76561200107749376" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			manifest := validManifest()
			tc.mutate(&manifest)
			err := manifest.Validate()
			if err == nil {
				t.Fatal("invalid manifest accepted")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestManifestRejectsUnknownAndTrailingFields checks strict JSON and manifest size limits.
func TestManifestRejectsUnknownAndTrailingFields(t *testing.T) {
	data, err := validManifest().Marshal()
	if err != nil {
		t.Fatal(err)
	}

	unknown := bytes.Replace(data, []byte(`"algorithm": "dayz"`), []byte(`"algorithm": "dayz", "unknown": true`), 1)
	if _, err := Parse(unknown); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Parse(append(data, []byte(`{}`)...)); !errors.Is(err, ErrManifestTrailingData) {
		t.Fatalf("trailing JSON error = %v, want ErrManifestTrailingData", err)
	}
	if _, err := Parse(make([]byte, limits.MaxManifestBytes+1)); !errors.Is(err, ErrManifestSize) {
		t.Fatalf("oversized manifest error = %v, want ErrManifestSize", err)
	}
}

// TestDefaultRangeMaxMatchesAccountBoundary checks the default range's inclusive endpoint.
func TestDefaultRangeMaxMatchesAccountBoundary(t *testing.T) {
	// defaultMaxAccountID is the largest account ID in the default production range.
	const defaultMaxAccountID = uint32(1<<31 - 1)
	if got := steamid.SteamID64(defaultMaxAccountID); got != steamid.SteamIDBase+(1<<31-1) {
		t.Fatalf("default range maximum = %d", got)
	}
}

// TestManifestAllowsProtocolRangeAboveDefault checks manifest validation above the default range.
func TestManifestAllowsProtocolRangeAboveDefault(t *testing.T) {
	// minAccount and maxAccount define one complete segment above the default production ceiling.
	const minAccount = uint32(1 << 31)
	const maxAccount = minAccount + 255
	manifest := Manifest{
		SchemaVersion:      1,
		Algorithm:          "dayz",
		IndexFormatVersion: 1,
		PrefixBits:         4,
		RiceK:              3,
		SegmentBits:        8,
		MinSteamID:         strconv.FormatUint(steamid.SteamID64(minAccount), 10),
		MaxSteamID:         strconv.FormatUint(steamid.SteamID64(maxAccount), 10),
		EntryCount:         "256",
		Segments: []SegmentRecord{{
			Index:        1 << 23,
			File:         SegmentFilename(minAccount, maxAccount),
			MinAccountID: minAccount,
			MaxAccountID: maxAccount,
			Size:         193,
			SHA256:       strings.Repeat("a", 64),
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("manifest above default range rejected: %v", err)
	}
}

// FuzzParseManifest checks arbitrary bounded JSON without panicking or reading oversized input.
func FuzzParseManifest(f *testing.F) {
	seed, err := validManifest().Marshal()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(seed)
	f.Add([]byte(`{"schema_version":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > limits.MaxManifestBytes+1 {
			t.Skip()
		}
		_, _ = Parse(data)
	})
}

// validManifest returns a small canonical two-segment release for parser tests.
func validManifest() Manifest {
	return Manifest{
		SchemaVersion:      1,
		Algorithm:          "dayz",
		IndexFormatVersion: 1,
		PrefixBits:         4,
		RiceK:              3,
		SegmentBits:        8,
		MinSteamID:         "76561197960265728",
		MaxSteamID:         "76561197960266239",
		EntryCount:         "512",
		Segments: []SegmentRecord{
			{
				Index:        0,
				File:         "00000000-000000ff.ivz",
				MinAccountID: 0,
				MaxAccountID: 255,
				Size:         193,
				SHA256:       strings.Repeat("a", 64),
			},
			{
				Index:        1,
				File:         "00000100-000001ff.ivz",
				MinAccountID: 256,
				MaxAccountID: 511,
				Size:         194,
				SHA256:       strings.Repeat("b", 64),
			},
		},
	}
}
