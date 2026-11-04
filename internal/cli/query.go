// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/woozymasta/inverz"
	"github.com/woozymasta/inverz/internal/cache"
)

// queryInput preserves the original spelling and parsed digest for ordered batch output.
type queryInput struct {
	Input string
}

// queryResult represents one success or logical failure in the JSON response.
type queryResult struct {
	Input   string  `json:"input"`
	SteamID *uint64 `json:"steam_id,omitempty"`
	Source  string  `json:"source,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// query opens exactly one local index and processes all values in input order.
func (a *App) query(algorithm inverz.Algorithm, options ProfileOptions, format string, noCache bool, inputs []queryInput) error {
	p, err := options.resolve()
	if err != nil {
		return err
	}

	indexRoot, cachePath := a.dataPaths()
	var optionsList []inverz.OpenOption
	var cacheDB *cache.SQLite
	if !noCache {
		maxBytes := cache.DefaultMaxBytes
		if a.CacheMaxSize > 0 && a.CacheMaxSize <= ByteSize(maxCacheBytes) {
			maxBytes = int64(a.CacheMaxSize)
		}
		if err := ensureDataDirectory(a.DataDir); err != nil {
			a.warnCache("cache is unavailable; resolving identifiers through the index")
		} else {
			cacheDB, err = cache.OpenSQLiteWithLimit(cachePath, maxBytes)
			if err != nil {
				a.warnCache("cache is unavailable; resolving identifiers through the index")
			} else {
				defer func() { _ = cacheDB.Close() }()
				optionsList = append(optionsList, inverz.WithCache(sqliteCacheAdapter{cache: cacheDB}))
			}
		}
	}

	index, err := inverz.Open(indexRoot, algorithm, p, optionsList...)
	if err != nil {
		return profileMissingError(algorithm, p, err)
	}
	defer func() { _ = index.Close() }()

	results := make([]queryResult, len(inputs))
	failed := false
	for i, input := range inputs {
		result, err := index.Resolve(a.ctx, input.Input)
		if err != nil {
			if errors.Is(err, inverz.ErrIdentifierNotFound) || errors.Is(err, inverz.ErrAmbiguousIdentifier) {
				failed = true
				results[i] = queryResult{Input: input.Input, Error: err.Error()}
				_, _ = fmt.Fprintf(a.stderr, "query input %d (%q): %v\n", i+1, input.Input, err)
				continue
			}

			return fmt.Errorf("query input %d (%q): %w", i+1, input.Input, err)
		}

		id := result.SteamID
		results[i] = queryResult{Input: input.Input, SteamID: &id, Source: result.Source}
	}

	if err := writeQueryResults(a.stdout, format, results); err != nil {
		return err
	}

	if cacheDB != nil && cacheDB.Stats().Saturated {
		a.warnCache(fmt.Sprintf(
			"cache size limit reached (%s); new entries are disabled, existing entries remain usable",
			formatCacheLimit(cacheDB.Stats().MaxBytes)))
	}
	if failed {
		return partialFailure()
	}

	return nil
}

// warnCache emits at most one cache diagnostic during this application run.
func (a *App) warnCache(message string) {
	a.cacheWarning.Do(func() {
		_, _ = fmt.Fprintf(a.stderr, "warning: %s\n", message)
	})
}

type sqliteCacheAdapter struct {
	cache *cache.SQLite // CLI-owned SQLite cache exposed through the public cache contract.
}

// Lookup delegates a public algorithm lookup to its verified SQLite digest index.
func (a sqliteCacheAdapter) Lookup(ctx context.Context, algorithm inverz.Algorithm, raw []byte) ([]uint64, error) {
	kind, err := cacheKind(algorithm)
	if err != nil {
		return nil, err
	}

	return a.cache.Lookup(ctx, kind, raw)
}

// Store records an identity only for the algorithm verified by the index scan.
func (a sqliteCacheAdapter) Store(ctx context.Context, algorithm inverz.Algorithm, steamID uint64) error {
	kind, err := cacheKind(algorithm)
	if err != nil {
		return err
	}

	return a.cache.PutMatch(ctx, kind, steamID)
}

func cacheKind(algorithm inverz.Algorithm) (cache.Kind, error) {
	switch algorithm {
	case inverz.AlgorithmDayZ:
		return cache.DayZ, nil

	case inverz.AlgorithmBattlEye:
		return cache.Battleye, nil

	default:
		return 0, fmt.Errorf("unsupported cache algorithm %d", algorithm)
	}
}
