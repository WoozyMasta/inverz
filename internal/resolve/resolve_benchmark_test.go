// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package resolve

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/woozymasta/inverz/internal/cache"
	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/generate"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// BenchmarkColdLookup measures resolving varied identifiers through the segment index.
func BenchmarkColdLookup(b *testing.B) {
	r, _, cleanup := benchmarkResolver(b, false)
	defer cleanup()

	//nolint:gosec // A fixed seed keeps benchmark queries reproducible.
	random := rand.New(rand.NewSource(1))
	queries := make([]Query, 256)

	for i := range queries {
		id := steamid.SteamIDBase + uint64(random.Uint32()%2_000_000)
		query, err := Parse(digest.FormatDayZ(digest.DayZ(id)), Auto)
		if err != nil {
			b.Fatal(err)
		}
		queries[i] = query
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Resolve(context.Background(), queries[i%len(queries)]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCacheHit measures lookups served by the verified positive cache.
func BenchmarkCacheHit(b *testing.B) {
	r, q, cleanup := benchmarkResolver(b, true)
	defer cleanup()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Resolve(context.Background(), q); err != nil {
			b.Fatal(err)
		}
	}
}

// benchmarkResolver creates the same eight-segment fixture for both benchmarks.
func benchmarkResolver(b *testing.B, warm bool) (*Resolver, Query, func()) {
	dir, err := os.MkdirTemp(".", ".resolve-bench-")
	if err != nil {
		b.Fatal(err)
	}

	indexPath := filepath.Join(dir, "index")
	id := steamid.SteamIDBase + 1_000_000
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 1_999_999},
		PrefixBits:  8,
		RiceK:       7,
		RiceKSet:    true,
		SegmentBits: 18,
		Jobs:        4,
		Output:      indexPath,
	}); err != nil {
		b.Fatal(err)
	}

	idx, err := indexset.Open(indexPath)
	if err != nil {
		b.Fatal(err)
	}
	if len(idx.Segments) != 8 {
		_ = idx.Close()
		b.Fatalf("benchmark fixture has %d segments, want 8", len(idx.Segments))
	}

	q, err := Parse(digest.FormatDayZ(digest.DayZ(id)), Auto)
	if err != nil {
		b.Fatal(err)
	}
	r := &Resolver{DayZSet: idx}
	if warm {
		c, err := cache.OpenSQLite(filepath.Join(dir, "cache.sqlite"))
		if err != nil {
			b.Fatal(err)
		}
		if err := c.PutMatch(context.Background(), cache.DayZ, id); err != nil {
			b.Fatal(err)
		}

		r.Cache = c
		return r, q, func() { _ = c.Close(); _ = idx.Close(); _ = os.RemoveAll(dir) }
	}

	return r, q, func() { _ = idx.Close(); _ = os.RemoveAll(dir) }
}
