// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package resolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/woozymasta/inverz/internal/cache"
	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/generate"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/prefix"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// testCache controls cache errors and candidates for resolver policy tests.
type testCache struct {
	matches     []uint64   // Candidate IDs returned by Lookup.
	lookupErr   error      // Error injected by Lookup.
	putErr      error      // Error injected by PutMatch.
	lookupCalls int        // Number of Lookup calls.
	putCalls    int        // Number of attempted writes.
	putKind     cache.Kind // Kind passed to the latest write.
	putID       uint64     // ID passed to the latest write.
}

// TestResolverL1AvoidsRepeatedPersistentLookup checks that verified cache hits stay in the bounded in-process cache.
func TestResolverL1AvoidsRepeatedPersistentLookup(t *testing.T) {
	root, err := os.MkdirTemp(".", ".resolve-l1-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	base := steamid.SteamIDBase
	indexPath := filepath.Join(root, "dayz")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base, Max: base + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      indexPath,
	}); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()

	id := base + 5
	raw := digest.DayZ(id)
	query, err := Parse(digest.FormatDayZ(raw), Auto)
	if err != nil {
		t.Fatal(err)
	}

	identityCache := &testCache{matches: []uint64{id}}
	resolver := Resolver{DayZSet: set, Cache: identityCache}
	for range 2 {
		result, err := resolver.Resolve(context.Background(), query)
		if err != nil || result.Source != "cache" || result.SteamID != id {
			t.Fatalf("Resolve() = %#v, %v; want verified cache match", result, err)
		}
	}
	if identityCache.lookupCalls != 1 {
		t.Fatalf("persistent Lookup calls = %d, want 1", identityCache.lookupCalls)
	}
}

// TestResolverL1EvictsOldest checks that the positive-result cache never exceeds its fixed capacity.
func TestResolverL1EvictsOldest(t *testing.T) {
	cache := newResolverL1()
	var oldest resolverL1Key
	var newest resolverL1Key
	for i := 0; i <= resolverL1Capacity; i++ {
		key := resolverL1Key{kind: DayZ}
		key.digest[0] = byte(i)
		key.digest[1] = byte(i >> 8)
		cache.put(key, uint64(i))
		if i == 0 {
			oldest = key
		}
		newest = key
	}

	if _, ok := cache.get(oldest); ok {
		t.Fatal("oldest result remains after the cache reached capacity")
	}
	if id, ok := cache.get(newest); !ok || id != resolverL1Capacity {
		t.Fatalf("newest result = %d, %t; want %d, true", id, ok, resolverL1Capacity)
	}
}

// TestExhaustiveRoundTrip checks both identifier formats across a small generated range.
func TestExhaustiveRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".resolve-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	base := steamid.SteamIDBase
	dayzPath := filepath.Join(dir, "dayz")
	bePath := filepath.Join(dir, "be")
	for _, tc := range []struct {
		a    segment.Algorithm // Digest algorithm used to build the fixture.
		path string            // Output directory for that algorithm's index.
	}{{segment.DayZ, dayzPath}, {segment.Battleye, bePath}} {
		if err := generate.Build(generate.Config{
			Algorithm:   tc.a,
			Range:       steamid.Range{Min: base, Max: base + 99},
			PrefixBits:  4,
			RiceK:       3,
			RiceKSet:    true,
			SegmentBits: 5,
			Jobs:        3,
			Output:      tc.path,
		}); err != nil {
			t.Fatal(err)
		}
	}

	dz, err := indexset.Open(dayzPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dz.Close() })
	be, err := indexset.Open(bePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = be.Close() })
	r := Resolver{DayZSet: dz, BattleyeSet: be}
	for id := base; id <= base+99; id++ {
		d := digest.DayZ(id)
		q, err := Parse(digest.FormatDayZ(d), Auto)
		if err != nil {
			t.Fatal(err)
		}

		got, err := r.Resolve(context.Background(), q)
		if err != nil || got.SteamID != id {
			t.Fatalf("DayZ %d => %#v, %v", id, got, err)
		}

		b := digest.Battleye(id)
		q, err = Parse(digest.FormatBattleye(b), Auto)
		if err != nil {
			t.Fatal(err)
		}

		got, err = r.Resolve(context.Background(), q)
		if err != nil || got.SteamID != id {
			t.Fatalf("BattlEye %d => %#v, %v", id, got, err)
		}
	}
}

