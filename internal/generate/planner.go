// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"fmt"

	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/steamid"
)

// SegmentRange is one requested portion of a globally aligned segment.
type SegmentRange struct {
	Index        uint32 // Global segment index derived from the AccountID boundary.
	MinAccountID uint32 // Inclusive first account ID represented by this segment.
	MaxAccountID uint32 // Inclusive last account ID represented by this segment.
}

// PlanSegments splits a supported SteamID range at global AccountID boundaries.
func PlanSegments(requested steamid.Range, segmentBits uint8) ([]SegmentRange, error) {
	if err := requested.Validate(); err != nil {
		return nil, err
	}

	if segmentBits == 0 || segmentBits > limits.MaxSegmentBits {
		return nil, ErrInvalidSegmentBits
	}
	minAccount, err := steamid.AccountID(requested.Min)
	if err != nil {
		return nil, err
	}
	maxAccount, err := steamid.AccountID(requested.Max)
	if err != nil {
		return nil, err
	}

	size := uint64(1) << segmentBits
	first := uint64(minAccount) / size
	last := uint64(maxAccount) / size
	count := last - first + 1
	if count > limits.MaxSegments {
		return nil, fmt.Errorf("%w: %d, limit %d", ErrSegmentCountLimit, count, limits.MaxSegments)
	}

	segments := make([]SegmentRange, 0, int(count))
	for index := first; index <= last; index++ {
		segmentMin := index * size
		segmentMax := segmentMin + size - 1
		if segmentMin < uint64(minAccount) {
			// Clip only the outer segments; interior boundaries remain globally aligned.
			segmentMin = uint64(minAccount)
		}

		if segmentMax > uint64(maxAccount) {
			// Clip the final segment to the request while preserving its global index.
			segmentMax = uint64(maxAccount)
		}

		// #nosec G115 - all values are bounded by the validated 32-bit AccountID range.
		segments = append(segments, SegmentRange{
			Index:        uint32(index),
			MinAccountID: uint32(segmentMin),
			MaxAccountID: uint32(segmentMax),
		})
	}

	return segments, nil
}
