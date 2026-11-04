// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package inverz

import "errors"

// Sentinel errors classify failures exposed by the stable root API.
var (
	// ErrInvalidAlgorithm identifies an unsupported persistent algorithm name.
	ErrInvalidAlgorithm = errors.New("invalid index algorithm")
	// ErrInvalidProfile identifies unsupported or noncanonical profile settings.
	ErrInvalidProfile = errors.New("invalid index profile")
	// ErrInvalidIdentifier identifies an invalid identifier for the selected algorithm.
	ErrInvalidIdentifier = errors.New("invalid identifier")
	// ErrIdentifierNotFound means the identifier has no match in the opened index range.
	ErrIdentifierNotFound = errors.New("identifier not found in index range")
	// ErrAmbiguousIdentifier means multiple SteamIDs produce the requested digest.
	ErrAmbiguousIdentifier = errors.New("ambiguous identifier digest")
	// ErrNilContext identifies an operation invoked without a context.
	ErrNilContext = errors.New("nil context")
	// ErrIndexClosed identifies an operation attempted after the index was closed.
	ErrIndexClosed = errors.New("index is closed")
)