// TestCacheRequiresVerificationForEachIdentifier checks that cache hits are verified per digest kind.
func TestCacheRequiresVerificationForEachIdentifier(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".resolve-cache-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	base := steamid.SteamIDBase
	indexPath := filepath.Join(dir, "dayz")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base, Max: base + 10},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        2,
		Output:      indexPath,
	}); err != nil {
		t.Fatal(err)
	}

	bePath := filepath.Join(dir, "battleye")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.Battleye,
		Range:       steamid.Range{Min: base, Max: base + 10},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        2,
		Output:      bePath,
	}); err != nil {
		t.Fatal(err)
	}

	idx, err := indexset.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = idx.Close() })
	beIndex, err := indexset.Open(bePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = beIndex.Close() })
	c, err := cache.OpenSQLite(filepath.Join(dir, "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = c.Close() })
	r := Resolver{DayZSet: idx, BattleyeSet: beIndex, Cache: c}
	id := base + 7
	d := digest.DayZ(id)
	q, _ := Parse(digest.FormatDayZ(d), Auto)
	first, err := r.Resolve(context.Background(), q)
	if err != nil || first.Source != "index" {
		t.Fatalf("first lookup: %#v %v", first, err)
	}

	b := digest.Battleye(id)
	q, _ = Parse(digest.FormatBattleye(b), Auto)
	second, err := r.Resolve(context.Background(), q)
	if err != nil || second.Source != "index" || second.SteamID != id {
		t.Fatalf("first BattlEye lookup: %#v %v", second, err)
	}
	third, err := r.Resolve(context.Background(), q)
	if err != nil || third.Source != "cache" || third.SteamID != id {
		t.Fatalf("verified BattlEye cache lookup: %#v %v", third, err)
	}
}

// TestResolverTreatsCacheAsBestEffortAndRevalidatesHits checks fallback and digest verification.
func TestResolverTreatsCacheAsBestEffortAndRevalidatesHits(t *testing.T) {
	root, err := os.MkdirTemp(".", ".resolve-cache-policy-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	base := steamid.SteamIDBase
	indexPath := filepath.Join(root, "dayz")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base, Max: base + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      indexPath,
	}); err != nil {
		t.Fatal(err)
	}
	set, err := indexset.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()

	id := base + 5
	raw := digest.DayZ(id)
	query, err := Parse(digest.FormatDayZ(raw), Auto)
	if err != nil {
		t.Fatal(err)
	}
	identityCache := &testCache{matches: []uint64{base + 7}, putErr: os.ErrPermission}
	resolver := Resolver{DayZSet: set, Cache: identityCache}
	result, err := resolver.Resolve(context.Background(), query)
	if err != nil || result.Source != "index" || result.SteamID != id {
		t.Fatalf("stale cache hit result=%#v error=%v", result, err)
	}
	if identityCache.putCalls != 1 || identityCache.putKind != cache.DayZ || identityCache.putID != id {
		t.Fatalf("cache write was not attempted for verified match: %#v", identityCache)
	}

	outsideID := base + 20
	outsideDigest := digest.DayZ(outsideID)
	outsideQuery, err := Parse(digest.FormatDayZ(outsideDigest), Auto)
	if err != nil {
		t.Fatal(err)
	}
	identityCache.matches = []uint64{outsideID}
	if result, err := resolver.Resolve(context.Background(), outsideQuery); !errors.Is(err, ErrNotFound) ||
		result.Source == "cache" {
		t.Fatalf("out-of-coverage cache result=%#v error=%v", result, err)
	}

	identityCache.lookupErr = os.ErrPermission
	result, err = resolver.Resolve(context.Background(), query)
	if err != nil || result.Source != "index" || result.SteamID != id {
		t.Fatalf("cache read failure blocked index lookup: result=%#v error=%v", result, err)
	}
}

