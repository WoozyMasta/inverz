// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package indexset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// Set is an opened immutable release and its segment readers.
type Set struct {
	Root     string            // Release directory containing the manifest and segment files.
	Segments []*segment.Reader // Open readers in the same order as Manifest.Segments.
	Manifest Manifest          // Validated immutable release metadata.
}

// VerifyEvent reports coarse lifecycle and byte-count changes while hashing a segment.
type VerifyEvent struct {
	State    string // Lifecycle state or progress update name.
	Segment  uint32 // Global segment index, zero for the operation-level event.
	Segments int    // Number of segments included in verification.
	Bytes    uint64 // Cumulative bytes read for this segment.
	Total    uint64 // Segment size, or total index-set size for operation start.
}

// ReadManifest reads and validates bounded release metadata without opening segment files.
func ReadManifest(root string) (Manifest, error) {
	manifestPath := filepath.Join(root, ManifestName)
	// Lstat rejects symlinks so the release entry point must be an actual file.
	stat, err := os.Lstat(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("stat release manifest: %w", err)
	}

	if !stat.Mode().IsRegular() {
		return Manifest{}, ErrManifestNotRegular
	}
	if stat.Size() <= 0 || stat.Size() > limits.MaxManifestBytes {
		return Manifest{}, ErrManifestSize
	}

	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open release manifest: %w", err)
	}

	// Read one byte past the limit to detect a file that grew after the initial stat.
	data, readErr := io.ReadAll(io.LimitReader(manifestFile, limits.MaxManifestBytes+1))
	closeErr := manifestFile.Close()
	if readErr != nil {
		return Manifest{}, fmt.Errorf("read release manifest: %w", readErr)
	}
	if closeErr != nil {
		return Manifest{}, fmt.Errorf("close release manifest: %w", closeErr)
	}
	if len(data) > limits.MaxManifestBytes {
		return Manifest{}, ErrManifestSize
	}

	return Parse(data)
}

// Open reads release.json and opens every compatible segment in the set.
func Open(root string) (*Set, error) {
	manifest, err := ReadManifest(root)
	if err != nil {
		return nil, err
	}

	segmentDir := filepath.Join(root, "segments")
	// Reject symlinked directories so manifest-relative segment paths stay under root.
	segmentDirInfo, err := os.Lstat(segmentDir)
	if err != nil {
		return nil, fmt.Errorf("stat segment directory: %w", err)
	}
	if !segmentDirInfo.IsDir() || segmentDirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSegmentDirectory
	}

	set := &Set{
		Root:     root,
		Manifest: manifest,
		Segments: make([]*segment.Reader, 0, len(manifest.Segments)),
	}
	// Close every previously opened reader on failure; callers receive only complete sets.
	for _, record := range manifest.Segments {
		path := filepath.Join(root, "segments", record.File)
		fileInfo, err := os.Lstat(path)
		if err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("stat segment %s: %w", record.File, err)
		}

		if !fileInfo.Mode().IsRegular() {
			_ = set.Close()
			return nil, fmt.Errorf("%w: %s", ErrSegmentNotRegular, record.File)
		}

		reader, err := segment.Open(path)
		if err != nil {
			_ = set.Close()
			return nil, fmt.Errorf("open segment %s: %w", record.File, err)
		}

		if err := validateSegment(manifest, record, path, reader); err != nil {
			// Open either returns a complete set or closes every reader it opened so far.
			_ = reader.Close()
			_ = set.Close()
			return nil, fmt.Errorf("validate segment %s: %w", record.File, err)
		}

		set.Segments = append(set.Segments, reader)
	}

	return set, nil
}

// Verify checks whole-file manifest digests for every segment.
func (s *Set) Verify() error {
	return s.VerifyWithProgress(nil)
}

