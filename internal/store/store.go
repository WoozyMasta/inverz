// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package store manages immutable local index-set releases and activation.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/profile"
	"github.com/woozymasta/inverz/internal/releaselock"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// Store resolves deterministic profile paths below one local root.
type Store struct {
	Root string // Filesystem root containing algorithm and profile directories.
}

// RemoteState stores mutable remote selection metadata outside release.json.
type RemoteState struct {
	Reference      string `json:"reference"`              // Credential-free registry reference.
	ManifestDigest string `json:"manifest_digest"`        // Canonical digest of the selected manifest.
	LastChecked    string `json:"last_checked,omitempty"` // Last remote check time in RFC3339 format.
	LastUpdated    string `json:"last_updated,omitempty"` // Last successful update time in RFC3339 format.
}

// LocalRelease is a bounded metadata summary for one installed profile.
type LocalRelease struct {
	Remote         *RemoteState `json:"remote,omitempty"          yaml:"remote,omitempty"`          // Last known remote selection.
	Algorithm      string       `json:"algorithm"                 yaml:"algorithm"`                 // Canonical digest algorithm.
	Profile        string       `json:"profile"                   yaml:"profile"`                   // Canonical profile key or invalid directory name.
	MinSteamID     string       `json:"min_steamid,omitempty"     yaml:"min_steamid,omitempty"`     // Inclusive first SteamID64.
	MaxSteamID     string       `json:"max_steamid,omitempty"     yaml:"max_steamid,omitempty"`     // Inclusive last SteamID64.
	EntryCount     string       `json:"entry_count,omitempty"     yaml:"entry_count,omitempty"`     // Number of indexed identifiers.
	ManifestSHA256 string       `json:"manifest_sha256,omitempty" yaml:"manifest_sha256,omitempty"` // Digest of canonical release metadata.
	State          string       `json:"state"                     yaml:"state"`                     // ready or invalid.
	Error          string       `json:"error,omitempty"           yaml:"error,omitempty"`           // Bounded metadata error for invalid entries.
	SegmentCount   int          `json:"segment_count,omitempty"   yaml:"segment_count,omitempty"`   // Number of segment files.
	Size           uint64       `json:"size,omitempty"            yaml:"size,omitempty"`            // Aggregate segment size from manifest metadata.
}

// activationHooks provides controlled failure injection for activation tests.
type activationHooks struct {
	beforeManifest func() error // Runs after segment installation and before manifest replacement.
}

// New creates a local store rooted at path.
func New(path string) (*Store, error) {
	if path == "" {
		return nil, ErrIndexRootRequired
	}

	return &Store{Root: path}, nil
}

// ReleasePath returns the canonical directory for a manifest profile.
func (s *Store) ReleasePath(manifest indexset.Manifest) (string, error) {
	if err := manifest.Validate(); err != nil {
		return "", err
	}

	return s.ProfilePath(manifest.Algorithm, profile.Profile{
		Version:     manifest.IndexFormatVersion,
		PrefixBits:  manifest.PrefixBits,
		RiceK:       manifest.RiceK,
		SegmentBits: manifest.SegmentBits,
	})
}

// ProfilePath returns the canonical directory for one algorithm/profile pair.
func (s *Store) ProfilePath(algorithm string, p profile.Profile) (string, error) {
	if err := validateAlgorithm(algorithm); err != nil {
		return "", err
	}
	if err := p.Validate(); err != nil {
		return "", err
	}

	return filepath.Join(s.Root, algorithm, p.String()), nil
}

// Open opens the selected algorithm/profile from the store root.
func (s *Store) Open(algorithm string, prefixBits, riceK, segmentBits uint8) (*indexset.Set, error) {
	return s.OpenProfile(algorithm, profile.Profile{
		Version:     1,
		PrefixBits:  prefixBits,
		RiceK:       riceK,
		SegmentBits: segmentBits,
	})
}

