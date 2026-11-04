// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"math"
	"unsafe"

	"github.com/woozymasta/inverz/internal/rice"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

const (
	// conservativePayloadFactor reserves twice the expected Rice payload for retained slice capacity.
	conservativePayloadFactor = 2
	// conservativePayloadDivisor expresses the payload factor as a fraction.
	conservativePayloadDivisor = 1
	// estimatedWorkerStackBytes approximates the initial stack reserved per pipeline goroutine.
	estimatedWorkerStackBytes = 2 << 10
)

// SegmentEstimate describes the advisory output and live-memory cost for one segment.
type SegmentEstimate struct {
	Entries               uint64       // Number of account IDs in the segment.
	RicePayloadBytes      uint64       // Expected live bytes held by encoded bucket streams.
	EncoderStateBytes     uint64       // Rice encoder structs, bucket counts, and last-account values.
	HashBufferBytes       uint64       // Bounded bucket and offset buffers held by hash workers.
	DirectoryBytes        uint64       // In-memory directory and serialized directory copy.
	PipelineOverheadBytes uint64       // Bounded jobs, results, shard metadata, and worker stacks.
	ExpectedRAMBytes      uint64       // Expected live generator memory without allocator slack.
	PeakRAMBytes          uint64       // Conservative peak estimate including Rice slice-capacity overhead.
	ExpectedCandidates    float64      // Expected average candidate count per prefix bucket.
	FixedBytes            uint64       // Header, directory, and bucket-count bytes independent of entries.
	ExpectedOutputBytes   uint64       // Estimated segment size; this value is advisory.
	Range                 SegmentRange // Inclusive account-ID bounds represented by the segment.
	Full                  bool         // Whether the range fills its globally aligned segment.
}

// PlanEstimate summarizes output and peak memory requirements for a segmented build.
type PlanEstimate struct {
	Segments                 []SegmentEstimate // Resource estimates for each planned segment.
	MinSteamID               uint64            // First SteamID64 in the requested range.
	MaxSteamID               uint64            // Last SteamID64 in the requested range.
	TotalEntries             uint64            // Total account IDs across all planned segments.
	ExpectedCandidatesPerSet float64           // Expected candidate count per prefix bucket across the set.
	ExpectedRAMBytes         uint64            // Largest expected live memory among planned segments.
	PeakRAMBytes             uint64            // Largest conservative peak among planned segments.
	ExpectedOutputBytes      uint64            // Estimated total output size; this value is advisory.
	PrefixBits               uint8             // Digest prefix width used by the estimate.
	RiceK                    uint8             // Rice remainder width used by the estimate.
	SegmentBits              uint8             // Globally aligned segment width used by the estimate.
	PublishableFullSegments  bool              // Whether all segments cover a complete aligned range.
}

// memoryEstimate groups bounded live allocations for one active segment.
type memoryEstimate struct {
	encoderState uint64
	hashBuffers  uint64
	directory    uint64
	pipeline     uint64
	expected     uint64
	conservative uint64
}

