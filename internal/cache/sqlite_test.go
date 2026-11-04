// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cache

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/steamid"
)

// errUnexpectedConcurrentLookup marks an unexpected cache result in the concurrency test.
var errUnexpectedConcurrentLookup = errors.New("concurrent cache lookup returned an unexpected result")

// TestSQLitePathEscapesURIReservedCharacters verifies driver options never become part of the filename.
func TestSQLitePathEscapesURIReservedCharacters(t *testing.T) {
	name := "cache with space #100%&.sqlite"
	if runtime.GOOS != "windows" {
		name = "cache with space ?#100%&.sqlite"
	}
	path := filepath.Join(t.TempDir(), name)
	cache, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open database at reserved-character path: %v", err)
	}

	const steamID = steamid.SteamIDBase + 71
	if err := cache.PutMatch(context.Background(), DayZ, steamID); err != nil {
		_ = cache.Close()
		t.Fatalf("write database at reserved-character path: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected database file %q: %v", path, err)
	}

	cache, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen database at reserved-character path: %v", err)
	}
	defer func() { _ = cache.Close() }()

	digest := digest.DayZ(steamID)
	got, err := cache.Lookup(context.Background(), DayZ, digest[:])
	if err != nil || len(got) != 1 || got[0] != steamID {
		t.Fatalf("lookup after reopen = %v, %v; want [%d]", got, err, steamID)
	}
}

// TestSQLiteRejectsInvalidInputs confirms callers can classify cache validation errors.
func TestSQLiteRejectsInvalidInputs(t *testing.T) {
	cache, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cache.Close() }()

	if _, err := cache.Lookup(context.Background(), DayZ, nil); !errors.Is(err, ErrInvalidDigestLength) {
		t.Fatalf("invalid DayZ digest error = %v, want ErrInvalidDigestLength", err)
	}
	if _, err := cache.Lookup(context.Background(), Battleye, nil); !errors.Is(err, ErrInvalidDigestLength) {
		t.Fatalf("invalid BattlEye digest error = %v, want ErrInvalidDigestLength", err)
	}
	if _, err := cache.Lookup(context.Background(), Kind(99), nil); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("invalid lookup kind error = %v, want ErrInvalidKind", err)
	}
	if err := cache.PutMatch(context.Background(), Kind(99), steamid.SteamIDBase); !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("invalid write kind error = %v, want ErrInvalidKind", err)
	}
	if err := cache.PutMatch(context.Background(), DayZ, steamid.ProtocolMaxSteamID+1); !errors.Is(err, ErrInvalidSteamID) {
		t.Fatalf("invalid SteamID64 error = %v, want ErrInvalidSteamID", err)
	}
	if err := cache.PutMatch(context.Background(), DayZ, ^uint64(0)); !errors.Is(err, ErrSQLiteIntegerRange) {
		t.Fatalf("out-of-range SQLite ID error = %v, want ErrSQLiteIntegerRange", err)
	}
	if _, err := cache.db.Exec(`
INSERT INTO identities
    (steam_id, dayz_digest, battleye_digest, dayz_verified, battleye_verified, resolved_at)
VALUES
    (?, ?, ?, 1, 0, 0)`, int64(-1), make([]byte, 32), make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Lookup(context.Background(), DayZ, make([]byte, 32)); !errors.Is(err, ErrInvalidSteamID) {
		t.Fatalf("negative cached SteamID64 error = %v, want ErrInvalidSteamID", err)
	}
}

// TestSQLiteTracksVerifiedDigests ensures one algorithm cannot implicitly warm the other cache key.
func TestSQLiteTracksVerifiedDigests(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".cache-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	c, err := OpenSQLite(filepath.Join(dir, "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = c.Close() })
	id := steamid.SteamIDBase + 345
	ctx := context.Background()
	if err := c.PutMatch(ctx, DayZ, id); err != nil {
		t.Fatal(err)
	}

	dz, be := digest.DayZ(id), digest.Battleye(id)
	ids, err := c.Lookup(ctx, DayZ, dz[:])
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("verified DayZ lookup => %v, %v", ids, err)
	}
	ids, err = c.Lookup(ctx, Battleye, be[:])
	if err != nil || len(ids) != 0 {
		t.Fatalf("unverified BattlEye lookup => %v, %v", ids, err)
	}

	if err := c.PutMatch(ctx, Battleye, id); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		k   Kind   // Digest algorithm being queried.
		raw []byte // Raw digest bytes for that algorithm.
	}{{DayZ, dz[:]}, {Battleye, be[:]}} {
		ids, err := c.Lookup(ctx, tc.k, tc.raw)
		if err != nil || len(ids) != 1 || ids[0] != id {
			t.Fatalf("lookup type %d => %v, %v", tc.k, ids, err)
		}
	}

	if err := c.PutMatch(ctx, DayZ, id); err != nil {
		t.Fatal(err)
	}

	ids, err = c.Lookup(ctx, DayZ, dz[:])
	if err != nil || len(ids) != 1 {
		t.Fatalf("upsert duplicated row: %v %v", ids, err)
	}
}

