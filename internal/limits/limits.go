// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package limits defines shared format and resource limits.
package limits

const (
	// MaxPrefixBits bounds per-segment directory allocations.
	MaxPrefixBits uint8 = 20
	// MaxRiceK is the largest supported Rice parameter.
	MaxRiceK uint8 = 31
	// MaxSegmentBits bounds segment ranges to the uint32 account-ID space.
	MaxSegmentBits uint8 = 32
	// MaxSegments bounds manifest size and open file use.
	MaxSegments = 256
	// MaxManifestBytes bounds release metadata read from disk or registry.
	MaxManifestBytes = 1 << 20
	// MaxOCIManifestBytes bounds registry-supplied OCI metadata.
	MaxOCIManifestBytes = 4 << 20
	// MaxOCIUpdateDownloadSize bounds missing segment layers fetched by one update.
	MaxOCIUpdateDownloadSize int64 = 8 << 30
	// MaxRemoteStateBytes bounds mutable local registry metadata.
	MaxRemoteStateBytes = 16 << 10
	// MaxSegmentFileSize bounds each untrusted segment file.
	MaxSegmentFileSize int64 = 2 << 30
	// MaxBucketByteSize bounds memory used to read one encoded bucket.
	MaxBucketByteSize uint64 = 64 << 20
)
