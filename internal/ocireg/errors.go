// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package ocireg

import "errors"

// Sentinel errors classify invalid publication options and untrusted OCI metadata.
var (
	// ErrIndexSetRequired means Publish received no index set.
	ErrIndexSetRequired = errors.New("index set is required")
	// ErrPartialSegmentsNotAllowed means publishing partial segments needs explicit permission.
	ErrPartialSegmentsNotAllowed = errors.New("partial segments require explicit publish permission")
	// ErrLocalStoreRequired means Pull received no local destination store.
	ErrLocalStoreRequired = errors.New("local store is required")
	// ErrOCIManifestSize means the OCI manifest is empty or exceeds the configured limit.
	ErrOCIManifestSize = errors.New("oci manifest size is outside limits")
	// ErrOCIDescriptorMismatch means an OCI descriptor's size or media type disagrees with its content.
	ErrOCIDescriptorMismatch = errors.New("oci manifest descriptor does not match content")
	// ErrExpectedIndexMismatch means the fetched release differs from the requested index identity.
	ErrExpectedIndexMismatch = errors.New("oci release does not match expected index")
	// ErrUpdateDownloadLimit means missing layers would exceed the incremental download budget.
	ErrUpdateDownloadLimit = errors.New("oci update download size exceeds limits")
	// ErrInvalidOCIManifest means manifest bytes or descriptor metadata are invalid.
	ErrInvalidOCIManifest = errors.New("oci manifest size or descriptor is invalid")
	// ErrOCIDigestMismatch means OCI content does not match its declared digest.
	ErrOCIDigestMismatch = errors.New("oci manifest digest mismatch")
	// ErrUnsupportedOCIArtifact means the manifest does not describe a supported Inverz artifact.
	ErrUnsupportedOCIArtifact = errors.New("oci artifact type or media type is unsupported")
	// ErrOCIConfigSize means the release config descriptor size is outside supported limits.
	ErrOCIConfigSize = errors.New("oci config size is outside limits")
	// ErrOCILayerCount means the manifest layer count is outside supported limits.
	ErrOCILayerCount = errors.New("oci layer count is outside limits")
	// ErrOCILayerCountMismatch means OCI layers do not match the release segment list.
	ErrOCILayerCountMismatch = errors.New("oci layer count does not match release config")
	// ErrOCILayerMismatch means an OCI layer descriptor disagrees with its release segment record.
	ErrOCILayerMismatch = errors.New("oci layer does not match release config")
	// ErrInvalidOCIReference means a registry reference lacks a repository or tag/digest.
	ErrInvalidOCIReference = errors.New("invalid oci reference; include a tag or digest")
	// ErrOCIContentMismatch means downloaded OCI bytes differ from their descriptor size or digest.
	ErrOCIContentMismatch = errors.New("oci content size or digest mismatch")
	// ErrOCILayerContentMismatch means a downloaded layer differs from its descriptor size or digest.
	ErrOCILayerContentMismatch = errors.New("oci layer size or digest mismatch")
)
