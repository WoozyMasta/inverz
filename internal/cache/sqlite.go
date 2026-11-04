// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cache

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// modernc.org/sqlite registers the CGO-free SQLite driver with database/sql.
	_ "modernc.org/sqlite"

	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/steamid"
)

// schemaVersion is the latest SQLite cache schema supported by this package.
const schemaVersion = 2

const (
	// DefaultMaxBytes bounds the on-disk cache while preserving existing entries.
	DefaultMaxBytes int64 = 512 << 20
	// cacheSampleWrites amortizes filesystem size checks over successful writes.
	cacheSampleWrites = 256
	// cacheSampleInterval bounds stale size information while writes continue.
	cacheSampleInterval = 30 * time.Second
	// cacheResumePercent is the low-water threshold for leaving saturated mode.
	cacheResumePercent = 90
)

// Compile-time check that the SQLite implementation satisfies the cache contract.
var _ Cache = (*SQLite)(nil)

// SQLite is a CGO-free SQLite-backed positive cache.
type SQLite struct {
	lastCheck     time.Time     // Time of the most recent physical-size sample.
	db            *sql.DB       // Serialized SQLite connection pool.
	path          string        // Database path used to measure the main file and WAL.
	maxBytes      atomic.Int64  // Configured maximum physical footprint.
	usedBytes     atomic.Int64  // Latest sampled database and WAL footprint.
	hits          atomic.Uint64 // Successful lookups that returned cached candidates.
	misses        atomic.Uint64 // Successful cache lookups without candidates.
	writes        atomic.Uint64 // Successfully committed cache writes.
	skipped       atomic.Uint64 // Writes skipped because the cache is saturated.
	sampledWrites uint64        // Successful write count at the last size sample.
	writeMu       sync.Mutex    // Serializes admission, writes, and footprint samples.
	saturated     atomic.Bool   // Whether new cache writes are currently disabled.
}

// Stats reports approximate disk usage and process-local cache activity.
type Stats struct {
	UsedBytes     int64  // Approximate database plus WAL size at the last sample.
	MaxBytes      int64  // Configured physical-size admission limit.
	Saturated     bool   // Whether cache writes are currently skipped.
	Hits          uint64 // Successful lookups that returned candidate IDs.
	Misses        uint64 // Successful lookups that returned no candidate IDs.
	Writes        uint64 // Successfully committed cache writes.
	SkippedWrites uint64 // Writes skipped while the cache was saturated.
}

// OpenSQLite opens or creates a cache database and applies its schema.
func OpenSQLite(path string) (*SQLite, error) {
	return OpenSQLiteWithLimit(path, DefaultMaxBytes)
}