// VerifyWithProgress verifies every segment and reports byte progress at MiB boundaries.
func (s *Set) VerifyWithProgress(onProgress func(VerifyEvent)) error {
	var totalBytes uint64
	for _, record := range s.Manifest.Segments {
		totalBytes += record.Size
	}
	if onProgress != nil {
		onProgress(VerifyEvent{
			State:    "operation-started",
			Segments: len(s.Segments),
			Total:    totalBytes,
		})
	}

	for i := range s.Segments {
		record := s.Manifest.Segments[i]
		if onProgress != nil {
			onProgress(VerifyEvent{
				State:    "started",
				Segment:  record.Index,
				Segments: len(s.Segments),
				Total:    record.Size,
			})
		}

		digest, size, err := hashFileProgress(filepath.Join(s.Root, "segments", record.File), func(n uint64) {
			if onProgress != nil {
				onProgress(VerifyEvent{
					State:    "progress",
					Segment:  record.Index,
					Segments: len(s.Segments),
					Bytes:    n,
					Total:    record.Size,
				})
			}
		})
		if err != nil {
			if onProgress != nil {
				onProgress(VerifyEvent{
					State:    "failed",
					Segment:  record.Index,
					Segments: len(s.Segments),
					Bytes:    size,
					Total:    record.Size,
				})
			}

			return fmt.Errorf("hash segment %s: %w", record.File, err)
		}

		if size != record.Size || digest != record.SHA256 {
			if onProgress != nil {
				onProgress(VerifyEvent{
					State:    "failed",
					Segment:  record.Index,
					Segments: len(s.Segments),
					Bytes:    size,
					Total:    record.Size,
				})
			}
			return fmt.Errorf("%w: %s", ErrSegmentDigestMismatch, record.File)
		}

		if onProgress != nil {
			onProgress(VerifyEvent{
				State:    "completed",
				Segment:  record.Index,
				Segments: len(s.Segments),
				Bytes:    size,
				Total:    record.Size,
			})
		}
	}

	return nil
}

// Close closes every opened segment and joins close errors.
func (s *Set) Close() error {
	var closeErrors []error
	for _, reader := range s.Segments {
		if err := reader.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}

	s.Segments = nil
	return errors.Join(closeErrors...)
}

// validateSegment compares a segment header and file size with its manifest record.
func validateSegment(manifest Manifest, record SegmentRecord, path string, reader *segment.Reader) error {
	h := reader.Header
	expectedAlgorithm := segment.DayZ
	if manifest.Algorithm == "battleye" {
		expectedAlgorithm = segment.Battleye
	}
	minSteamID := steamid.SteamID64(record.MinAccountID)
	maxSteamID := steamid.SteamID64(record.MaxAccountID)
	entries := uint64(record.MaxAccountID) - uint64(record.MinAccountID) + 1

	// The manifest has already restricted Algorithm to the two supported spellings.
	if h.Algorithm != expectedAlgorithm ||
		h.PrefixBits != manifest.PrefixBits ||
		h.RiceK != manifest.RiceK ||
		h.Min != minSteamID ||
		h.Max != maxSteamID ||
		h.EntryCount != entries {
		return ErrSegmentIncompatible
	}

	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	if stat.Size() < 0 {
		return ErrNegativeFileSize
	}
	// #nosec G115 - negative file sizes are rejected above.
	if uint64(stat.Size()) != record.Size {
		return ErrSegmentFileSizeMismatch
	}

	return nil
}

// hashFileProgress hashes a file with unchanged copy buffering and optional MiB callbacks.
func hashFileProgress(path string, onProgress func(uint64)) (string, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}

	defer func() { _ = f.Close() }()
	return hashReaderProgress(f, onProgress)
}

// hashReaderProgress returns bytes consumed even when the source fails mid-stream.
func hashReaderProgress(source io.Reader, onProgress func(uint64)) (string, uint64, error) {
	h := sha256.New()
	reader := source
	var tracked *progressReader
	if onProgress != nil {
		tracked = &progressReader{reader: source, report: onProgress}
		reader = tracked
	}
	size, err := io.Copy(h, reader)
	if tracked != nil {
		tracked.reportTotal()
	}
	if err != nil {
		return "", copiedBytes(size), err
	}

	if size < 0 {
		return "", 0, ErrNegativeFileSize
	}

	// #nosec G115 - io.Copy returns a nonnegative byte count.
	return hex.EncodeToString(h.Sum(nil)), copiedBytes(size), nil
}

// progressReader batches read notifications so hashing remains independent of UI refreshes.
type progressReader struct {
	reader   io.Reader    // Original file stream.
	report   func(uint64) // Receives cumulative bytes at one-MiB boundaries.
	total    uint64       // Total bytes read from reader.
	pending  uint64       // Bytes accumulated since the previous report.
	reported uint64       // Last cumulative byte count sent to the callback.
}

// Read forwards bytes and reports once per MiB, plus the final partial block.
func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.total += uint64(n)
		r.pending += uint64(n)
		if r.pending >= 1<<20 {
			r.reportTotal()
			r.pending = 0
		}
	} else if err == io.EOF && r.pending > 0 {
		r.reportTotal()
		r.pending = 0
	}

	return n, err
}

// reportTotal emits only a new cumulative count, including a final partial block.
func (r *progressReader) reportTotal() {
	if r.total != r.reported {
		r.report(r.total)
		r.reported = r.total
	}
}

// copiedBytes converts the nonnegative byte count returned by io.Copy to a progress value.
func copiedBytes(size int64) uint64 {
	if size <= 0 {
		return 0
	}

	// #nosec G115 - negative sizes and zero are handled above.
	return uint64(size)
}