// OpenProfile opens the selected canonical algorithm/profile release.
func (s *Store) OpenProfile(algorithm string, p profile.Profile) (*indexset.Set, error) {
	path, err := s.ProfilePath(algorithm, p)
	if err != nil {
		return nil, err
	}

	return indexset.Open(path)
}

// List returns release metadata without hashing or reading segment payloads.
func (s *Store) List() ([]LocalRelease, error) {
	rootInfo, err := os.Lstat(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return []LocalRelease{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("stat index store: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrIndexRootNotDirectory
	}

	var releases []LocalRelease
	for _, algorithm := range []string{"dayz", "battleye"} {
		algorithmDir := filepath.Join(s.Root, algorithm)
		algorithmInfo, err := os.Lstat(algorithmDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s profiles: %w", algorithm, err)
		}

		if !algorithmInfo.IsDir() || algorithmInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}

		entries, err := os.ReadDir(algorithmDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s profiles: %w", algorithm, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			release := LocalRelease{Algorithm: algorithm, Profile: entry.Name(), State: "invalid"}
			path := filepath.Join(algorithmDir, entry.Name())
			info, err := os.Lstat(path)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				release.Error = "profile path is not a real directory"
				releases = append(releases, release)
				continue
			}

			p, err := profile.Parse(entry.Name())
			if err != nil {
				release.Error = err.Error()
				releases = append(releases, release)
				continue
			}

			manifestPath := filepath.Join(path, indexset.ManifestName)
			manifest, data, err := readManifestWithData(manifestPath)
			if err != nil {
				release.Error = err.Error()
				releases = append(releases, release)
				continue
			}

			if manifest.Algorithm != algorithm ||
				manifest.IndexFormatVersion != p.Version ||
				manifest.PrefixBits != p.PrefixBits ||
				manifest.RiceK != p.RiceK || manifest.SegmentBits != p.SegmentBits {
				release.Error = ErrProfileManifestMismatch.Error()
				releases = append(releases, release)
				continue
			}

			if err := validateListedSegments(path, manifest); err != nil {
				release.Error = err.Error()
				releases = append(releases, release)
				continue
			}

			release.State = "ready"
			release.MinSteamID = manifest.MinSteamID
			release.MaxSteamID = manifest.MaxSteamID
			release.EntryCount = manifest.EntryCount
			release.SegmentCount = len(manifest.Segments)
			release.ManifestSHA256 = hashBytes(data)
			for _, segment := range manifest.Segments {
				release.Size += segment.Size
			}
			if remote, err := s.ReadRemoteState(manifest); err == nil {
				release.Remote = &remote
			}
			releases = append(releases, release)
		}
	}

	sort.Slice(releases, func(i, j int) bool {
		left, right := releases[i], releases[j]
		if left.Algorithm != right.Algorithm {
			return left.Algorithm < right.Algorithm
		}

		leftProfile, leftErr := profile.Parse(left.Profile)
		rightProfile, rightErr := profile.Parse(right.Profile)
		if leftErr != nil || rightErr != nil {
			return left.Profile < right.Profile
		}

		if leftProfile.Version != rightProfile.Version {
			return leftProfile.Version < rightProfile.Version
		}

		if leftProfile.PrefixBits != rightProfile.PrefixBits {
			return leftProfile.PrefixBits < rightProfile.PrefixBits
		}

		if leftProfile.RiceK != rightProfile.RiceK {
			return leftProfile.RiceK < rightProfile.RiceK
		}

		return leftProfile.SegmentBits < rightProfile.SegmentBits
	})

	return releases, nil
}