// Estimate returns a per-segment resource estimate; memory values are advisory.
func (c Config) Estimate() (PlanEstimate, error) {
	if err := c.ValidateProfile(); err != nil {
		return PlanEstimate{}, err
	}

	plan, err := PlanSegments(c.Range, c.SegmentBits)
	if err != nil {
		return PlanEstimate{}, err
	}

	buckets := uint64(1) << c.PrefixBits
	result := PlanEstimate{
		MinSteamID:              c.Range.Min,
		MaxSteamID:              c.Range.Max,
		PrefixBits:              c.PrefixBits,
		RiceK:                   c.RiceK,
		SegmentBits:             c.SegmentBits,
		PublishableFullSegments: true,
		Segments:                make([]SegmentEstimate, 0, len(plan)),
	}

	for _, item := range plan {
		entries := uint64(item.MaxAccountID) - uint64(item.MinAccountID) + 1
		fixed := uint64(segment.HeaderSize) + (buckets+1)*4 + buckets*4

		// Rice gaps have geometric spacing; this expected code length estimates only payload bytes.
		emptyProbability := math.Pow(1-1/float64(buckets), float64(uint64(1)<<c.RiceK))
		bitsPerEntry := float64(c.RiceK) + 1 + emptyProbability/(1-emptyProbability)
		estimatedBytes := fixed + uint64(float64(entries)*bitsPerEntry/8)
		payloadBytes := estimatedBytes - fixed
		memory := estimateSegmentMemory(entries, buckets, payloadBytes, c.Jobs, c.PrefixBits)

		full := uint64(item.MaxAccountID)-uint64(item.MinAccountID)+1 == uint64(1)<<c.SegmentBits
		if !full {
			result.PublishableFullSegments = false
		}
		if memory.expected > result.ExpectedRAMBytes {
			result.ExpectedRAMBytes = memory.expected
		}
		if memory.conservative > result.PeakRAMBytes {
			result.PeakRAMBytes = memory.conservative
		}

		result.TotalEntries += entries
		result.ExpectedCandidatesPerSet += float64(entries) / float64(buckets)
		result.ExpectedOutputBytes += estimatedBytes
		result.Segments = append(result.Segments, SegmentEstimate{
			Range:                 item,
			Entries:               entries,
			RicePayloadBytes:      payloadBytes,
			EncoderStateBytes:     memory.encoderState,
			HashBufferBytes:       memory.hashBuffers,
			DirectoryBytes:        memory.directory,
			PipelineOverheadBytes: memory.pipeline,
			ExpectedRAMBytes:      memory.expected,
			PeakRAMBytes:          memory.conservative,
			ExpectedCandidates:    float64(entries) / float64(buckets),
			FixedBytes:            fixed,
			ExpectedOutputBytes:   estimatedBytes,
			Full:                  full,
		})
	}

	if result.TotalEntries == 0 {
		return PlanEstimate{}, ErrEmptyEstimate
	}

	return result, nil
}

// estimateSegmentMemory follows the one-pass encoder's retained buffers and bounded queues.
func estimateSegmentMemory(entries, buckets, payloadBytes uint64, jobs int, prefixBits uint8) memoryEstimate {
	_, hashWorkers, encodeWorkers, _ := resolveJobs(jobs)
	logicalShards := uint64(1) << min(int(prefixBits), 5)
	blockEntries := min(uint64(orderedBlockSize), entries)
	hashCount := positiveIntAsUint64(hashWorkers)
	encodeCount := positiveIntAsUint64(encodeWorkers)
	bufferCount := hashCount + 1

	encoderState := buckets * (uint64(unsafe.Sizeof(rice.Encoder{})) + 8 + 4)
	directory := (buckets + 1) * 8                // Mutable offsets plus their serialized copy.
	hashBuffers := bufferCount * blockEntries * 6 // uint32 buckets and uint16 grouped offsets.
	pipeline := (hashCount+encodeCount)*estimatedWorkerStackBytes +
		hashCount*uint64(unsafe.Sizeof(blockJob{})) +
		hashCount*uint64(unsafe.Sizeof(blockResult{})) +
		hashCount*2*uint64(unsafe.Sizeof(blockResult{})) +
		logicalShards*uint64(unsafe.Sizeof(encoderJob{}))

	base := encoderState + directory + hashBuffers + pipeline
	conservativePayload := payloadBytes * conservativePayloadFactor / conservativePayloadDivisor

	return memoryEstimate{
		encoderState: encoderState,
		hashBuffers:  hashBuffers,
		directory:    directory,
		pipeline:     pipeline,
		expected:     base + payloadBytes,
		conservative: base + conservativePayload,
	}
}

// positiveIntAsUint64 converts a nonnegative worker count to the estimator's unsigned byte domain.
func positiveIntAsUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}

	return uint64(value)
}

// SteamIDRange converts the planned account range to SteamID64.
func (e SegmentEstimate) SteamIDRange() steamid.Range {
	return steamid.Range{
		Min: steamid.SteamID64(e.Range.MinAccountID),
		Max: steamid.SteamID64(e.Range.MaxAccountID),
	}
}
