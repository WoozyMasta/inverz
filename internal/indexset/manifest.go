// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package indexset validates deterministic release metadata for segment sets.
package indexset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

const (
	// ManifestName is the deterministic index-set release file name.
	ManifestName = "release.json"
)

// Manifest describes one immutable algorithm release and its ordered segments.
type Manifest struct {
	Algorithm          string          `json:"algorithm"`            // Digest algorithm used by every segment.
	MinSteamID         string          `json:"min_steamid"`          // Inclusive first SteamID64 as decimal text.
	MaxSteamID         string          `json:"max_steamid"`          // Inclusive last SteamID64 as decimal text.
	EntryCount         string          `json:"entry_count"`          // Number of IDs in the inclusive range.
	Segments           []SegmentRecord `json:"segments"`             // Ordered immutable segment metadata.
	SchemaVersion      uint8           `json:"schema_version"`       // Release manifest schema version.
	IndexFormatVersion uint8           `json:"index_format_version"` // On-disk segment format version.
	PrefixBits         uint8           `json:"prefix_bits"`          // Digest prefix width used for bucketing.
	RiceK              uint8           `json:"rice_k"`               // Rice remainder width used for gaps.
	SegmentBits        uint8           `json:"segment_bits"`         // Globally aligned account segment width.
}

// SegmentRecord identifies one immutable segment file and its content digest.
type SegmentRecord struct {
	File         string `json:"file"`           // Canonical segment filename relative to the segments directory.
	SHA256       string `json:"sha256"`         // Lowercase hexadecimal SHA-256 of the complete file.
	Size         uint64 `json:"size"`           // Complete segment file size in bytes.
	Index        uint32 `json:"index"`          // Global segment index derived from MinAccountID.
	MinAccountID uint32 `json:"min_account_id"` // Inclusive first account ID stored in the segment.
	MaxAccountID uint32 `json:"max_account_id"` // Inclusive last account ID stored in the segment.
}

// SegmentFilename returns the canonical filename for one inclusive range.
func SegmentFilename(minAccountID, maxAccountID uint32) string {
	return fmt.Sprintf("%08x-%08x.ivz", minAccountID, maxAccountID)
}

// Parse validates and parses a bounded, strict release manifest.
func Parse(data []byte) (Manifest, error) {
	var manifest Manifest
	if len(data) == 0 || len(data) > limits.MaxManifestBytes {
		return manifest, ErrManifestSize
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	// Reject schema drift instead of silently ignoring fields written by a newer format.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode release manifest: %w", err)
	}
	// A second decode must reach EOF; this rejects concatenated JSON values and trailing bytes.
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, ErrManifestTrailingData
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}

	return manifest, nil
}

// Marshal returns canonical indented JSON with one trailing newline.
func (m Manifest) Marshal() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release manifest: %w", err)
	}

	return append(data, '\n'), nil
}

// Validate checks manifest versions, bounds, segment order and file names.
func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 || m.IndexFormatVersion != 1 {
		return ErrManifestVersion
	}
	if m.Algorithm != "dayz" && m.Algorithm != "battleye" {
		return ErrUnsupportedAlgorithm
	}

	if m.PrefixBits == 0 ||
		m.PrefixBits > limits.MaxPrefixBits ||
		m.RiceK > limits.MaxRiceK ||
		m.SegmentBits == 0 ||
		m.SegmentBits > limits.MaxSegmentBits {
		return ErrUnsupportedProfile
	}

	if len(m.Segments) == 0 || len(m.Segments) > limits.MaxSegments {
		return ErrSegmentCount
	}

	minSteamID, err := parseSteamID(m.MinSteamID)
	if err != nil {
		return fmt.Errorf("invalid minimum SteamID64: %w", err)
	}

	maxSteamID, err := parseSteamID(m.MaxSteamID)
	if err != nil {
		return fmt.Errorf("invalid maximum SteamID64: %w", err)
	}

	rangeValue := steamid.Range{Min: minSteamID, Max: maxSteamID}
	entryCount, err := rangeValue.Count()
	if err != nil {
		return err
	}
	if m.EntryCount != strconv.FormatUint(entryCount, 10) {
		// Comparing canonical decimal text also rejects leading zeroes in the declared count.
		return ErrEntryCountMismatch
	}

	// A valid range count guarantees both endpoints can be converted to AccountID.
	minAccount, _ := steamid.AccountID(minSteamID)
	maxAccount, _ := steamid.AccountID(maxSteamID)
	if m.Segments[0].MinAccountID != minAccount ||
		m.Segments[len(m.Segments)-1].MaxAccountID != maxAccount {
		return ErrSegmentCoverageMismatch
	}

	segmentSize := uint64(1) << m.SegmentBits
	var covered uint64
	var previous SegmentRecord
	// Require globally bounded, adjacent records so the ordered list covers the range exactly once.
	for i, record := range m.Segments {
		if record.MinAccountID > record.MaxAccountID {
			return fmt.Errorf("%w: segment %d", ErrInvalidSegmentRange, i)
		}

		if uint64(record.MinAccountID)/segmentSize != uint64(record.MaxAccountID)/segmentSize {
			return fmt.Errorf("%w: segment %d", ErrSegmentCrossesBoundary, i)
		}

		// #nosec G115 -- the quotient is bounded by the uint32 AccountID.
		if record.Index != uint32(uint64(record.MinAccountID)/segmentSize) {
			return fmt.Errorf("%w: segment %d", ErrSegmentIndexMismatch, i)
		}

		if i > 0 {
			if record.Index != previous.Index+1 || uint64(record.MinAccountID) != uint64(previous.MaxAccountID)+1 {
				return ErrSegmentOrder
			}
		}

		if !validSegmentName(record.File, record.MinAccountID, record.MaxAccountID) {
			return fmt.Errorf("%w: segment %d", ErrUnsafeSegmentName, i)
		}
		if record.Size < segment.HeaderSize || record.Size > uint64(limits.MaxSegmentFileSize) {
			return fmt.Errorf("%w: segment %d", ErrSegmentSize, i)
		}
		if !validSHA256(record.SHA256) {
			return fmt.Errorf("%w: segment %d", ErrInvalidSegmentSHA256, i)
		}

		covered += uint64(record.MaxAccountID) - uint64(record.MinAccountID) + 1
		previous = record
	}

	if covered != entryCount {
		return ErrSegmentEntryCountMismatch
	}

	return nil
}

// parseSteamID accepts only canonical decimal SteamID64 strings.
func parseSteamID(value string) (uint64, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, ErrSteamIDDecimal
	}

	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrSteamIDDecimal, err)
	}
	if strconv.FormatUint(id, 10) != value {
		return 0, ErrSteamIDNonCanonical
	}

	return id, nil
}

// validSegmentName rejects paths and accepts only the canonical filename for the account range.
func validSegmentName(name string, minAccount, maxAccount uint32) bool {
	if name == "." ||
		name == ".." ||
		strings.ContainsAny(name, `/\`) ||
		filepath.Base(name) != name {
		return false
	}

	return name == SegmentFilename(minAccount, maxAccount)
}

// validSHA256 accepts exactly 64 lowercase hexadecimal characters.
func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}

	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}

		return false
	}

	return true
}
