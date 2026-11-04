// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package resolve maps external identifiers to SteamID64 values.
package resolve

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"

	"github.com/woozymasta/inverz/internal/cache/api"
	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/prefix"
	"github.com/woozymasta/inverz/internal/rice"
	"github.com/woozymasta/inverz/internal/steamid"
)

const (
	// Auto detects identifier type from its encoded length.
	Auto Kind = iota
	// DayZ selects a DayZ Base64URL GUID.
	DayZ
	// Battleye selects a BattlEye hexadecimal GUID.
	Battleye
	// resolverL1Capacity bounds the process-wide verified result cache.
	resolverL1Capacity = 10_000
)

// resolverL1 stores only positive results validated by a persistent cache.
var resolverL1 = newResolverL1()

// Kind identifies an external identifier form.
type Kind uint8

// Query stores a validated raw identifier digest.
type Query struct {
	Raw  []byte // Validated identifier digest bytes.
	Kind Kind   // DayZ or BattlEye identifier form.
}

// Cache is the positive identity cache contract used by Resolver.
type Cache = api.Cache

// Result describes a successful lookup.
type Result struct {
	Source  string // Lookup source, either "cache" or "index".
	SteamID uint64 // Resolved SteamID64 value.
}

// Resolver resolves identifiers against configured immutable indexes.
type Resolver struct {
	DayZSet     *indexset.Set // Index used for DayZ GUID queries.
	BattleyeSet *indexset.Set // Index used for BattlEye GUID queries.
	Cache       Cache         // Optional positive cache whose hits are verified against the digest.
}

// resolverL1Key scopes a digest result to the immutable release root that verified it.
type resolverL1Key struct {
	root   string   // Release directory that validated this result.
	kind   Kind     // Digest algorithm used to resolve the identifier.
	digest [32]byte // Raw digest, with BattlEye occupying the first 16 bytes.
}

// resolverL1Entry stores one bounded positive lookup result.
type resolverL1Entry struct {
	key   resolverL1Key // Cache key for the verified digest.
	id    uint64        // SteamID64 that matched the digest.
	valid bool          // Whether this ring slot contains an entry.
}

// resolverL1Cache is a process-wide FIFO cache for verified positive lookups.
type resolverL1Cache struct {
	byKey   map[resolverL1Key]int               // Maps a digest key to its ring slot.
	entries [resolverL1Capacity]resolverL1Entry // Fixed bound on retained results.
	next    int                                 // Next slot replaced on insertion.
	mu      sync.Mutex                          // Protects ring slots and key lookup.
}

// Parse detects or validates a GUID and converts it to raw digest bytes.
func Parse(input string, override Kind) (Query, error) {
	s := strings.TrimSpace(input)
	kind := override
	if kind == Auto {
		// Encoded lengths distinguish formats before the strict digest parsers validate their contents.
		switch {
		case len(s) == 32:
			kind = Battleye

		case len(s) == 44:
			kind = DayZ

		default:
			return Query{}, ErrInvalidIdentifierFormat
		}
	}

	q := Query{Kind: kind}
	switch kind {
	case DayZ:
		d, err := digest.ParseDayZ(s)
		if err != nil {
			return Query{}, err
		}

		q.Raw = append([]byte(nil), d[:]...)

	case Battleye:
		d, err := digest.ParseBattleye(s)
		if err != nil {
			return Query{}, err
		}

		q.Raw = append([]byte(nil), d[:]...)

	default:
		return Query{}, ErrInvalidIdentifierType
	}

	return q, nil
}

// Resolve checks cache first, then reads and verifies one digest-prefix bucket.
func (r *Resolver) Resolve(ctx context.Context, q Query) (Result, error) {
	if ctx == nil {
		return Result{}, ErrNilResolveContext
	}
	if err := validateQuery(q); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	set := r.DayZSet
	if q.Kind == Battleye {
		set = r.BattleyeSet
	}
	if set == nil {
		return Result{}, ErrIndexUnavailable
	}

	expected := "dayz"
	if q.Kind == Battleye {
		expected = "battleye"
	}
	if set.Manifest.Algorithm != expected {
		return Result{}, ErrIndexAlgorithmMismatch
	}

	minSteamID := steamid.SteamID64(set.Manifest.Segments[0].MinAccountID)
	maxSteamID := steamid.SteamID64(set.Manifest.Segments[len(set.Manifest.Segments)-1].MaxAccountID)
	if r.Cache != nil {
		key := resolverL1Key{root: set.Root, kind: q.Kind}
		copy(key.digest[:], q.Raw)
		if id, ok := resolverL1.get(key); ok && id >= minSteamID && id <= maxSteamID {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}

			return Result{SteamID: id, Source: "cache"}, nil
		}

		// Cache entries are candidates only; recompute the digest before trusting any returned ID.
		cacheKind := api.DayZ
		if q.Kind == Battleye {
			cacheKind = api.Battleye
		}
		matches, err := r.Cache.Lookup(ctx, cacheKind, q.Raw)
		// Cancellation wins over a cache result or error so callers can stop work promptly.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, ctxErr
		}

		// Cache failures are best-effort and fall through to the authoritative segment index.
		if err == nil {
			var verified uint64
			verifiedCount := 0
			for _, id := range matches {
				// Cached answers remain scoped to the active profile's declared coverage.
				if id < minSteamID || id > maxSteamID || !digestMatches(q.Kind, id, q.Raw) {
					continue
				}

				verified = id
				verifiedCount++
			}

			if verifiedCount > 1 {
				return Result{}, ErrAmbiguous
			}
			if verifiedCount == 1 {
				resolverL1.put(key, verified)

				return Result{SteamID: verified, Source: "cache"}, nil
			}
		}
	}

	bucket, err := prefix.Extract(q.Raw, set.Manifest.PrefixBits)
	if err != nil {
		return Result{}, err
	}

	found, err := scanSet(ctx, set, bucket, func(id uint64) bool {
		return digestMatches(q.Kind, id, q.Raw)
	})
	if err != nil {
		return Result{}, err
	}

	// Cache writes are best-effort; a cache outage must not turn a valid index hit into a failure.
	if r.Cache != nil {
		_ = r.Cache.PutMatch(ctx, cacheKindFor(q.Kind), found)
	}

	return Result{SteamID: found, Source: "index"}, nil
}

