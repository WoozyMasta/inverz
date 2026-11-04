// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woozymasta/inverz/internal/generate"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/profile"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// TestActivateExtensionKeepsPreviousReleaseOnFailure preserves the active manifest until activation completes.
func TestActivateExtensionKeepsPreviousReleaseOnFailure(t *testing.T) {
	root := workspaceTempDir(t)
	sourceRoot := filepath.Join(workspaceTempDir(t), "source")
	firstSource := filepath.Join(sourceRoot, "first")
	secondSource := filepath.Join(sourceRoot, "second")
	base := steamid.SteamIDBase
	config := generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base, Max: base + 255},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 8,
		Jobs:        2,
		Output:      firstSource,
	}
	if err := generate.Build(config); err != nil {
		t.Fatal(err)
	}

	first, err := indexset.Open(firstSource)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(first.Manifest, stagedFiles(firstSource, first.Manifest)); err != nil {
		t.Fatal(err)
	}

	activePath, err := store.ReleasePath(first.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(activePath, indexset.ManifestName)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	config.Range.Max = base + 511
	config.Output = secondSource
	if err := generate.Build(config); err != nil {
		t.Fatal(err)
	}

	second, err := indexset.Open(secondSource)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = second.Close() }()
	newRecord := second.Manifest.Segments[1]
	stage := map[string]string{newRecord.File: filepath.Join(secondSource, "segments", newRecord.File)}
	err = store.activate(second.Manifest, stage, &activationHooks{beforeManifest: func() error { return os.ErrPermission }})
	if err == nil {
		t.Fatal("expected injected activation failure")
	}

	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed activation replaced the active manifest")
	}

	opened, err := store.Open("dayz", 3, 2, 8)
	if err != nil {
		t.Fatalf("previous release is unavailable after failed activation: %v", err)
	}

	defer func() { _ = opened.Close() }()
	if len(opened.Segments) != 1 {
		t.Fatalf("active set contains %d segments after failure, want 1", len(opened.Segments))
	}
	if err := store.Activate(second.Manifest, stagedFiles(secondSource, second.Manifest)); err != nil {
		t.Fatalf("activate valid extension: %v", err)
	}

	updated, err := store.Open("dayz", 3, 2, 8)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = updated.Close() }()
	if len(updated.Segments) != 2 {
		t.Fatalf("activated set contains %d segments, want 2", len(updated.Segments))
	}
}