// OpenSQLiteWithLimit opens a cache with a physical-footprint admission limit.
func OpenSQLiteWithLimit(path string, maxBytes int64) (*SQLite, error) {
	if maxBytes <= 0 {
		return nil, ErrInvalidMaxBytes
	}

	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite cache path: %w", err)
	}

	db, err := sql.Open(
		"sqlite",
		sqliteDSN(resolvedPath),
	)
	if err != nil {
		return nil, fmt.Errorf("open SQLite cache: %w", err)
	}

	// One connection serializes SQLite access and keeps connection-local pragmas stable.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	var version int
	err = db.QueryRow("PRAGMA user_version").Scan(&version)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read SQLite cache schema version: %w", err)
	}

	if version > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf(
			"SQLite cache schema version %d is newer than supported version %d: %w",
			version,
			schemaVersion,
			ErrUnsupportedSchemaVersion,
		)
	}

	switch version {
	case 0:
		// Create the schema and publish its version atomically for a new database.
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("begin SQLite cache schema transaction: %w", err)
		}

		statements := []string{
			`CREATE TABLE IF NOT EXISTS identities (
        steam_id        INTEGER PRIMARY KEY,
        dayz_digest     BLOB NOT NULL CHECK(length(dayz_digest)    =32),
        battleye_digest BLOB NOT NULL CHECK(length(battleye_digest)=16),
        dayz_verified   INTEGER NOT NULL DEFAULT 0 CHECK(dayz_verified IN (0, 1)),
        battleye_verified INTEGER NOT NULL DEFAULT 0 CHECK(battleye_verified IN (0, 1)),
        resolved_at     INTEGER NOT NULL
    ) STRICT`,
			`CREATE INDEX IF NOT EXISTS identities_dayz_digest ON identities(dayz_digest)`,
			`CREATE INDEX IF NOT EXISTS identities_battleye_digest ON identities(battleye_digest)`,
			"PRAGMA user_version = 2",
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				_ = tx.Rollback()
				_ = db.Close()
				return nil, fmt.Errorf("initialize SQLite cache schema: %w", err)
			}
		}

		if err := tx.Commit(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("commit SQLite cache schema: %w", err)
		}

	case 1:
		// Legacy rows default to unverified so migration does not trust cache-only identifiers.
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("begin SQLite cache migration: %w", err)
		}

		statements := []string{
			"ALTER TABLE identities ADD COLUMN dayz_verified INTEGER NOT NULL DEFAULT 0 CHECK(dayz_verified IN (0, 1))",
			"ALTER TABLE identities ADD COLUMN battleye_verified INTEGER NOT NULL DEFAULT 0 CHECK(battleye_verified IN (0, 1))",
			"PRAGMA user_version = 2",
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement); err != nil {
				_ = tx.Rollback()
				_ = db.Close()
				return nil, fmt.Errorf("migrate SQLite cache schema: %w", err)
			}
		}

		if err := tx.Commit(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("commit SQLite cache migration: %w", err)
		}

	case schemaVersion:
	default:
		_ = db.Close()
		return nil, fmt.Errorf("unsupported SQLite cache schema version %d: %w", version, ErrUnsupportedSchemaVersion)
	}

	if _, err := db.Exec(`
SELECT
  steam_id,dayz_digest,battleye_digest,dayz_verified,battleye_verified,resolved_at
FROM
  identities
LIMIT 0
`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("validate SQLite cache schema: %w", err)
	}

	s := &SQLite{db: db, path: resolvedPath}
	s.maxBytes.Store(maxBytes)
	if err := s.refreshSize(time.Now()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("measure SQLite cache size: %w", err)
	}
	return s, nil
}

// sqliteDSN encodes the filesystem path before appending driver query parameters.
func sqliteDSN(path string) string {
	path = filepath.ToSlash(path)
	if volume := filepath.VolumeName(path); volume != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
	}).String()
}

// Stats returns the latest sampled footprint and current admission state.
func (s *SQLite) Stats() Stats {
	return Stats{
		UsedBytes:     s.usedBytes.Load(),
		MaxBytes:      s.maxBytes.Load(),
		Saturated:     s.saturated.Load(),
		Hits:          s.hits.Load(),
		Misses:        s.misses.Load(),
		Writes:        s.writes.Load(),
		SkippedWrites: s.skipped.Load(),
	}
}

// SetMaxBytes changes the write-admission limit and refreshes the footprint immediately.
func (s *SQLite) SetMaxBytes(maxBytes int64) error {
	if maxBytes <= 0 {
		return ErrInvalidMaxBytes
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.maxBytes.Store(maxBytes)
	return s.refreshSize(time.Now())
}

// refreshSize samples the main database and WAL, applying hysteresis to admission state.
func (s *SQLite) refreshSize(now time.Time) error {
	var total int64
	for _, path := range []string{s.path, s.path + "-wal"} {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return fmt.Errorf("stat %q: %w", path, err)
		}

		size := info.Size()
		if size > math.MaxInt64-total {
			total = math.MaxInt64
		} else {
			total += size
		}
	}

	s.usedBytes.Store(total)
	maxBytes := s.maxBytes.Load()
	if s.saturated.Load() {
		resumeAt := maxBytes/100*cacheResumePercent + maxBytes%100*cacheResumePercent/100
		if total <= resumeAt {
			s.saturated.Store(false)
		}
	} else if total >= maxBytes {
		s.saturated.Store(true)
	}

	s.lastCheck = now
	s.sampledWrites = s.writes.Load()

	return nil
}