// TestSQLiteSchemaVersionAndFutureRejection checks version recording and forward-version rejection.
func TestSQLiteSchemaVersionAndFutureRejection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.sqlite")
	cache, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version=%d, want %d", version, schemaVersion)
	}

	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSQLite(path); !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("future schema error = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

// TestSQLiteMigratesUnverifiedLegacyRows ensures a v1 row is not trusted after migration.
func TestSQLiteMigratesUnverifiedLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	id := steamid.SteamIDBase + 99
	dz, be := digest.DayZ(id), digest.Battleye(id)
	if _, err := db.Exec(`
CREATE TABLE identities (
    steam_id INTEGER PRIMARY KEY,
    dayz_digest BLOB NOT NULL,
    battleye_digest BLOB NOT NULL,
    resolved_at INTEGER NOT NULL
) STRICT;
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO identities (steam_id, dayz_digest, battleye_digest, resolved_at)
VALUES (?, ?, ?, 0)`, int64(id), dz[:], be[:]); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cache, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cache.Close() }()
	ids, err := cache.Lookup(context.Background(), DayZ, dz[:])
	if err != nil || len(ids) != 0 {
		t.Fatalf("legacy cache row was trusted after migration: %v, %v", ids, err)
	}
}

// TestSQLiteConcurrentReadsAndWrites exercises serialized access under concurrent callers.
func TestSQLiteConcurrentReadsAndWrites(t *testing.T) {
	cache, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cache.Close() })
	const goroutines = 12
	const iterations = 20
	var wait sync.WaitGroup

	errorsFound := make(chan error, goroutines)
	id := steamid.SteamIDBase + 123
	dz, be := digest.DayZ(id), digest.Battleye(id)

	for range goroutines {
		wait.Go(func() {
			for range iterations {
				if err := cache.PutMatch(context.Background(), DayZ, id); err != nil {
					errorsFound <- err
					return
				}
				if err := cache.PutMatch(context.Background(), Battleye, id); err != nil {
					errorsFound <- err
					return
				}

				for _, query := range []struct {
					kind Kind   // Digest algorithm being queried.
					raw  []byte // Raw digest bytes for that algorithm.
				}{{DayZ, dz[:]}, {Battleye, be[:]}} {
					ids, err := cache.Lookup(context.Background(), query.kind, query.raw)
					if err != nil || len(ids) != 1 || ids[0] != id {
						errorsFound <- errUnexpectedConcurrentLookup
						return
					}
				}
			}
		})
	}

	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

// TestSQLiteSizeLimitAndWALAccounting covers bounded admission, readable hits, and limit recovery.
func TestSQLiteSizeLimitAndWALAccounting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.sqlite")
	if err := os.WriteFile(path, []byte("db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}

	c := &SQLite{path: path}
	c.maxBytes.Store(2048)
	if err := c.refreshSize(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.Stats(); !got.Saturated || got.UsedBytes != 2+4096 {
		t.Fatalf("WAL footprint not included in size sample: %+v", got)
	}
}

// TestSQLiteReadDoesNotResampleSize keeps filesystem stats off the lookup path.
func TestSQLiteReadDoesNotResampleSize(t *testing.T) {
	c, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	lastSample := c.lastCheck
	if _, err := c.Lookup(context.Background(), DayZ, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if !c.lastCheck.Equal(lastSample) {
		t.Fatal("read lookup unexpectedly sampled the cache footprint")
	}
}

// TestSQLiteExistingDatabaseStartsSaturated verifies the admission state is initialized on reopen.
func TestSQLiteExistingDatabaseStartsSaturated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	c, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}

	id := steamid.SteamIDBase + 503
	if err := c.PutMatch(context.Background(), DayZ, id); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c, err = OpenSQLiteWithLimit(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if !c.Stats().Saturated {
		t.Fatal("existing database over limit did not start saturated")
	}

	dz := digest.DayZ(id)
	if got, err := c.Lookup(context.Background(), DayZ, dz[:]); err != nil || len(got) != 1 {
		t.Fatalf("existing hit while saturated = %v, %v", got, err)
	}
	if err := c.PutMatch(context.Background(), Battleye, id+1); err != nil {
		t.Fatalf("saturated write should be skipped: %v", err)
	}
	if got := c.Stats(); got.SkippedWrites != 1 {
		t.Fatalf("skipped writes = %d, want 1", got.SkippedWrites)
	}
	if err := c.SetMaxBytes(1 << 20); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Saturated {
		t.Fatal("cache remained saturated after increasing the limit")
	}
	if err := c.PutMatch(context.Background(), Battleye, id+1); err != nil {
		t.Fatalf("write after raising limit: %v", err)
	}
}
