// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package store

import "errors"

var (
	// ErrIndexRootRequired reports that the store root path is empty.
	ErrIndexRootRequired = errors.New("index root is required")
	// ErrIndexRootNotDirectory reports that the configured index root is not a real directory.
	ErrIndexRootNotDirectory = errors.New("index root must be a real directory")
	// ErrProfileNotFound reports that the selected local profile is not installed.
	ErrProfileNotFound = errors.New("index profile is not installed")
	// ErrProfileDirectoryInvalid reports a profile path that is not a real directory.
	ErrProfileDirectoryInvalid = errors.New("index profile path must be a real directory")
	// ErrProfileManifestMismatch reports a profile directory containing another profile's manifest.
	ErrProfileManifestMismatch = errors.New("profile directory does not match its release manifest")
	// ErrUnsupportedAlgorithm reports an algorithm not supported by index sets.
	ErrUnsupportedAlgorithm = errors.New("unsupported index-set algorithm")
	// ErrUnsupportedProfile reports profile parameters outside supported bounds.
	ErrUnsupportedProfile = errors.New("unsupported index-set profile")
	// ErrActiveManifestInvalid reports an active manifest that is not a bounded regular file.
	ErrActiveManifestInvalid = errors.New("active release manifest is not a bounded regular file")
	// ErrActiveManifestTooLarge reports an active manifest exceeding the read limit.
	ErrActiveManifestTooLarge = errors.New("active release manifest exceeds size limit")
	// ErrStagedSegmentTooLarge reports a staged segment exceeding the format limit.
	ErrStagedSegmentTooLarge = errors.New("staged segment exceeds size limit")
	// ErrStagedSegmentMismatch reports staged bytes with a mismatched size or digest.
	ErrStagedSegmentMismatch = errors.New("staged segment size or SHA-256 mismatch")
	// ErrSegmentSizeMismatch reports a segment size that differs from its release record.
	ErrSegmentSizeMismatch = errors.New("segment size does not match release record")
	// ErrSegmentDigestMismatch reports a segment digest that differs from its release record.
	ErrSegmentDigestMismatch = errors.New("segment SHA-256 does not match release record")
	// ErrSegmentHeaderMismatch reports segment metadata that differs from its release record.
	ErrSegmentHeaderMismatch = errors.New("segment header does not match release record")
	// ErrSegmentNotRegular reports an existing segment path that is not a regular file.
	ErrSegmentNotRegular = errors.New("existing segment is not a regular file")
	// ErrSegmentsDirectoryInvalid reports a release whose segments path is not a real directory.
	ErrSegmentsDirectoryInvalid = errors.New("segments directory is missing or invalid")
	// ErrImmutableSegmentChanged reports a changed digest for an existing segment identity.
	ErrImmutableSegmentChanged = errors.New("immutable segment changed digest")
	// ErrCoverageRegression reports activation that would reduce existing release coverage.
	ErrCoverageRegression = errors.New("activation would reduce existing index coverage")
	// ErrStagedSegmentMissing reports missing staged bytes for a segment not already installed.
	ErrStagedSegmentMissing = errors.New("staged segment content is missing")
	// ErrRemoteStateFileInvalid reports remote state that is not a bounded regular file.
	ErrRemoteStateFileInvalid = errors.New("remote state file is invalid")
	// ErrRemoteStateInvalid reports malformed remote metadata that can be refreshed from the registry.
	ErrRemoteStateInvalid = errors.New("remote state metadata is invalid")
	// ErrRemoteStateTrailingData reports extra JSON values after the remote state document.
	ErrRemoteStateTrailingData = errors.New("remote state contains trailing data")
	// ErrRemoteReferenceInvalid reports an invalid or credential-bearing remote reference.
	ErrRemoteReferenceInvalid = errors.New("remote reference is invalid")
	// ErrRemoteManifestDigestInvalid reports a malformed remote manifest digest.
	ErrRemoteManifestDigestInvalid = errors.New("remote manifest digest is invalid")
	// ErrRemoteTimestampInvalid reports a timestamp that is not valid RFC3339.
	ErrRemoteTimestampInvalid = errors.New("remote state timestamp is invalid")
	// ErrDigestAlgorithmNotSHA256 reports a digest using an unsupported algorithm.
	ErrDigestAlgorithmNotSHA256 = errors.New("digest algorithm is not SHA-256")
	// ErrDigestNotCanonicalSHA256 reports a malformed or noncanonical SHA-256 digest.
	ErrDigestNotCanonicalSHA256 = errors.New("digest is not canonical SHA-256")
)