// newResolverL1 creates a bounded positive-result cache.
func newResolverL1() *resolverL1Cache {
	return &resolverL1Cache{byKey: make(map[resolverL1Key]int, resolverL1Capacity)}
}

// get returns a cached ID without changing FIFO order.
func (c *resolverL1Cache) get(key resolverL1Key) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	slot, ok := c.byKey[key]
	if !ok {
		return 0, false
	}

	return c.entries[slot].id, true
}

// put inserts a verified result and evicts the oldest entry when the ring is full.
func (c *resolverL1Cache) put(key resolverL1Key, id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if slot, ok := c.byKey[key]; ok {
		c.entries[slot].id = id
		return
	}

	entry := &c.entries[c.next]
	if entry.valid {
		delete(c.byKey, entry.key)
	}
	*entry = resolverL1Entry{key: key, id: id, valid: true}
	c.byKey[key] = c.next
	c.next = (c.next + 1) % len(c.entries)
}

// validateQuery checks that the raw digest matches a supported identifier kind.
func validateQuery(q Query) error {
	switch q.Kind {
	case DayZ:
		if len(q.Raw) != 32 {
			return ErrInvalidDayZDigestLength
		}

	case Battleye:
		if len(q.Raw) != 16 {
			return ErrInvalidBattleyeDigestLength
		}

	default:
		return ErrInvalidIdentifierType
	}

	return nil
}

// digestMatches recomputes the selected protocol digest for one cached ID.
func digestMatches(kind Kind, id uint64, raw []byte) bool {
	switch kind {
	case DayZ:
		d := digest.DayZ(id)
		return bytes.Equal(d[:], raw)

	case Battleye:
		d := digest.Battleye(id)
		return bytes.Equal(d[:], raw)

	default:
		return false
	}
}

// cacheKindFor maps resolver kinds to cache lookup kinds.
func cacheKindFor(kind Kind) api.Kind {
	if kind == Battleye {
		return api.Battleye
	}

	return api.DayZ
}

// scanSet checks every segment bucket and rejects malformed or ambiguous candidates.
func scanSet(ctx context.Context, set *indexset.Set, bucket uint32, matchDigest func(uint64) bool) (uint64, error) {
	var found uint64
	matches := 0
	var scanned uint64
	var scratch []byte
	for i, reader := range set.Segments {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		data, err := reader.ReadBucketInto(bucket, scratch)
		if err != nil {
			return 0, err
		}
		scratch = data

		count := binary.LittleEndian.Uint32(data[:4])
		if uint64(count) > reader.Header.EntryCount {
			return 0, ErrBucketCandidateCount
		}

		decoder, err := rice.NewDecoder(data[4:], count, set.Manifest.RiceK)
		if err != nil {
			return 0, fmt.Errorf("decode segment %d bucket: %w", i, err)
		}

		record := set.Manifest.Segments[i]
		var previous uint64
		for n := uint32(0); ; n++ {
			// Check cancellation periodically so a large bucket stays responsive without checking every ID.
			if scanned&255 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}

			gap, ok, err := decoder.Next()
			if err != nil {
				return 0, fmt.Errorf("decode segment %d bucket: %w", i, err)
			}
			if !ok {
				break
			}

			account := uint64(record.MinAccountID) + uint64(gap)
			if n > 0 {
				// Rice values encode gaps, so later account IDs are relative to the preceding candidate.
				account = previous + uint64(gap) + 1
			}
			if account > uint64(record.MaxAccountID) || (n > 0 && account <= previous) {
				return 0, ErrBucketAccountOrder
			}

			id := steamid.SteamIDBase + account
			if matchDigest(id) {
				found = id
				matches++
			}

			previous = account
			scanned++
		}
	}

	if matches > 1 {
		return 0, ErrAmbiguous
	}
	if matches == 0 {
		return 0, ErrNotFound
	}

	return found, nil
}