// TestActivateRejectsChangedImmutableDigest prevents changing an existing segment's content identity.
func TestActivateRejectsChangedImmutableDigest(t *testing.T) {
	source := filepath.Join(workspaceTempDir(t), "source")
	config := generate.Config{
		Algorithm:   segment.Battleye,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      source,
	}
	if err := generate.Build(config); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(source)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = set.Close() }()
	store, err := New(workspaceTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(set.Manifest, stagedFiles(source, set.Manifest)); err != nil {
		t.Fatal(err)
	}

	changed := set.Manifest
	changed.Segments = append([]indexset.SegmentRecord(nil), set.Manifest.Segments...)
	changed.Segments[0].SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.Activate(changed, nil); err == nil {
		t.Fatal("accepted a changed digest for an immutable segment")
	}

	releasePath, err := store.ReleasePath(set.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	missingSegment := filepath.Join(releasePath, "segments", set.Manifest.Segments[0].File)
	if err := os.Remove(missingSegment); err != nil {
		t.Fatal(err)
	}
	if err := store.Activate(changed, stagedFiles(source, set.Manifest)); err == nil {
		t.Fatal("accepted a changed immutable digest when the active segment file was missing")
	}
}

// TestActivateRejectsCoverageRegression prevents installing a narrower release over a wider one.
func TestActivateRejectsCoverageRegression(t *testing.T) {
	root := workspaceTempDir(t)
	storeRoot := filepath.Join(root, "store")
	localStore, err := New(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	config := generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      filepath.Join(root, "wide"),
	}
	if err := generate.Build(config); err != nil {
		t.Fatal(err)
	}
	wide, err := indexset.Open(config.Output)
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.Activate(wide.Manifest, stagedFiles(config.Output, wide.Manifest)); err != nil {
		_ = wide.Close()
		t.Fatal(err)
	}
	if err := wide.Close(); err != nil {
		t.Fatal(err)
	}

	config.Range = steamid.Range{Min: steamid.SteamIDBase + 2, Max: steamid.SteamIDBase + 13}
	config.Output = filepath.Join(root, "narrow")
	if err := generate.Build(config); err != nil {
		t.Fatal(err)
	}
	narrow, err := indexset.Open(config.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = narrow.Close() }()
	if err := localStore.Activate(narrow.Manifest, stagedFiles(config.Output, narrow.Manifest)); !errors.Is(err, ErrCoverageRegression) {
		t.Fatalf("narrow activation error = %v, want %v", err, ErrCoverageRegression)
	}
}

// TestRemoteStateRejectsCredentialsAndNonCanonicalDigests rejects unsafe references and metadata.
func TestRemoteStateRejectsCredentialsAndNonCanonicalDigests(t *testing.T) {
	tests := []struct {
		name  string      // Case name shown by the test runner.
		state RemoteState // Invalid state to validate.
	}{
		{
			name: "userinfo",
			state: RemoteState{
				Reference:      "user:password@registry.example/repo:tag",
				ManifestDigest: "sha256:" + strings.Repeat("a", 64),
			},
		},
		{
			name: "uppercase digest",
			state: RemoteState{
				Reference:      "registry.example/repo:tag",
				ManifestDigest: "sha256:" + strings.Repeat("A", 64),
			},
		},
		{
			name: "invalid timestamp",
			state: RemoteState{
				Reference:      "registry.example/repo:tag",
				ManifestDigest: "sha256:" + strings.Repeat("a", 64),
				LastChecked:    "yesterday",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.state.Validate(); err == nil {
				t.Fatal("accepted invalid remote state")
			}
		})
	}
}

// TestReadRemoteStateClassifiesTruncatedDataAsRefreshable verifies corrupt cache metadata is recoverable.
func TestReadRemoteStateClassifiesTruncatedDataAsRefreshable(t *testing.T) {
	root := workspaceTempDir(t)
	localStore, err := New(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	config := generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      filepath.Join(root, "source"),
	}
	if err := generate.Build(config); err != nil {
		t.Fatal(err)
	}
	set, err := indexset.Open(config.Output)
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.Activate(set.Manifest, stagedFiles(config.Output, set.Manifest)); err != nil {
		_ = set.Close()
		t.Fatal(err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	releaseDir, err := localStore.ReleasePath(set.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(releaseDir, "remote-state.json")
	if err := os.WriteFile(statePath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := localStore.ReadRemoteState(set.Manifest); !errors.Is(err, ErrRemoteStateInvalid) {
		t.Fatalf("truncated remote state error = %v, want %v", err, ErrRemoteStateInvalid)
	}
}

// TestListAndRemoveManageOnlyCanonicalProfileDirectories checks fast metadata enumeration and precise deletion.
func TestListAndRemoveManageOnlyCanonicalProfileDirectories(t *testing.T) {
	root := workspaceTempDir(t)
	localStore, err := New(filepath.Join(root, "indexes"))
	if err != nil {
		t.Fatal(err)
	}

	for _, algorithm := range []segment.Algorithm{segment.DayZ, segment.Battleye} {
		name := "dayz"
		if algorithm == segment.Battleye {
			name = "battleye"
		}
		source := filepath.Join(root, "source", name)
		if err := generate.Build(generate.Config{
			Algorithm:   algorithm,
			Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
			PrefixBits:  2,
			RiceK:       1,
			RiceKSet:    true,
			SegmentBits: 4,
			Jobs:        1,
			Output:      source,
		}); err != nil {
			t.Fatal(err)
		}
		set, err := indexset.Open(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := localStore.Activate(set.Manifest, stagedFiles(source, set.Manifest)); err != nil {
			_ = set.Close()
			t.Fatal(err)
		}
		if err := set.Close(); err != nil {
			t.Fatal(err)
		}
		if algorithm == segment.DayZ {
			releasePath, err := localStore.ProfilePath(name, profile.Profile{
				Version: 1, PrefixBits: 2, RiceK: 1, SegmentBits: 4,
			})
			if err != nil {
				t.Fatal(err)
			}
			segmentPath := filepath.Join(releasePath, "segments", set.Manifest.Segments[0].File)
			if err := os.Remove(segmentPath); err != nil {
				t.Fatalf("remove segment payload before metadata listing: %v", err)
			}
		}
	}

	cachePath := filepath.Join(root, "cache.sqlite")
	if err := os.WriteFile(cachePath, []byte("cache"), 0600); err != nil {
		t.Fatal(err)
	}

	releases, err := localStore.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 || releases[0].Algorithm != "battleye" || releases[1].Algorithm != "dayz" {
		t.Fatalf("List() = %#v", releases)
	}

	for _, release := range releases {
		if release.Profile != "v1-p2-k1-s4" {
			t.Errorf("invalid release summary: %#v", release)
		}
		if release.Algorithm == "dayz" &&
			(release.State != "invalid" || !strings.Contains(release.Error, "segment")) {
			t.Errorf("missing segment was not reported invalid: %#v", release)
		}
		if release.Algorithm == "battleye" &&
			(release.State != "ready" || release.SegmentCount != 1 || release.Size == 0) {
			t.Errorf("valid release was not reported ready: %#v", release)
		}
	}

	if err := localStore.Remove("dayz", profile.Profile{
		Version:     1,
		PrefixBits:  2,
		RiceK:       1,
		SegmentBits: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("Remove deleted the shared cache: %v", err)
	}
	releases, err = localStore.List()
	if err != nil || len(releases) != 1 || releases[0].Algorithm != "battleye" {
		t.Fatalf("List() after Remove = %#v, %v", releases, err)
	}
}

// TestListRejectsSegmentFilesWithUnexpectedSizes checks structural validation without payload hashing.
func TestListRejectsSegmentFilesWithUnexpectedSizes(t *testing.T) {
	root := workspaceTempDir(t)
	localStore, err := New(filepath.Join(root, "indexes"))
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(root, "source")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      source,
	}); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.Activate(set.Manifest, stagedFiles(source, set.Manifest)); err != nil {
		_ = set.Close()
		t.Fatal(err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}

	releasePath, err := localStore.ProfilePath("dayz", profile.Profile{
		Version:     1,
		PrefixBits:  2,
		RiceK:       1,
		SegmentBits: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	record := set.Manifest.Segments[0]
	segmentPath := filepath.Join(releasePath, "segments", record.File)
	if err := os.Truncate(segmentPath, 0); err != nil {
		t.Fatal(err)
	}

	releases, err := localStore.List()
	if err != nil || len(releases) != 1 {
		t.Fatalf("List() = %#v, %v", releases, err)
	}
	if releases[0].State != "invalid" || !strings.Contains(releases[0].Error, record.File) {
		t.Fatalf("wrong-size segment state = %#v", releases[0])
	}
}

// TestListRejectsSegmentSymlinks ensures list never treats links as installed payloads.
func TestListRejectsSegmentSymlinks(t *testing.T) {
	root := workspaceTempDir(t)
	localStore, err := New(filepath.Join(root, "indexes"))
	if err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(root, "source")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      source,
	}); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.Activate(set.Manifest, stagedFiles(source, set.Manifest)); err != nil {
		_ = set.Close()
		t.Fatal(err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}

	releasePath, err := localStore.ProfilePath("dayz", profile.Profile{
		Version:     1,
		PrefixBits:  2,
		RiceK:       1,
		SegmentBits: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	segmentPath := filepath.Join(releasePath, "segments", set.Manifest.Segments[0].File)
	target := filepath.Join(root, "outside.ivz")
	if err := os.Rename(segmentPath, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, segmentPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	releases, err := localStore.List()
	if err != nil || len(releases) != 1 {
		t.Fatalf("List() = %#v, %v", releases, err)
	}
	if releases[0].State != "invalid" || !strings.Contains(releases[0].Error, set.Manifest.Segments[0].File) {
		t.Fatalf("symlink segment state = %#v", releases[0])
	}
	if err := os.Remove(segmentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, segmentPath); err != nil {
		t.Fatal(err)
	}

	segmentDir := filepath.Join(releasePath, "segments")
	movedSegmentDir := filepath.Join(releasePath, "segments-real")
	if err := os.Rename(segmentDir, movedSegmentDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(movedSegmentDir, segmentDir); err != nil {
		t.Skipf("directory symlink creation unavailable: %v", err)
	}

	releases, err = localStore.List()
	if err != nil || len(releases) != 1 {
		t.Fatalf("List() with symlink directory = %#v, %v", releases, err)
	}
	if releases[0].State != "invalid" || !strings.Contains(releases[0].Error, "segments directory") {
		t.Fatalf("symlink segments directory state = %#v", releases[0])
	}
}

// stagedFiles maps each manifest file name to its generated source path.
func stagedFiles(root string, manifest indexset.Manifest) map[string]string {
	files := make(map[string]string, len(manifest.Segments))
	for _, record := range manifest.Segments {
		files[record.File] = filepath.Join(root, "segments", record.File)
	}

	return files
}

// workspaceTempDir creates a temporary directory beneath the module workspace.
func workspaceTempDir(t *testing.T) string {
	t.Helper()
	path, err := os.MkdirTemp(".", ".store-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := os.RemoveAll(path); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})

	return path
}