// Remove deletes exactly one canonical local index profile, never the shared cache.
func (s *Store) Remove(algorithm string, p profile.Profile) error {
	path, err := s.ProfilePath(algorithm, p)
	if err != nil {
		return err
	}
	if err := ensureRealDirectory(s.Root); err != nil {
		return err
	}

	algorithmDir := filepath.Join(s.Root, algorithm)
	if err := ensureRealDirectory(algorithmDir); err != nil {
		return err
	}

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrProfileNotFound
	}
	if err != nil {
		return fmt.Errorf("stat profile directory: %w", err)
	}

	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrProfileDirectoryInvalid
	}

	lock, err := releaselock.Acquire(path)
	if err != nil {
		return err
	}

	removeErr := os.RemoveAll(path)
	lockErr := lock.Close()
	if err := errors.Join(removeErr, lockErr); err != nil {
		return fmt.Errorf("remove profile directory: %w", err)
	}

	return nil
}

// ensureRealDirectory rejects missing, non-directory and symlinked store paths.
func ensureRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrProfileNotFound
	}
	if err != nil {
		return err
	}

	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrProfileDirectoryInvalid
	}

	return nil
}

// readManifestWithData reads a bounded regular manifest and returns its validated metadata.
func readManifestWithData(path string) (indexset.Manifest, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return indexset.Manifest{}, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limits.MaxManifestBytes {
		return indexset.Manifest{}, nil, indexset.ErrManifestSize
	}

	file, err := os.Open(path)
	if err != nil {
		return indexset.Manifest{}, nil, err
	}

	data, readErr := io.ReadAll(io.LimitReader(file, limits.MaxManifestBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return indexset.Manifest{}, nil, readErr
	}
	if closeErr != nil {
		return indexset.Manifest{}, nil, closeErr
	}
	if len(data) > limits.MaxManifestBytes {
		return indexset.Manifest{}, nil, indexset.ErrManifestSize
	}

	manifest, err := indexset.Parse(data)
	if err != nil {
		return indexset.Manifest{}, nil, err
	}

	return manifest, data, nil
}

// hashBytes returns the lowercase SHA-256 digest of bounded manifest bytes.
func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// validateAlgorithm accepts only canonical persistent algorithm names.
func validateAlgorithm(algorithm string) error {
	if algorithm != "dayz" && algorithm != "battleye" {
		return ErrUnsupportedAlgorithm
	}

	return nil
}

// Activate installs verified segments and atomically replaces release.json last.
func (s *Store) Activate(manifest indexset.Manifest, staged map[string]string) error {
	releaseDir, err := s.ReleasePath(manifest)
	if err != nil {
		return err
	}

	lock, err := releaselock.Acquire(releaseDir)
	if err != nil {
		return err
	}

	activateErr := s.activate(manifest, staged, nil)
	return errors.Join(activateErr, lock.Close())
}

// WriteRemoteState atomically stores remote metadata without credential fields.
func (s *Store) WriteRemoteState(manifest indexset.Manifest, state RemoteState) (writeErr error) {
	releaseDir, err := s.ReleasePath(manifest)
	if err != nil {
		return err
	}
	if err := state.Validate(); err != nil {
		return err
	}

	lock, err := releaselock.Acquire(releaseDir)
	if err != nil {
		return err
	}
	defer func() { writeErr = errors.Join(writeErr, lock.Close()) }()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')
	tmp, err := os.CreateTemp(releaseDir, ".remote-state-*.stage")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, filepath.Join(releaseDir, "remote-state.json"))
}

