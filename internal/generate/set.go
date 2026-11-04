// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/releaselock"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// buildHooks exposes controlled failure points to package tests.
type buildHooks struct {
	beforeActivate func() error // Runs after segment installation and before manifest activation.
}

// Build creates or extends one deterministic index-set release.
func Build(c Config) error {
	_, err := BuildWithResult(c)
	return err
}

// BuildWithResult creates or extends a release and returns its final summary.
func BuildWithResult(c Config) (result BuildResult, buildErr error) {
	if err := c.Validate(); err != nil {
		return BuildResult{}, err
	}

	lock, err := releaselock.Acquire(c.Output)
	if err != nil {
		return BuildResult{}, err
	}
	defer func() { buildErr = errors.Join(buildErr, lock.Close()) }()

	if err := build(c, nil); err != nil {
		return BuildResult{}, err
	}

	set, err := indexset.Open(c.Output)
	if err != nil {
		return BuildResult{}, fmt.Errorf("open built release summary: %w", err)
	}

	entryCount, err := c.Range.Count()
	if err != nil {
		_ = set.Close()
		return BuildResult{}, err
	}

	result = BuildResult{
		Algorithm:    set.Manifest.Algorithm,
		MinSteamID:   c.Range.Min,
		MaxSteamID:   c.Range.Max,
		EntryCount:   entryCount,
		SegmentCount: len(set.Manifest.Segments),
	}
	if err := set.Close(); err != nil {
		return BuildResult{}, fmt.Errorf("close built release summary: %w", err)
	}

	return result, nil
}

// build validates a request, installs immutable segments, and activates its manifest.
func build(c Config, hooks *buildHooks) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.Context.Err(); err != nil {
		return err
	}

	plan, err := PlanSegments(c.Range, c.SegmentBits)
	if err != nil {
		return err
	}

	segmentDir := filepath.Join(c.Output, "segments")
	if err := os.MkdirAll(segmentDir, 0700); err != nil {
		return fmt.Errorf("create segment directory: %w", err)
	}

	var oldManifest *indexset.Manifest
	var oldSet *indexset.Set
	if c.Force {
		manifest, readErr := indexset.ReadManifest(c.Output)
		if readErr == nil {
			oldManifest = &manifest
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read existing release manifest before rebuild: %w", readErr)
		}
	} else {
		oldSet, err = indexset.Open(c.Output)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			// Only a missing manifest means a new release; preserve errors from corrupt releases.
			if _, statErr := os.Stat(
				filepath.Join(c.Output, indexset.ManifestName),
			); !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("open existing release before extension: %w", err)
			}

			oldSet = nil
		}

		if oldSet != nil {
			defer func() { _ = oldSet.Close() }()
			oldManifest = &oldSet.Manifest
		}
	}

	if oldManifest != nil {
		if !sameProfile(*oldManifest, c) {
			return ErrExistingProfileMismatch
		}

		oldMin := steamid.SteamID64(oldManifest.Segments[0].MinAccountID)
		oldMax := steamid.SteamID64(oldManifest.Segments[len(oldManifest.Segments)-1].MaxAccountID)
		if c.Range.Min > oldMin || c.Range.Max < oldMax {
			return ErrCoverageRegression
		}
	}

	oldRecords := make(map[uint32]indexset.SegmentRecord)
	if oldManifest != nil {
		for _, record := range oldManifest.Segments {
			oldRecords[record.Index] = record
		}
	}

	manifest := indexset.Manifest{
		SchemaVersion:      1,
		Algorithm:          algorithmName(c.Algorithm),
		IndexFormatVersion: 1,
		PrefixBits:         c.PrefixBits,
		RiceK:              c.RiceK,
		SegmentBits:        c.SegmentBits,
		MinSteamID:         strconv.FormatUint(c.Range.Min, 10),
		MaxSteamID:         strconv.FormatUint(c.Range.Max, 10),
	}
	entryCount, _ := c.Range.Count()
	manifest.EntryCount = strconv.FormatUint(entryCount, 10)
	manifest.Segments = make([]indexset.SegmentRecord, 0, len(plan))

	for _, planned := range plan {
		if err := c.Context.Err(); err != nil {
			return err
		}

		fileName := indexset.SegmentFilename(planned.MinAccountID, planned.MaxAccountID)
		// Reuse only a segment with the same global index and exact inclusive bounds.
		if old, ok := oldRecords[planned.Index]; ok && !c.Force &&
			old.MinAccountID == planned.MinAccountID &&
			old.MaxAccountID == planned.MaxAccountID {
			manifest.Segments = append(manifest.Segments, old)
			if c.OnProgress != nil {
				c.OnProgress(ProgressEvent{State: "completed", Segment: planned.Index, Segments: len(plan)})
			}

			continue
		}

		minSteamID := steamid.SteamID64(planned.MinAccountID)
		maxSteamID := steamid.SteamID64(planned.MaxAccountID)
		stageName, err := makeStageName(segmentDir)
		if err != nil {
			return err
		}

		stageConfig := c
		stageConfig.SegmentIndex = planned.Index
		stageConfig.SegmentCount = len(plan)
		// Build into a same-directory staging name so installation can use an atomic rename.
		stageConfig.Range = steamid.Range{Min: minSteamID, Max: maxSteamID}
		stageConfig.Output = stageName

		built, err := buildSegment(stageConfig)
		if err != nil {
			_ = os.Remove(stageName)
			return fmt.Errorf("build segment %d: %w", planned.Index, err)
		}

		record, err := inspectBuiltSegment(stageName, c, planned, fileName, built)
		if err != nil {
			_ = os.Remove(stageName)
			return fmt.Errorf("verify built segment %d: %w", planned.Index, err)
		}

		finalPath := filepath.Join(segmentDir, fileName)
		old, hasCanonical := oldRecords[planned.Index]
		hasCanonical = hasCanonical &&
			old.MinAccountID == planned.MinAccountID &&
			old.MaxAccountID == planned.MaxAccountID

		if c.Force && hasCanonical && (record.SHA256 != old.SHA256 || record.Size != old.Size) {
			_ = os.Remove(stageName)
			return fmt.Errorf("%w: rebuilt segment %s differs from release manifest", ErrImmutableSegmentMismatch, fileName)
		}

		if info, statErr := os.Lstat(finalPath); statErr == nil {
			if !info.Mode().IsRegular() {
				_ = os.Remove(stageName)
				return fmt.Errorf("existing segment %s: %w", fileName, ErrImmutableSegmentMismatch)
			}

			if c.Force && hasCanonical {
				// The manifest digest is canonical; force may replace a damaged on-disk copy.
				if err := os.Rename(stageName, finalPath); err != nil {
					_ = os.Remove(stageName)
					return fmt.Errorf("repair segment %s: %w", fileName, err)
				}
			} else {
				// Orphaned names without a manifest record remain immutable by content.
				existingHash, existingSize, hashErr := hashFile(finalPath)
				if hashErr != nil {
					_ = os.Remove(stageName)
					return fmt.Errorf("hash existing immutable segment %s: %w", fileName, hashErr)
				}

				if existingHash != record.SHA256 || existingSize != record.Size {
					_ = os.Remove(stageName)
					return fmt.Errorf("%w: %s", ErrImmutableSegmentMismatch, fileName)
				}

				_ = os.Remove(stageName)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			_ = os.Remove(stageName)
			return fmt.Errorf("stat existing segment %s: %w", fileName, statErr)
		} else if err := os.Rename(stageName, finalPath); err != nil {
			_ = os.Remove(stageName)
			return fmt.Errorf("install segment %s: %w", fileName, err)
		}

		manifest.Segments = append(manifest.Segments, record)
	}
	if hooks != nil && hooks.beforeActivate != nil {
		if err := hooks.beforeActivate(); err != nil {
			return fmt.Errorf("activate release: %w", err)
		}
	}
	if err := c.Context.Err(); err != nil {
		return err
	}

	// Rename the fully written manifest last so readers observe either the old or new release.
	if err := writeManifest(c.Output, manifest); err != nil {
		return err
	}

	return nil
}

