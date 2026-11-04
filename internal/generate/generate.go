// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package generate builds deterministic on-disk identifier indexes.
package generate

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/prefix"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

const (
	// segmentPayloadBufferSize batches per-bucket writes into larger file operations.
	segmentPayloadBufferSize = 1 << 20
)

// Config controls one algorithm build.
type Config struct {
	Context      context.Context     // Cancels build work between bounded chunks.
	OnProgress   func(ProgressEvent) // Receives serialized build progress callbacks.
	Output       string              // Index-set directory to create or extend.
	Range        steamid.Range       // Inclusive SteamID64 range to index.
	SegmentCount int                 // Number of segments in the requested release.
	Jobs         int                 // Total CPU parallelism budget shared by hashing and encoding.
	SegmentIndex uint32              // Global index of the segment currently being built.
	Force        bool                // Rebuild exact matching segments instead of reusing them.
	Algorithm    segment.Algorithm   // DayZ or BattlEye digest format.
	PrefixBits   uint8               // Number of leading digest bits used for bucket selection.
	RiceK        uint8               // Rice remainder width used to encode bucket gaps.
	SegmentBits  uint8               // Globally aligned AccountID segment width.
	RiceKSet     bool                // Whether RiceK was explicitly selected, including an explicit zero.
}

// ProgressEvent reports a completed build milestone.
type ProgressEvent struct {
	Phase     string // Build phase such as build or write.
	State     string // Lifecycle state: started, progress, completed, or failed.
	Segment   uint32 // Global segment index; zero for operation-level events.
	Segments  int    // Total segments in the release.
	Processed uint64 // Completed units in the phase.
	Total     uint64 // Total units in the phase.
	Percent   uint64 // Rounded integer completion percentage.
}

// BuildResult describes the index set produced by a successful build.
type BuildResult struct {
	Algorithm    string `json:"algorithm" yaml:"algorithm"`         // Digest algorithm stored in the release.
	MinSteamID   uint64 `json:"min_steamid" yaml:"min_steamid"`     // First SteamID64 in the release.
	MaxSteamID   uint64 `json:"max_steamid" yaml:"max_steamid"`     // Last SteamID64 in the release.
	EntryCount   uint64 `json:"entry_count" yaml:"entry_count"`     // Number of indexed SteamID64 values.
	SegmentCount int    `json:"segment_count" yaml:"segment_count"` // Number of immutable segment files.
}

// builtSegment carries file metadata produced during the final sequential write.
type builtSegment struct {
	SHA256 string // Full-file digest computed without reopening the staged segment.
	Size   uint64 // Number of serialized segment bytes.
}

// progress serializes coarse progress events from concurrent build workers.
type progress struct {
	onProgress func(ProgressEvent) // Optional consumer callback.
	phase      string              // Name reported for this build phase.
	segment    uint32              // Global segment index reported with each milestone.
	segments   int                 // Total release segment count.
	total      uint64              // Total units in the phase.
	chunk      uint64              // Work amount between worker updates.
	processed  atomic.Uint64       // Aggregate completed units.
	percent    uint64              // Last reported one-percent milestone.
	mu         sync.Mutex          // Serializes milestone callbacks.
}

// ValidateProfile fills profile defaults and validates range and codec settings.
func (c *Config) ValidateProfile() error {
	if c.Context == nil {
		c.Context = context.Background()
	}
	if err := c.Range.Validate(); err != nil {
		return err
	}

	if c.PrefixBits == 0 {
		c.PrefixBits = 18
	}
	if c.PrefixBits > limits.MaxPrefixBits {
		return ErrInvalidPrefixBits
	}

	// An explicit zero is valid; only the unset zero receives the profile-derived default.
	if !c.RiceKSet && c.RiceK == 0 {
		c.RiceK = c.PrefixBits - 1
	}
	if c.RiceK > limits.MaxRiceK {
		return ErrInvalidRiceK
	}

	if c.SegmentBits == 0 {
		c.SegmentBits = 28
	}
	if c.SegmentBits > limits.MaxSegmentBits {
		return ErrInvalidSegmentBits
	}

	if _, err := PlanSegments(c.Range, c.SegmentBits); err != nil {
		return err
	}

	jobs, _, _, err := resolveJobs(c.Jobs)
	if err != nil {
		return err
	}
	c.Jobs = jobs

	return nil
}