// ReadRemoteState loads bounded metadata associated with one release profile.
func (s *Store) ReadRemoteState(manifest indexset.Manifest) (RemoteState, error) {
	releaseDir, err := s.ReleasePath(manifest)
	if err != nil {
		return RemoteState{}, err
	}

	path := filepath.Join(releaseDir, "remote-state.json")
	info, err := os.Lstat(path)
	if err != nil {
		return RemoteState{}, err
	}

	if !info.Mode().IsRegular() ||
		info.Size() <= 0 ||
		info.Size() > limits.MaxRemoteStateBytes {
		return RemoteState{}, ErrRemoteStateFileInvalid
	}

	file, err := os.Open(path)
	if err != nil {
		return RemoteState{}, err
	}

	data, readErr := io.ReadAll(io.LimitReader(file, limits.MaxRemoteStateBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return RemoteState{}, readErr
	}

	if closeErr != nil {
		return RemoteState{}, closeErr
	}
	if len(data) == 0 || int64(len(data)) > limits.MaxRemoteStateBytes {
		return RemoteState{}, ErrRemoteStateFileInvalid
	}

	var state RemoteState
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return RemoteState{}, fmt.Errorf("%w: decode: %w", ErrRemoteStateInvalid, err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RemoteState{}, fmt.Errorf("%w: %w", ErrRemoteStateInvalid, ErrRemoteStateTrailingData)
	}

	if err := state.Validate(); err != nil {
		return RemoteState{}, fmt.Errorf("%w: %w", ErrRemoteStateInvalid, err)
	}

	return state, nil
}

// Validate rejects credential-like reference strings and malformed digests.
func (state RemoteState) Validate() error {
	if state.Reference == "" ||
		!strings.Contains(state.Reference, "/") ||
		strings.Contains(state.Reference, "://") ||
		strings.ContainsAny(state.Reference, "\\?#") ||
		strings.IndexFunc(state.Reference, unicode.IsSpace) >= 0 {
		return ErrRemoteReferenceInvalid
	}

	if strings.Count(state.Reference, "@") > 1 {
		return ErrRemoteReferenceInvalid
	}
	if index := strings.IndexByte(state.Reference, '@'); index >= 0 {
		// Registry references use @ only for an immutable digest; accepting other
		// suffixes could make later remote selection depend on mutable tags.
		if err := validateSHA256(state.Reference[index+1:]); err != nil {
			return ErrRemoteReferenceInvalid
		}
	}

	if !strings.HasPrefix(state.ManifestDigest, "sha256:") {
		return ErrRemoteManifestDigestInvalid
	}
	if err := validateSHA256(state.ManifestDigest); err != nil {
		return ErrRemoteManifestDigestInvalid
	}

	for _, timestamp := range []string{state.LastChecked, state.LastUpdated} {
		if timestamp != "" {
			if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
				return ErrRemoteTimestampInvalid
			}
		}
	}

	return nil
}