// sameProfile reports whether an existing release can be extended without changing its codec.
func sameProfile(manifest indexset.Manifest, c Config) bool {
	return manifest.Algorithm == algorithmName(c.Algorithm) &&
		manifest.PrefixBits == c.PrefixBits &&
		manifest.RiceK == c.RiceK &&
		manifest.SegmentBits == c.SegmentBits
}

// makeStageName reserves a unique temporary name in the target segment directory.
func makeStageName(directory string) (string, error) {
	file, err := os.CreateTemp(directory, ".segment-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create segment staging path: %w", err)
	}

	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("close segment staging file: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return "", fmt.Errorf("prepare segment staging path: %w", err)
	}

	return name, nil
}

// inspectBuiltSegment verifies a generated segment and records its immutable file metadata.
func inspectBuiltSegment(
	path string,
	c Config,
	planned SegmentRange,
	fileName string,
	built builtSegment,
) (indexset.SegmentRecord, error) {
	reader, err := segment.Open(path)
	if err != nil {
		return indexset.SegmentRecord{}, err
	}

	minSteamID := steamid.SteamID64(planned.MinAccountID)
	maxSteamID := steamid.SteamID64(planned.MaxAccountID)
	if reader.Header.Algorithm != c.Algorithm ||
		reader.Header.PrefixBits != c.PrefixBits ||
		reader.Header.RiceK != c.RiceK ||
		reader.Header.Min != minSteamID ||
		reader.Header.Max != maxSteamID {
		// Close before returning because the temporary segment is removed by the caller.
		_ = reader.Close()
		return indexset.SegmentRecord{}, ErrBuiltSegmentMismatch
	}

	if err := reader.Close(); err != nil {
		return indexset.SegmentRecord{}, err
	}

	return indexset.SegmentRecord{
		Index:        planned.Index,
		File:         fileName,
		MinAccountID: planned.MinAccountID,
		MaxAccountID: planned.MaxAccountID,
		Size:         built.Size,
		SHA256:       built.SHA256,
	}, nil
}

// hashFile returns a file's SHA-256 digest and byte length.
func hashFile(path string) (string, uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}

	defer func() { _ = file.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, file)
	if err != nil {
		return "", 0, err
	}

	if size < 0 {
		return "", 0, ErrNegativeFileSize
	}

	// #nosec G115 - io.Copy returns a nonnegative byte count.
	return hex.EncodeToString(h.Sum(nil)), uint64(size), nil
}

// writeManifest writes and syncs a temporary manifest before atomically activating it.
func writeManifest(root string, manifest indexset.Manifest) error {
	data, err := manifest.Marshal()
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(root, ".release-*.tmp")
	if err != nil {
		return fmt.Errorf("create release manifest staging file: %w", err)
	}

	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write release manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync release manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close release manifest: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(root, indexset.ManifestName)); err != nil {
		return fmt.Errorf("activate release manifest: %w", err)
	}

	return nil
}
