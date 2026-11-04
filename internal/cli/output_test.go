// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/woozymasta/inverz"
	"github.com/woozymasta/inverz/internal/steamid"
)

// TestPrintBuildSummaryUsesPlainKeyValueLines rejects column padding and trailing spaces.
func TestPrintBuildSummaryUsesPlainKeyValueLines(t *testing.T) {
	var output bytes.Buffer
	namespace := &GenerateNamespace{
		MinSteamID: steamid.SteamIDBase,
		MaxSteamID: steamid.SteamIDBase + (1 << 31) - 1,
	}
	profile := inverz.Profile{Version: 1, PrefixBits: 18, RiceK: 17, SegmentBits: 28}
	result := inverz.BuildResult{Entries: 1 << 31, SizeBytes: 5_262_407_656}

	err := printBuildSummary(&output, inverz.AlgorithmDayZ, profile, namespace, result,
		31_198*time.Millisecond, 8, 68_800_000)
	if err != nil {
		t.Fatal(err)
	}

	const want = "generated: dayz v1-p18-k17-s28\n" +
		"time: 31.198s\n" +
		"avg rate: 68.8 MIDs/s\n" +
		"segments: 8/8\n" +
		"entries: 2147483648\n" +
		"range: 76561197960265728..76561200107749375\n" +
		"size: 4.9 GiB\n"
	if got := output.String(); got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

// TestWriteKeyValuesDoesNotAlignLabels keeps every report line in the same format.
func TestWriteKeyValuesDoesNotAlignLabels(t *testing.T) {
	var output bytes.Buffer
	if err := writeKeyValues(&output, [][2]string{{"a", "1"}, {"longer", "2"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "a: 1\nlonger: 2\n"; got != want {
		t.Fatalf("key/value output = %q, want %q", got, want)
	}
}

// TestPrintEstimateUsesPlainKeyValueLines formats estimates without aligned columns.
func TestPrintEstimateUsesPlainKeyValueLines(t *testing.T) {
	var output bytes.Buffer
	profile := inverz.Profile{Version: 1, PrefixBits: 4, RiceK: 3, SegmentBits: 4}
	estimate := inverz.EstimateResult{
		Range:              inverz.SteamIDRange{Min: 1, Max: 2},
		Entries:            2,
		Segments:           1,
		ExpectedCandidates: 0.5,
		ExpectedBytes:      123,
		ExpectedRAMBytes:   456,
		PeakRAMBytes:       789,
		Publishable:        true,
	}
	if err := printEstimate(&output, inverz.AlgorithmDayZ, profile, estimate); err != nil {
		t.Fatal(err)
	}

	const want = "algorithm: dayz\n" +
		"profile: v1-p4-k3-s4\n" +
		"SteamID range: 1..2\n" +
		"entry count: 2\n" +
		"segment count: 1\n" +
		"expected candidates: 0.50\n" +
		"estimated bytes: 123\n" +
		"expected RAM: 456\n" +
		"peak RAM: 789\n" +
		"public full segments: true\n"
	if got := output.String(); got != want {
		t.Fatalf("estimate = %q, want %q", got, want)
	}
}

// TestPrintInfoUsesKeyValueLines formats remote and segment metadata consistently.
func TestPrintInfoUsesKeyValueLines(t *testing.T) {
	var output bytes.Buffer
	info := inverz.Info{
		Algorithm:          "dayz",
		Profile:            inverz.Profile{Version: 1, PrefixBits: 4, RiceK: 3, SegmentBits: 4},
		EntryCount:         "16",
		IndexFormatVersion: 1,
		SegmentCount:       2,
		SizeBytes:          1024,
		Remote:             &inverz.RemoteInfo{Reference: "example/repo", ManifestDigest: "sha256:test"},
		Segments: []inverz.SegmentInfo{
			{File: "00000000-0000000f.ivz", SHA256: "sha256:segment", SizeBytes: 512},
			{File: "00000010-0000001f.ivz", SHA256: "sha256:next", SizeBytes: 512, Index: 1, MinAccountID: 16, MaxAccountID: 31},
		},
	}
	if err := printInfo(&output, info); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{
		"algorithm: dayz\n",
		"entry_count: 16\n",
		"remote_reference: example/repo\n",
		"remote_manifest_digest: sha256:test\n",
		"segment: 00000000-0000000f.ivz\n",
		"sha256: sha256:segment\n",
		"size: 512\n",
		"index: 0\n",
		"min_account_id: 0\n",
		"max_account_id: 0\n",
		"max_account_id: 0\n\nsegment: 00000010-0000001f.ivz\n",
		"sha256: sha256:next\n",
		"min_account_id: 16\n",
		"max_account_id: 31\n",
	} {
		if !strings.Contains(output.String(), line) {
			t.Errorf("info output %q does not contain %q", output.String(), line)
		}
	}
	if strings.Contains(output.String(), "remote: {") || strings.Contains(output.String(), `[{"file"`) {
		t.Fatalf("info output contains a Go struct dump or JSON segment array: %q", output.String())
	}
}

// TestFormatIDRateUsesAccountIDUnits checks the final generation summary's throughput unit.
func TestFormatIDRateUsesAccountIDUnits(t *testing.T) {
	if got, want := formatIDRate(47_200_000), "47.2 MIDs/s"; got != want {
		t.Fatalf("formatIDRate() = %q, want %q", got, want)
	}
}