// activate installs verified segments and replaces the manifest only after all succeed.
func (s *Store) activate(manifest indexset.Manifest, staged map[string]string, hooks *activationHooks) error {
	if err := manifest.Validate(); err != nil {
		return err
	}

	releaseDir, err := s.ReleasePath(manifest)
	if err != nil {
		return err
	}

	segmentDir := filepath.Join(releaseDir, "segments")
	if err := os.MkdirAll(segmentDir, 0700); err != nil {
		return fmt.Errorf("create segment directory: %w", err)
	}

	active, openErr := indexset.Open(releaseDir)
	var previousManifest *indexset.Manifest
	previousSegmentsOpened := false
	if openErr == nil {
		manifest := active.Manifest
		previousManifest = &manifest
		previousSegmentsOpened = true

		// Release readers hold segment handles; close them before a possible repair
		// rename because Windows cannot replace a file while it is open.
		if err := active.Close(); err != nil {
			return fmt.Errorf("close active release before activation: %w", err)
		}
	} else {
		// A damaged active index may still have a valid manifest that enforces
		// immutable segment records during repair.
		manifest, err := readManifest(releaseDir)
		if err == nil {
			previousManifest = &manifest
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read active release manifest after open failure (%v): %w", openErr, err)
		}
	}

	if previousManifest != nil {
		oldMin := steamid.SteamID64(previousManifest.Segments[0].MinAccountID)
		oldMax := steamid.SteamID64(previousManifest.Segments[len(previousManifest.Segments)-1].MaxAccountID)
		newMin := steamid.SteamID64(manifest.Segments[0].MinAccountID)
		newMax := steamid.SteamID64(manifest.Segments[len(manifest.Segments)-1].MaxAccountID)
		if newMin > oldMin || newMax < oldMax {
			return ErrCoverageRegression
		}

		for _, old := range previousManifest.Segments {
			for _, next := range manifest.Segments {
				// Segment identity is its index and account interval; changing its
				// digest would make an existing release mutable.
				if old.Index == next.Index &&
					old.MinAccountID == next.MinAccountID &&
					old.MaxAccountID == next.MaxAccountID &&
					old.SHA256 != next.SHA256 {
					return fmt.Errorf("immutable segment %s changed digest: %w", old.File, ErrImmutableSegmentChanged)
				}
			}
		}
	}

	for _, record := range manifest.Segments {
		finalPath := filepath.Join(segmentDir, record.File)
		if info, err := os.Lstat(finalPath); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("existing segment %s: %w", record.File, ErrSegmentNotRegular)
			}

			if previousSegmentsOpened && previousManifest != nil && hasIdenticalRecord(*previousManifest, record) {
				continue
			}

			if err := verifySegment(finalPath, manifest, record); err != nil {
				// A corrupt existing file is repairable only when verified staged
				// content for the same immutable record is available.
				source, ok := staged[record.File]
				if !ok {
					return fmt.Errorf("existing segment %s is unusable and no staged repair is available: %w", record.File, err)
				}

				if err := installSegment(source, finalPath, s.Root, manifest, record); err != nil {
					return fmt.Errorf("repair segment %s: %w", record.File, err)
				}

				continue
			}

			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat segment %s: %w", record.File, err)
		}

		source, ok := staged[record.File]
		if !ok {
			return fmt.Errorf("staged content is missing for segment %s: %w", record.File, ErrStagedSegmentMissing)
		}

		if err := installSegment(source, finalPath, s.Root, manifest, record); err != nil {
			return fmt.Errorf("install segment %s: %w", record.File, err)
		}
	}

	if hooks != nil && hooks.beforeManifest != nil {
		if err := hooks.beforeManifest(); err != nil {
			return fmt.Errorf("activate release: %w", err)
		}
	}

	return writeManifest(releaseDir, manifest)
}