// maybeRefreshSize amortizes filesystem stats across successful writes.
func (s *SQLite) maybeRefreshSize(now time.Time) error {
	writes := s.writes.Load()
	if writes-s.sampledWrites < cacheSampleWrites && now.Sub(s.lastCheck) < cacheSampleInterval {
		return nil
	}

	return s.refreshSize(now)
}

// Lookup returns at most two verified matches to preserve ambiguity detection.
func (s *SQLite) Lookup(ctx context.Context, kind Kind, raw []byte) ([]uint64, error) {
	var query string

	switch kind {
	case DayZ:
		if len(raw) != 32 {
			return nil, fmt.Errorf("%w: DayZ", ErrInvalidDigestLength)
		}
		query = "SELECT steam_id FROM identities WHERE dayz_digest = ? AND dayz_verified = 1 LIMIT 2"

	case Battleye:
		if len(raw) != 16 {
			return nil, fmt.Errorf("%w: BattlEye", ErrInvalidDigestLength)
		}
		query = "SELECT steam_id FROM identities WHERE battleye_digest = ? AND battleye_verified = 1 LIMIT 2"

	default:
		return nil, ErrInvalidKind
	}

	rows, err := s.db.QueryContext(ctx, query, raw)
	if err != nil {
		return nil, fmt.Errorf("query SQLite cache: %w", err)
	}

	defer func() { _ = rows.Close() }()
	matches := make([]uint64, 0, 1)

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan SQLite cache result: %w", err)
		}

		if id < 0 {
			return nil, fmt.Errorf("%w: cache contains a negative ID", ErrInvalidSteamID)
		}

		if _, err := steamid.AccountID(uint64(id)); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidSteamID, err)
		}

		matches = append(matches, uint64(id))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite cache results: %w", err)
	}

	if len(matches) > 0 {
		s.hits.Add(1)
	} else {
		s.misses.Add(1)
	}

	return matches, nil
}

// PutMatch records an identity verified by one complete index lookup.
func (s *SQLite) PutMatch(ctx context.Context, kind Kind, id uint64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.saturated.Load() {
		s.skipped.Add(1)
		return nil
	}

	// Check SQLite's signed representation before protocol validation to keep the SQL cast guarded.
	if id > math.MaxInt64 {
		return ErrSQLiteIntegerRange
	}
	if _, err := steamid.AccountID(id); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSteamID, err)
	}
	if kind != DayZ && kind != Battleye {
		return ErrInvalidKind
	}

	dz, be := digest.DayZ(id), digest.Battleye(id)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite cache update: %w", err)
	}
	// Rollback is harmless after commit and ensures failed writes release the transaction.
	defer func() { _ = tx.Rollback() }()

	dayzVerified, battleyeVerified := 0, 0
	if kind == DayZ {
		dayzVerified = 1
	} else {
		battleyeVerified = 1
	}
	// Only this query's algorithm is newly verified; the upsert preserves any earlier verification.
	_, err = tx.ExecContext(ctx, `
INSERT INTO identities
    (steam_id, dayz_digest, battleye_digest, dayz_verified, battleye_verified, resolved_at)
VALUES
    (?, ?, ?, ?, ?, ?)
    ON CONFLICT (steam_id)
    DO
UPDATE
SET
    dayz_digest    =excluded.dayz_digest,
    battleye_digest=excluded.battleye_digest,
    dayz_verified  =MAX(identities.dayz_verified, excluded.dayz_verified),
    battleye_verified=MAX(identities.battleye_verified, excluded.battleye_verified),
	    resolved_at    =excluded.resolved_at`, int64(id), dz[:], be[:], dayzVerified, battleyeVerified, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("write SQLite cache entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite cache update: %w", err)
	}

	s.writes.Add(1)
	if err := s.maybeRefreshSize(time.Now()); err != nil {
		return fmt.Errorf("refresh SQLite cache size: %w", err)
	}

	return nil
}

// Close closes the database.
func (s *SQLite) Close() error {
	return s.db.Close()
}
