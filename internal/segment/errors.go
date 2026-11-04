// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package segment

import "errors"

var (
	// ErrInvalidMagic reports that the file does not have the index magic bytes.
	ErrInvalidMagic = errors.New("invalid index magic")
	// ErrUnsupportedVersion reports an index version that this reader cannot parse.
	ErrUnsupportedVersion = errors.New("unsupported index version")
	// ErrInvalidHeaderSize reports a header with an unsupported encoded size.
	ErrInvalidHeaderSize = errors.New("invalid header size")
	// ErrUnsupportedHeaderFlags reports nonzero flags or reserved flag bytes.
	ErrUnsupportedHeaderFlags = errors.New("unsupported header flags")
	// ErrNonzeroReservedBytes reports nonzero bytes reserved for future format use.
	ErrNonzeroReservedBytes = errors.New("nonzero reserved header bytes")
	// ErrUnknownAlgorithm reports an algorithm value not defined by this format.
	ErrUnknownAlgorithm = errors.New("unknown index algorithm")
	// ErrUnsupportedPrefixWidth reports a prefix width outside supported bounds.
	ErrUnsupportedPrefixWidth = errors.New("unsupported prefix width")
	// ErrUnsupportedRiceParameter reports a Rice parameter outside supported bounds.
	ErrUnsupportedRiceParameter = errors.New("unsupported Rice parameter")
	// ErrInvalidSteamIDRange reports a range outside the supported SteamID domain.
	ErrInvalidSteamIDRange = errors.New("invalid SteamID range")
	// ErrEntryCountMismatch reports an entry count inconsistent with the ID range.
	ErrEntryCountMismatch = errors.New("entry count does not match SteamID range")
	// ErrBucketCountMismatch reports a bucket count inconsistent with prefix width.
	ErrBucketCountMismatch = errors.New("bucket count does not match prefix width")
	// ErrInvalidDirectoryLayout reports directory metadata inconsistent with v1 layout.
	ErrInvalidDirectoryLayout = errors.New("invalid directory layout")
	// ErrInvalidPayloadOffset reports a payload offset inconsistent with the directory.
	ErrInvalidPayloadOffset = errors.New("invalid payload offset")
	// ErrSegmentSizeLimit reports a declared segment size beyond the format limit.
	ErrSegmentSizeLimit = errors.New("segment size exceeds format limit")
	// ErrInvalidFileSize reports a file size that cannot be represented safely.
	ErrInvalidFileSize = errors.New("index file has invalid size")
	// ErrSegmentFileSizeLimit reports a segment file larger than the reader limit.
	ErrSegmentFileSizeLimit = errors.New("segment file exceeds size limit")
	// ErrFileSizeMismatch reports disagreement between the file length and header.
	ErrFileSizeMismatch = errors.New("index file size does not match header")
	// ErrDirectoryOffsetRange reports a directory offset outside the OS file API range.
	ErrDirectoryOffsetRange = errors.New("directory offset exceeds file offset range")
	// ErrDirectoryChecksumMismatch reports a directory checksum mismatch.
	ErrDirectoryChecksumMismatch = errors.New("directory checksum mismatch")
	// ErrDirectoryMustStartAtZero reports a directory whose first offset is nonzero.
	ErrDirectoryMustStartAtZero = errors.New("directory must start at zero")
	// ErrDirectoryOffsetsNotMonotonic reports a decreasing directory offset.
	ErrDirectoryOffsetsNotMonotonic = errors.New("directory offsets are not monotonic")
	// ErrDirectoryOffsetExceedsPayload reports an offset past the declared payload.
	ErrDirectoryOffsetExceedsPayload = errors.New("directory offset exceeds payload")
	// ErrBucketSizeOutOfBounds reports a bucket outside structural size limits.
	ErrBucketSizeOutOfBounds = errors.New("bucket size is outside limits")
	// ErrDirectoryEndMismatch reports a final directory offset unequal to payload size.
	ErrDirectoryEndMismatch = errors.New("directory end does not match payload size")
	// ErrBucketOffsetRange reports a bucket offset outside the OS file API range.
	ErrBucketOffsetRange = errors.New("bucket offset exceeds file offset range")
	// ErrBucketLengthRange reports a bucket too large for the current process address space.
	ErrBucketLengthRange = errors.New("bucket length exceeds process address space")
	// ErrPayloadOffsetRange reports a payload range outside the OS file API range.
	ErrPayloadOffsetRange = errors.New("payload range exceeds file offset range")
	// ErrPayloadChecksumMismatch reports a payload checksum mismatch.
	ErrPayloadChecksumMismatch = errors.New("payload checksum mismatch")
)