// readManifest reads and parses a bounded active release manifest.
func readManifest(root string) (indexset.Manifest, error) {
	var manifest indexset.Manifest
	path := filepath.Join(root, indexset.ManifestName)
	info, err := os.Lstat(path)
	if err != nil {
		return manifest, err
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limits.MaxManifestBytes {
		return manifest, ErrActiveManifestInvalid
	}

	file, err := os.Open(path)
	if err != nil {
		return manifest, err
	}

	data, readErr := io.ReadAll(io.LimitReader(file, limits.MaxManifestBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return manifest, readErr
	}
	if closeErr != nil {
		return manifest, closeErr
	}
	if len(data) > limits.MaxManifestBytes {
		return manifest, ErrActiveManifestTooLarge
	}

	return indexset.Parse(data)
}

// installSegment verifies staged bytes and atomically installs them at destination.
func installSegment(source, destination, storeRoot string, manifest indexset.Manifest, record indexset.SegmentRecord) error {
	if record.Size > uint64(limits.MaxSegmentFileSize) {
		return ErrStagedSegmentTooLarge
	}

	if pathIsWithin(source, storeRoot) {
		if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
			if err := verifySegment(source, manifest, record); err != nil {
				return err
			}

			// A verified file already on the target filesystem can be renamed in
			// place, avoiding a second full-size copy of the segment.
			if err := os.Rename(source, destination); err == nil {
				return nil
			}
		}
	}

	input, err := os.Open(source)
	if err != nil {
		return err
	}

	defer func() { _ = input.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".segment-*.stage")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	h := sha256.New()
	limit := int64(record.Size) + 1 // #nosec G115 - the segment size is bounded above.
	reader := io.LimitReader(input, limit)
	size, err := io.Copy(io.MultiWriter(tmp, h), reader)
	if err != nil {
		_ = tmp.Close()
		return err
	}

	// #nosec G115 - the segment size is bounded above.
	if size < 0 ||
		size != int64(record.Size) ||
		hex.EncodeToString(h.Sum(nil)) != record.SHA256 {
		_ = tmp.Close()
		return ErrStagedSegmentMismatch
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := verifySegment(tmpPath, manifest, record); err != nil {
		return err
	}

	return os.Rename(tmpPath, destination)
}

// verifySegment checks file size, digest, and header fields against the release record.
func verifySegment(path string, manifest indexset.Manifest, record indexset.SegmentRecord) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}

	// #nosec G115 - negative file sizes are rejected first.
	if stat.Size() < 0 || uint64(stat.Size()) != record.Size {
		_ = file.Close()
		return ErrSegmentSizeMismatch
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	if hex.EncodeToString(hash.Sum(nil)) != record.SHA256 {
		return ErrSegmentDigestMismatch
	}
	reader, err := segment.Open(path)
	if err != nil {
		return err
	}

	defer func() { _ = reader.Close() }()
	minSteamID := steamid.SteamID64(record.MinAccountID)
	maxSteamID := steamid.SteamID64(record.MaxAccountID)
	expectedAlgorithm := segment.DayZ
	if manifest.Algorithm == "battleye" {
		expectedAlgorithm = segment.Battleye
	}

	h := reader.Header
	if h.Algorithm != expectedAlgorithm ||
		h.PrefixBits != manifest.PrefixBits ||
		h.RiceK != manifest.RiceK ||
		h.Min != minSteamID ||
		h.Max != maxSteamID ||
		h.EntryCount != uint64(record.MaxAccountID)-uint64(record.MinAccountID)+1 {
		return ErrSegmentHeaderMismatch
	}

	return nil
}

// validateSHA256 checks the algorithm prefix, byte length, and lowercase hex form.
func validateSHA256(value string) error {
	if !strings.HasPrefix(value, "sha256:") {
		return ErrDigestAlgorithmNotSHA256
	}

	encoded := strings.TrimPrefix(value, "sha256:")
	digestBytes, err := hex.DecodeString(encoded)
	// DecodeString accepts uppercase hex, but persisted references use one
	// canonical lowercase spelling to avoid equivalent metadata values.
	if err != nil ||
		len(digestBytes) != sha256.Size ||
		hex.EncodeToString(digestBytes) != encoded {
		return ErrDigestNotCanonicalSHA256
	}

	return nil
}

// pathIsWithin reports whether path resolves below root.
func pathIsWithin(path, root string) bool {
	absolutePath, pathErr := filepath.Abs(path)
	absoluteRoot, rootErr := filepath.Abs(root)
	if pathErr != nil || rootErr != nil {
		return false
	}

	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || filepath.IsAbs(relative) || relative == ".." {
		return false
	}

	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// hasIdenticalRecord reports whether an active release declares this segment unchanged.
func hasIdenticalRecord(manifest indexset.Manifest, candidate indexset.SegmentRecord) bool {
	for _, record := range manifest.Segments {
		if record.Index == candidate.Index {
			return record == candidate
		}
	}

	return false
}

// writeManifest atomically replaces the active release manifest.
func writeManifest(root string, manifest indexset.Manifest) error {
	data, err := manifest.Marshal()
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(root, ".release-*.stage")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, filepath.Join(root, indexset.ManifestName))
}

// validateListedSegments checks structural segment presence without reading payloads.
func validateListedSegments(releaseDir string, manifest indexset.Manifest) error {
	segmentDir := filepath.Join(releaseDir, "segments")
	info, err := os.Lstat(segmentDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrSegmentsDirectoryInvalid
	}

	for _, record := range manifest.Segments {
		path := filepath.Join(segmentDir, record.File)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("segment %s is missing or unreadable", record.File)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("segment %s is not a regular file", record.File)
		}

		// #nosec G115 - Negative sizes are rejected before conversion.
		if info.Size() < 0 || uint64(info.Size()) != record.Size {
			return fmt.Errorf("segment %s has size %d, want %d", record.File, info.Size(), record.Size)
		}
	}

	return nil
}
