// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// TestValidateProfileUsesGOMAXPROCS checks that the default follows the runtime CPU budget.
func TestValidateProfileUsesGOMAXPROCS(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)

	config := Config{Range: steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase}}
	if err := config.ValidateProfile(); err != nil {
		t.Fatal(err)
	}

	if config.Jobs != 4 {
		t.Fatalf("default jobs = %d, want 4", config.Jobs)
	}
}

// TestResolveJobsBudgetAndClamp checks stage allocation and the runtime limit.
func TestResolveJobsBudgetAndClamp(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)

	for _, test := range []struct {
		requested int
		jobs      int
		hash      int
		encode    int
	}{
		{requested: 0, jobs: 4, hash: 2, encode: 2},
		{requested: 1, jobs: 1, hash: 1, encode: 1},
		{requested: 2, jobs: 2, hash: 1, encode: 1},
		{requested: 3, jobs: 3, hash: 1, encode: 2},
		{requested: 8, jobs: 4, hash: 2, encode: 2},
		{requested: 128, jobs: 4, hash: 2, encode: 2},
	} {
		jobs, hash, encode, err := resolveJobs(test.requested)
		if err != nil {
			t.Fatalf("resolveJobs(%d): %v", test.requested, err)
		}
		if jobs != test.jobs || hash != test.hash || encode != test.encode {
			t.Errorf("resolveJobs(%d) = %d/%d/%d, want %d/%d/%d",
				test.requested, jobs, hash, encode, test.jobs, test.hash, test.encode)
		}
	}

	if _, _, _, err := resolveJobs(-1); !errors.Is(err, ErrInvalidJobs) {
		t.Fatalf("negative jobs error = %v, want %v", err, ErrInvalidJobs)
	}
}

// TestDeterministicBuildAcrossJobs verifies hashes and manifest bytes for each CPU budget.
func TestDeterministicBuildAcrossJobs(t *testing.T) {
	dir := t.TempDir()
	base := Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 999},
		PrefixBits:  4,
		RiceK:       3,
		RiceKSet:    true,
		SegmentBits: 8,
		Jobs:        1,
	}
	var wantManifest []byte
	var wantHashes map[string]string
	for _, jobs := range []int{1, 2, 3, 4, 8, 16, 32, 128} {
		output := filepath.Join(dir, strconv.Itoa(jobs))
		base.Output = output
		base.Jobs = jobs
		if err := Build(base); err != nil {
			t.Fatalf("build with jobs=%d: %v", jobs, err)
		}

		manifestBytes, err := os.ReadFile(filepath.Join(output, indexset.ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		if wantManifest == nil {
			wantManifest = manifestBytes
		} else if !bytes.Equal(manifestBytes, wantManifest) {
			t.Fatalf("release manifest differs with jobs=%d", jobs)
		}

		set, err := indexset.Open(output)
		if err != nil {
			t.Fatal(err)
		}
		if err := set.Verify(); err != nil {
			_ = set.Close()
			t.Fatal(err)
		}

		hashes := make(map[string]string, len(set.Manifest.Segments))
		for _, record := range set.Manifest.Segments {
			data, err := os.ReadFile(filepath.Join(output, "segments", record.File))
			if err != nil {
				_ = set.Close()
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			hashes[record.File] = hex.EncodeToString(sum[:])
		}
		if err := set.Close(); err != nil {
			t.Fatal(err)
		}

		if wantHashes == nil {
			wantHashes = hashes
			if got := hashes["00000000-000000ff.ivz"]; got != "92ce5a6dd805457294e0fd9379a8ac9260aa53f725ab52d9b927636eec1becbe" {
				t.Fatalf("segment SHA-256 = %s, want the v1 legacy bytes hash", got)
			}
			continue
		}

		if !equalStringMaps(hashes, wantHashes) {
			t.Fatalf("segment SHA-256 differs with jobs=%d", jobs)
		}
	}
}

// equalStringMaps compares expected segment digest sets without depending on map order.
func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}

	for key, value := range left {
		if right[key] != value {
			return false
		}
	}

	return true
}

// TestBuildRejectsCoverageRegression preserves coverage of an existing release.
func TestBuildRejectsCoverageRegression(t *testing.T) {
	output := filepath.Join(t.TempDir(), "index")
	config := Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      output,
	}
	if err := Build(config); err != nil {
		t.Fatal(err)
	}

	config.Range = steamid.Range{Min: steamid.SteamIDBase + 2, Max: steamid.SteamIDBase + 13}
	if err := Build(config); !errors.Is(err, ErrCoverageRegression) {
		t.Fatalf("narrowing build error = %v, want %v", err, ErrCoverageRegression)
	}
}

