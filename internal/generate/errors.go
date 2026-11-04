// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import "errors"

// Sentinel errors classify invalid build settings and index generation failures.
var (
	// ErrInvalidPrefixBits means the digest prefix width is outside the supported range.
	ErrInvalidPrefixBits = errors.New("prefix bits must be in range 1..20")
	// ErrInvalidRiceK means the Rice remainder width is outside the supported range.
	ErrInvalidRiceK = errors.New("rice-k must be in range 0..31")
	// ErrInvalidSegmentBits means the segment width is outside the supported range.
	ErrInvalidSegmentBits = errors.New("segment bits must be in range 1..32")
	// ErrInvalidJobs means the requested CPU budget is negative.
	ErrInvalidJobs = errors.New("jobs must not be negative")
	// ErrInvalidAlgorithm means the selected digest algorithm is unsupported.
	ErrInvalidAlgorithm = errors.New("type must be dayz or battleye")
	// ErrOutputRequired means the build output path is empty.
	ErrOutputRequired = errors.New("output is required")
	// ErrPayloadOffsetRange means the segment payload offset exceeds the file API range.
	ErrPayloadOffsetRange = errors.New("payload offset exceeds file offset range")
	// ErrPayloadDirectoryLimit means a payload directory offset exceeds the index format limit.
	ErrPayloadDirectoryLimit = errors.New("payload directory offset exceeds format limit")
	// ErrBucketCountLimit means a bucket contains more values than its format field can represent.
	ErrBucketCountLimit = errors.New("bucket count exceeds index format limit")
	// ErrBucketSizeLimit means a bucket payload exceeds the configured bucket size limit.
	ErrBucketSizeLimit = errors.New("bucket size exceeds limit")
	// ErrSegmentPayloadLimit means the segment payload exceeds the configured file size limit.
	ErrSegmentPayloadLimit = errors.New("segment payload exceeds file size limit")
	// ErrEmptyEstimate means the resource estimate was calculated for an empty range.
	ErrEmptyEstimate = errors.New("estimate has no entries")
	// ErrSegmentCountLimit means a plan contains more segments than the configured limit.
	ErrSegmentCountLimit = errors.New("segment count exceeds limit")
	// ErrExistingProfileMismatch means an existing release uses different immutable codec settings.
	ErrExistingProfileMismatch = errors.New("existing release uses a different immutable profile")
	// ErrCoverageRegression means a build would reduce an existing release's covered SteamID range.
	ErrCoverageRegression = errors.New("build would reduce existing index coverage")
	// ErrOrderedPipelineIncomplete means ordered block results did not cover the requested range.
	ErrOrderedPipelineIncomplete = errors.New("ordered generation pipeline did not cover the full range")
	// ErrImmutableSegmentMismatch means an existing immutable segment has different content.
	ErrImmutableSegmentMismatch = errors.New("immutable segment has different content")
	// ErrBuiltSegmentMismatch means a generated segment header disagrees with its requested profile.
	ErrBuiltSegmentMismatch = errors.New("built segment header does not match requested range and profile")
	// ErrNegativeFileSize means a file operation returned an invalid negative byte count.
	ErrNegativeFileSize = errors.New("file size is negative")
)
