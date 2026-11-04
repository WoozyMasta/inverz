// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package resolve

import "errors"

// Sentinel errors classify invalid queries and malformed index candidates.
var (
	// ErrNotFound means the identifier has no match in the configured range.
	ErrNotFound = errors.New("identifier not found in index range")
	// ErrIndexUnavailable means the needed algorithm index is not configured.
	ErrIndexUnavailable = errors.New("matching index is unavailable")
	// ErrAmbiguous means multiple SteamIDs produce the same identifier digest.
	ErrAmbiguous = errors.New("ambiguous digest")
	// ErrInvalidIdentifierFormat means automatic detection cannot recognize the encoded identifier length.
	ErrInvalidIdentifierFormat = errors.New("identifier must be a BattlEye hex GUID or padded DayZ Base64URL GUID")
	// ErrInvalidIdentifierType means the requested query kind is not supported.
	ErrInvalidIdentifierType = errors.New("invalid identifier type")
	// ErrNilResolveContext means Resolve received no context.
	ErrNilResolveContext = errors.New("nil resolve context")
	// ErrIndexAlgorithmMismatch means the selected index does not match the query kind.
	ErrIndexAlgorithmMismatch = errors.New("configured index algorithm does not match query")
	// ErrInvalidDayZDigestLength means a DayZ query does not contain a 32-byte digest.
	ErrInvalidDayZDigestLength = errors.New("invalid dayz digest length")
	// ErrInvalidBattleyeDigestLength means a BattlEye query does not contain a 16-byte digest.
	ErrInvalidBattleyeDigestLength = errors.New("invalid battleye digest length")
	// ErrBucketCandidateCount means a bucket declares more candidates than its segment contains.
	ErrBucketCandidateCount = errors.New("bucket candidate count exceeds segment entry count")
	// ErrBucketAccountOrder means decoded bucket account IDs are out of range or not strictly increasing.
	ErrBucketAccountOrder = errors.New("bucket account IDs are out of range or unordered")
)