// resolveJobs clamps the requested total CPU budget and splits it across pipeline stages.
func resolveJobs(requested int) (jobs, hashWorkers, encodeWorkers int, err error) {
	if requested < 0 {
		return 0, 0, 0, ErrInvalidJobs
	}

	runtimeBudget := runtime.GOMAXPROCS(0)
	runtimeBudget = max(1, runtimeBudget)

	jobs = requested
	if jobs == 0 || jobs > runtimeBudget {
		jobs = runtimeBudget
	}
	hashWorkers = max(1, jobs/2)
	encodeWorkers = max(1, jobs-hashWorkers)

	return jobs, hashWorkers, encodeWorkers, nil
}

// Validate fills defaults and validates the complete build configuration.
func (c *Config) Validate() error {
	if c.Algorithm != segment.DayZ && c.Algorithm != segment.Battleye {
		return ErrInvalidAlgorithm
	}
	if err := c.ValidateProfile(); err != nil {
		return err
	}

	if c.Output == "" {
		return ErrOutputRequired
	}

	return nil
}

// buildSegment hashes one segment, encodes its buckets in order, and writes the immutable file.
func buildSegment(c Config) (built builtSegment, err error) {
	if c.OnProgress != nil {
		c.OnProgress(ProgressEvent{
			State:    "started",
			Segment:  c.SegmentIndex,
			Segments: c.SegmentCount,
		})
	}

	defer func() {
		if c.OnProgress != nil {
			state := "completed"
			if err != nil {
				state = "failed"
			}
			c.OnProgress(ProgressEvent{
				State:    state,
				Segment:  c.SegmentIndex,
				Segments: c.SegmentCount,
			})
		}
	}()

	if err := c.Context.Err(); err != nil {
		return builtSegment{}, err
	}

	// Config validation guarantees the inclusive range count is representable.
	n, _ := c.Range.Count()
	buckets := uint32(1) << c.PrefixBits
	_, hashWorkers, encodeWorkers, err := resolveJobs(c.Jobs)
	if err != nil {
		return builtSegment{}, err
	}

	// #nosec G115 - the hash worker count is positive after budget resolution.
	if n < uint64(hashWorkers) {
		// Empty worker partitions add metadata without doing work, so cap them at the entry count.
		// #nosec G115 - this branch proves n is below a positive int value.
		hashWorkers = int(n)
	}

	if err := buildOrderedSegment(c, n, buckets, hashWorkers, encodeWorkers, &built); err != nil {
		return builtSegment{}, err
	}

	return built, nil
}

// algorithmName returns the stable manifest spelling for an algorithm.
func algorithmName(a segment.Algorithm) string {
	if a == segment.DayZ {
		return "dayz"
	}

	return "battleye"
}

// newProgress creates a phase tracker with a per-worker reporting threshold.
func newProgress(
	phase string,
	total uint64,
	hashWorkers int,
	onProgress func(ProgressEvent),
	segment uint32,
	segments int,
) *progress {
	chunk := total / 100 / uint64(hashWorkers) // #nosec G115 -- worker count is positive.
	if phase != "write" && chunk < 65_536 {
		// Keep atomic updates several orders of magnitude below per-ID hashing operations.
		chunk = 65_536
	} else if chunk == 0 {
		chunk = 1
	}

	if onProgress != nil {
		onProgress(ProgressEvent{
			Phase:    phase,
			State:    "phase-started",
			Segment:  segment,
			Segments: segments,
			Total:    total,
		})
	}

	return &progress{
		phase:      phase,
		total:      total,
		chunk:      chunk,
		onProgress: onProgress,
		segment:    segment,
		segments:   segments,
	}
}

// add reports a milestone after workers complete n units.
func (p *progress) add(n uint64) {
	if n == 0 || p.onProgress == nil {
		return
	}

	done := p.processed.Add(n)
	milestone := done * 100 / p.total

	p.mu.Lock()
	defer p.mu.Unlock()

	if milestone > p.percent {
		// Serialize callbacks because worker updates can reach milestones out of order.
		p.percent = milestone
		if p.onProgress != nil {
			p.onProgress(ProgressEvent{
				Phase:     p.phase,
				State:     "progress",
				Segment:   p.segment,
				Segments:  p.segments,
				Processed: done,
				Total:     p.total,
				Percent:   milestone,
			})
		}
	}
}

// bucket hashes an identifier with the selected algorithm and extracts its leading prefix.
func bucket(a segment.Algorithm, id uint64, bits uint8) uint32 {
	if a == segment.DayZ {
		d := digest.DayZ(id)
		b, _ := prefix.Extract(d[:], bits)
		return b
	}

	d := digest.Battleye(id)
	b, _ := prefix.Extract(d[:], bits)
	return b
}