// TestResolverRejectsInvalidQueriesAndHonorsCancellation checks query validation and context handling.
func TestResolverRejectsInvalidQueriesAndHonorsCancellation(t *testing.T) {
	resolver := Resolver{}
	for _, tc := range []struct {
		name  string // Invalid query case.
		query Query  // Query passed to the resolver.
		want  error  // Expected validation sentinel.
	}{
		{name: "automatic kind", query: Query{Kind: Auto, Raw: make([]byte, 32)}, want: ErrInvalidIdentifierType},
		{name: "dayz length", query: Query{Kind: DayZ, Raw: make([]byte, 31)}, want: ErrInvalidDayZDigestLength},
		{name: "battleye length", query: Query{Kind: Battleye, Raw: make([]byte, 32)}, want: ErrInvalidBattleyeDigestLength},
		{name: "unknown kind", query: Query{Kind: Kind(255), Raw: make([]byte, 16)}, want: ErrInvalidIdentifierType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolver.Resolve(context.Background(), tc.query); !errors.Is(err, tc.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, tc.want)
			}
		})
	}

	if _, err := Parse("short", Auto); !errors.Is(err, ErrInvalidIdentifierFormat) {
		t.Fatalf("Parse() error = %v, want ErrInvalidIdentifierFormat", err)
	}
	var nilContext context.Context
	if _, err := resolver.Resolve(nilContext, Query{Kind: DayZ, Raw: make([]byte, 32)}); !errors.Is(err, ErrNilResolveContext) {
		t.Fatalf("Resolve(nil) error = %v, want ErrNilResolveContext", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	query := Query{Kind: DayZ, Raw: make([]byte, 32)}
	if _, err := resolver.Resolve(ctx, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolver error = %v", err)
	}
}

// TestScanSetRejectsInjectedCollisionAcrossSegments checks ambiguity across segment boundaries.
func TestScanSetRejectsInjectedCollisionAcrossSegments(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".resolve-collision-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	base := steamid.SteamIDBase
	indexPath := filepath.Join(dir, "dayz")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base, Max: base + 127},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 5,
		Jobs:        2,
		Output:      indexPath,
	}); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close() })

	bucketSegments := make(map[uint32]map[uint32]struct{})
	for account := uint64(0); account <= 127; account++ {
		d := digest.DayZ(base + account)
		bucket, err := prefix.Extract(d[:], set.Manifest.PrefixBits)
		if err != nil {
			t.Fatal(err)
		}

		segments := bucketSegments[bucket]
		if segments == nil {
			segments = make(map[uint32]struct{})
			bucketSegments[bucket] = segments
		}

		segments[uint32(account>>5)] = struct{}{}
	}

	var collisionBucket uint32
	foundCollision := false
	for bucket, segments := range bucketSegments {
		if len(segments) > 1 {
			collisionBucket = bucket
			foundCollision = true
			break
		}
	}

	if !foundCollision {
		t.Fatal("fixture does not place one bucket in multiple segments")
	}

	_, err = scanSet(context.Background(), set, collisionBucket, func(uint64) bool { return true })
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("scanSet error = %v, want %v", err, ErrAmbiguous)
	}
}

// FuzzParseIdentifier checks arbitrary bounded input without panicking or overreading.
func FuzzParseIdentifier(f *testing.F) {
	f.Add("JCNShusG41ALBHLwqmAu5wINMUMVGiGONjUqccYdh4I=", uint8(Auto))
	f.Add("00000000000000000000000000000000", uint8(Auto))
	f.Fuzz(func(t *testing.T, input string, rawKind uint8) {
		if len(input) > 1024 {
			t.Skip()
		}
		_, _ = Parse(input, Kind(rawKind%4))
	})
}

// Lookup returns the configured cache result.
func (c *testCache) Lookup(context.Context, cache.Kind, []byte) ([]uint64, error) {
	c.lookupCalls++

	return c.matches, c.lookupErr
}

// PutMatch records the algorithm-specific cache write attempt.
func (c *testCache) PutMatch(_ context.Context, kind cache.Kind, id uint64) error {
	c.putCalls++
	c.putKind = kind
	c.putID = id
	return c.putErr
}

// Close satisfies the cache lifecycle contract.
func (*testCache) Close() error {
	return nil
}