// TestForceRebuildsHealthyCorruptAndMissingSegments checks canonical force repair behavior.
func TestForceRebuildsHealthyCorruptAndMissingSegments(t *testing.T) {
	for _, mode := range []string{"healthy", "corrupt", "missing"} {
		t.Run(mode, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "index")
			config := Config{
				Algorithm:   segment.DayZ,
				Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
				PrefixBits:  2,
				RiceK:       1,
				RiceKSet:    true,
				SegmentBits: 4,
				Jobs:        1,
				Output:      output,
			}
			if err := Build(config); err != nil {
				t.Fatal(err)
			}

			set, err := indexset.Open(output)
			if err != nil {
				t.Fatal(err)
			}

			record := set.Manifest.Segments[0]
			if err := set.Close(); err != nil {
				t.Fatal(err)
			}

			segmentPath := filepath.Join(output, "segments", indexset.SegmentFilename(0, 15))
			var original []byte
			if mode == "corrupt" {
				original, err = os.ReadFile(segmentPath)
				if err != nil {
					t.Fatal(err)
				}

				original[len(original)-1] ^= 0xff
				// #nosec G703 - segmentPath is derived from the test's isolated t.TempDir output.
				if err := os.WriteFile(segmentPath, original, 0600); err != nil {
					t.Fatal(err)
				}
			}

			if mode == "missing" {
				if err := os.Remove(segmentPath); err != nil {
					t.Fatal(err)
				}
			}

			config.Force = true
			if err := Build(config); err != nil {
				t.Fatalf("force rebuild: %v", err)
			}

			set, err = indexset.Open(output)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := set.Close(); err != nil {
					t.Errorf("close verified index: %v", err)
				}
			})
			if err := set.Verify(); err != nil {
				t.Fatalf("force result verification: %v", err)
			}
			if set.Manifest.Segments[0].SHA256 != record.SHA256 {
				t.Fatalf("segment digest = %s, want canonical %s", set.Manifest.Segments[0].SHA256, record.SHA256)
			}
		})
	}
}

// TestForceRejectsNoncanonicalRebuild prevents force from hiding nondeterministic output.
func TestForceRejectsNoncanonicalRebuild(t *testing.T) {
	config := Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      filepath.Join(t.TempDir(), "index"),
	}
	if err := Build(config); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(config.Output, indexset.ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := indexset.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	manifest.Segments[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	data, err = manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G703 - manifestPath is inside this test's isolated t.TempDir output.
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	config.Force = true
	err = build(config, nil)
	if !errors.Is(err, ErrImmutableSegmentMismatch) {
		t.Fatalf("noncanonical rebuild error = %v, want %v", err, ErrImmutableSegmentMismatch)
	}
}

// TestDayZDecimalCursorMatchesDigest checks a million sequential IDs against the public digest path.
func TestDayZDecimalCursorMatchesDigest(t *testing.T) {
	const count = 1_000_000
	start := steamid.SteamIDBase + 99_999_997
	var cursor dayZDecimalCursor
	cursor.reset(start)

	for offset := range count {
		id := start + uint64(offset)
		if got, want := cursor.nextDigest(), digest.DayZ(id); got != want {
			t.Fatalf("cursor digest mismatch at ID %d: got %x, want %x", id, got, want)
		}
	}
}

// TestStandaloneSegmentMatchesLargerSet verifies globally aligned segments are reusable.
func TestStandaloneSegmentMatchesLargerSet(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".generate-standalone-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	base := steamid.SteamIDBase
	standalonePath := filepath.Join(dir, "standalone")
	largerPath := filepath.Join(dir, "larger")
	config := Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base + 32, Max: base + 63},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 5,
		Jobs:        1,
		Output:      standalonePath,
	}
	if err := Build(config); err != nil {
		t.Fatal(err)
	}

	config.Range = steamid.Range{Min: base, Max: base + 95}
	config.Jobs = 3
	config.Output = largerPath
	if err := Build(config); err != nil {
		t.Fatal(err)
	}
	standalone, err := indexset.Open(standalonePath)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = standalone.Close() }()

	larger, err := indexset.Open(largerPath)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = larger.Close() }()

	if len(standalone.Manifest.Segments) != 1 || len(larger.Manifest.Segments) != 3 {
		t.Fatalf("segment counts = %d and %d, want 1 and 3", len(standalone.Manifest.Segments), len(larger.Manifest.Segments))
	}

	standaloneRecord := standalone.Manifest.Segments[0]
	largerRecord := larger.Manifest.Segments[1]
	if standaloneRecord != largerRecord {
		t.Fatalf("standalone segment record %#v differs from larger set record %#v", standaloneRecord, largerRecord)
	}

	standaloneBytes, err := os.ReadFile(filepath.Join(standalonePath, "segments", standaloneRecord.File))
	if err != nil {
		t.Fatal(err)
	}

	largerBytes, err := os.ReadFile(filepath.Join(largerPath, "segments", largerRecord.File))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(standaloneBytes, largerBytes) {
		t.Fatal("standalone segment bytes differ from the same segment in a larger set")
	}
}

