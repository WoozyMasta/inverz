// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package indexset

import "errors"

// Sentinel errors classify malformed release manifests and incompatible segment files.
var (
	// ErrManifestSize means the release manifest is empty or exceeds its size limit.
	ErrManifestSize = errors.New("release manifest size is outside limits")
	// ErrManifestTrailingData means data follows the single JSON release object.
	ErrManifestTrailingData = errors.New("release manifest contains trailing data")
	// ErrManifestVersion means the manifest or index format version is unsupported.
	ErrManifestVersion = errors.New("unsupported release manifest version")
	// ErrUnsupportedAlgorithm means the release names an unsupported digest algorithm.
	ErrUnsupportedAlgorithm = errors.New("unsupported release algorithm")
	// ErrUnsupportedProfile means one or more codec settings are outside supported limits.
	ErrUnsupportedProfile = errors.New("unsupported release profile")
	// ErrSegmentCount means the release has no segments or exceeds the segment limit.
	ErrSegmentCount = errors.New("release segment count is outside limits")
	// ErrEntryCountMismatch means the declared entry count differs from the SteamID range.
	ErrEntryCountMismatch = errors.New("release entry count does not match SteamID range")
	// ErrSegmentCoverageMismatch means the first or last segment misses the release bounds.
	ErrSegmentCoverageMismatch = errors.New("segment coverage does not match release range")
	// ErrInvalidSegmentRange means a segment's inclusive account range is reversed.
	ErrInvalidSegmentRange = errors.New("segment has an invalid account range")
	// ErrSegmentCrossesBoundary means a segment spans more than one global segment boundary.
	ErrSegmentCrossesBoundary = errors.New("segment crosses a global segment boundary")
	// ErrSegmentIndexMismatch means a segment index does not match its global account range.
	ErrSegmentIndexMismatch = errors.New("segment index does not match its account range")
	// ErrSegmentOrder means segments are unordered, overlap, or leave a gap.
	ErrSegmentOrder = errors.New("segments are unordered, overlap or leave a coverage gap")
	// ErrUnsafeSegmentName means a segment filename is unsafe or not canonical for its range.
	ErrUnsafeSegmentName = errors.New("segment has an unsafe or unexpected file name")
	// ErrSegmentSize means a segment size is outside the supported file limits.
	ErrSegmentSize = errors.New("segment size is outside limits")
	// ErrInvalidSegmentSHA256 means a segment digest is not lowercase hexadecimal SHA-256.
	ErrInvalidSegmentSHA256 = errors.New("segment has an invalid SHA-256")
	// ErrSegmentEntryCountMismatch means the segment entry total differs from the release range.
	ErrSegmentEntryCountMismatch = errors.New("segment entry total does not match release range")
	// ErrSteamIDDecimal means a SteamID64 value is not a canonical decimal string.
	ErrSteamIDDecimal = errors.New("steamid64 must be a decimal string")
	// ErrSteamIDNonCanonical means a SteamID64 value has leading zeroes or another noncanonical form.
	ErrSteamIDNonCanonical = errors.New("steamid64 must use canonical decimal notation")
	// ErrManifestNotRegular means the release manifest path is not a regular file.
	ErrManifestNotRegular = errors.New("release manifest must be a regular file")
	// ErrSegmentDirectory means the segment path is not a real directory.
	ErrSegmentDirectory = errors.New("segment directory must be a real directory")
	// ErrSegmentNotRegular means a segment path is not a regular file.
	ErrSegmentNotRegular = errors.New("segment must be a regular file")
	// ErrSegmentIncompatible means a segment header disagrees with its release record.
	ErrSegmentIncompatible = errors.New("segment header is incompatible with release record")
	// ErrNegativeFileSize means a file operation returned an invalid negative byte count.
	ErrNegativeFileSize = errors.New("file size is negative")
	// ErrSegmentFileSizeMismatch means the segment length differs from its manifest record.
	ErrSegmentFileSizeMismatch = errors.New("segment file size does not match release record")
	// ErrSegmentDigestMismatch means a segment's complete-file digest or size differs from its record.
	ErrSegmentDigestMismatch = errors.New("segment does not match release digest or size")
)