// TestBuildExtensionReusesSegments verifies extending a release preserves existing segments.
func TestBuildExtensionReusesSegments(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".generate-extension-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	output := filepath.Join(dir, "release")
	config := Config{
		Algorithm:   segment.Battleye,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 511},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 8,
		Output:      output,
		Jobs:        2}
	if err := Build(config); err != nil {
		t.Fatal(err)
	}

	first, err := indexset.Open(output)
	if err != nil {
		t.Fatal(err)
	}

	firstDigests := []string{first.Manifest.Segments[0].SHA256, first.Manifest.Segments[1].SHA256}
	_ = first.Close()
	config.Range.Max = steamid.SteamIDBase + 767
	if err := Build(config); err != nil {
		t.Fatal(err)
	}

	second, err := indexset.Open(output)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = second.Close() }()

	if len(second.Manifest.Segments) != 3 {
		t.Fatalf("extension has %d segments, want 3", len(second.Manifest.Segments))
	}
	for i, digest := range firstDigests {
		if second.Manifest.Segments[i].SHA256 != digest {
			t.Fatalf("segment %d changed across extension", i)
		}
	}
}

// TestFailedExtensionLeavesActiveRelease verifies failed activation preserves the active manifest.
func TestFailedExtensionLeavesActiveRelease(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".generate-activation-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	output := filepath.Join(dir, "release")
	config := Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 255},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 8,
		Output:      output,
		Jobs:        2,
	}
	if err := Build(config); err != nil {
		t.Fatal(err)
	}

	activePath := filepath.Join(output, indexset.ManifestName)
	activeBefore, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}

	config.Range.Max = steamid.SteamIDBase + 511
	err = build(config, &buildHooks{beforeActivate: func() error { return os.ErrPermission }})
	if err == nil {
		t.Fatal("expected injected activation failure")
	}

	activeAfter, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(activeBefore, activeAfter) {
		t.Fatal("failed extension changed the active release manifest")
	}

	set, err := indexset.Open(output)
	if err != nil {
		t.Fatalf("previous release is no longer usable: %v", err)
	}
	_ = set.Close()
}

// TestBuildAllowsProtocolRangeAboveDefault verifies the full protocol range remains buildable.
func TestBuildAllowsProtocolRangeAboveDefault(t *testing.T) {
	minSteamID := steamid.SteamIDBase + (1 << 31)
	root, err := os.MkdirTemp(".", ".generate-above-default-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(root) })
	output := filepath.Join(root, "above-default")
	result, err := BuildWithResult(Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: minSteamID, Max: minSteamID + 7},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      output,
	})
	if err != nil {
		t.Fatalf("build above default range: %v", err)
	}

	if result.MinSteamID != minSteamID ||
		result.MaxSteamID != minSteamID+7 ||
		result.EntryCount != 8 ||
		result.SegmentCount != 1 {
		t.Fatalf("unexpected build result: %#v", result)
	}

	set, err := indexset.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()
	if set.Manifest.MaxSteamID != strconv.FormatUint(minSteamID+7, 10) {
		t.Fatalf("manifest maximum = %s, want %d", set.Manifest.MaxSteamID, minSteamID+7)
	}
}

// TestConfigValidationReturnsNamedErrors checks that invalid options are classifiable with errors.Is.
func TestConfigValidationReturnsNamedErrors(t *testing.T) {
	base := Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 1},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 1,
		Output:      "release",
		Jobs:        1,
	}
	for _, tc := range []struct {
		name  string        // Invalid setting under test.
		setup func(*Config) // Applies the invalid setting to a valid baseline config.
		want  error         // Sentinel expected from validation.
	}{
		{
			name: "prefix width",
			setup: func(c *Config) {
				c.PrefixBits = 21
			},
			want: ErrInvalidPrefixBits,
		},
		{
			name: "rice width",
			setup: func(c *Config) {
				c.RiceK = 32
			},
			want: ErrInvalidRiceK,
		},
		{
			name: "segment width",
			setup: func(c *Config) {
				c.SegmentBits = 33
			},
			want: ErrInvalidSegmentBits,
		},
		{
			name: "worker count",
			setup: func(c *Config) {
				c.Jobs = -1
			},
			want: ErrInvalidJobs,
		},
		{
			name: "algorithm",
			setup: func(c *Config) {
				c.Algorithm = segment.Algorithm(255)
			},
			want: ErrInvalidAlgorithm,
		},
		{
			name: "output",
			setup: func(c *Config) {
				c.Output = ""
			},
			want: ErrOutputRequired,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := base
			tc.setup(&config)
			if err := config.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}
}
