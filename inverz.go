// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package inverz exposes the stable local index API without CLI, SQLite, or registry dependencies.
package inverz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	cacheapi "github.com/woozymasta/inverz/internal/cache/api"
	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/generate"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/profile"
	"github.com/woozymasta/inverz/internal/resolve"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
	"github.com/woozymasta/inverz/internal/store"
)

// Algorithm identifies one canonical index digest format.
type Algorithm uint8

const (
	// AlgorithmDayZ selects SHA-256 DayZ identifiers.
	AlgorithmDayZ Algorithm = iota + 1
	// AlgorithmBattlEye selects the BattlEye MD5 identifier format.
	AlgorithmBattlEye
)

// Profile contains the immutable parameters encoded in an index release.
type Profile struct {
	Version     uint8 `json:"version"      yaml:"version"`      // On-disk index format version.
	PrefixBits  uint8 `json:"prefix_bits"  yaml:"prefix_bits"`  // Digest prefix width.
	RiceK       uint8 `json:"rice_k"       yaml:"rice_k"`       // Rice remainder width.
	SegmentBits uint8 `json:"segment_bits" yaml:"segment_bits"` // AccountID segment width.
}

// SteamIDRange describes an inclusive SteamID64 interval.
type SteamIDRange struct {
	Min uint64 `json:"min" yaml:"min"` // First SteamID64 in the interval.
	Max uint64 `json:"max" yaml:"max"` // Last SteamID64 in the interval.
}

// ProgressEvent reports lifecycle or progress data from a long operation.
type ProgressEvent struct {
	State     string `json:"state"     yaml:"state"`     // started, operation-started, progress, completed, or failed.
	Unit      string `json:"unit"      yaml:"unit"`      // items or bytes, based on the operation phase.
	Phase     string `json:"phase"     yaml:"phase"`     // build, write, verify, or transfer.
	Segment   uint32 `json:"segment"   yaml:"segment"`   // Global segment index; zero for operation-level events.
	Segments  int    `json:"segments"  yaml:"segments"`  // Total segments in the release.
	Reused    int    `json:"reused"    yaml:"reused"`    // Existing OCI blobs or local segments skipped.
	Processed uint64 `json:"processed" yaml:"processed"` // Completed units in the phase.
	Total     uint64 `json:"total"     yaml:"total"`     // Total units in the phase.
	Percent   uint64 `json:"percent"   yaml:"percent"`   // Integer completion percentage where applicable.
}

// BuildOptions configures one deterministic local index build.
type BuildOptions struct {
	OnProgress func(ProgressEvent) // Optional progress callback.
	Range      SteamIDRange        // Inclusive SteamID64 range.
	Jobs       int                 // Total generator CPU budget; zero uses runtime.GOMAXPROCS.
	Profile    Profile             // Immutable codec profile.
	Force      bool                // Rebuild matching segments instead of reusing them.
	Algorithm  Algorithm           // Digest algorithm stored in the index.
}

// BuildResult summarizes the index created or extended by Build.
type BuildResult struct {
	Path      string `json:"path"       yaml:"path"`       // Canonical release directory.
	Entries   uint64 `json:"entries"    yaml:"entries"`    // Number of indexed SteamIDs.
	Segments  int    `json:"segments"   yaml:"segments"`   // Number of immutable segment files.
	SizeBytes uint64 `json:"size_bytes" yaml:"size_bytes"` // Aggregate segment size.
}

// EstimateResult summarizes advisory resource estimates for a build request.
type EstimateResult struct {
	Range              SteamIDRange `json:"range"               yaml:"range"`               // Requested SteamID64 interval.
	Entries            uint64       `json:"entries"             yaml:"entries"`             // Number of indexed SteamIDs.
	Segments           int          `json:"segments"            yaml:"segments"`            // Number of planned segments.
	ExpectedCandidates float64      `json:"expected_candidates" yaml:"expected_candidates"` // Average bucket occupancy.
	ExpectedBytes      uint64       `json:"expected_bytes"      yaml:"expected_bytes"`      // Advisory output size.
	ExpectedRAMBytes   uint64       `json:"expected_ram_bytes"  yaml:"expected_ram_bytes"`  // Expected live generator memory.
	PeakRAMBytes       uint64       `json:"peak_ram_bytes"         yaml:"peak_ram_bytes"`   // Conservative peak including payload capacity slack.
	Publishable        bool         `json:"publishable"         yaml:"publishable"`         // Whether all segments are full.
}

// SegmentInfo contains stable semantic metadata for one index segment.
type SegmentInfo struct {
	File         string `json:"file"           yaml:"file"`           // Canonical segment filename.
	SHA256       string `json:"sha256"         yaml:"sha256"`         // Lowercase digest of the full segment file.
	SizeBytes    uint64 `json:"size"           yaml:"size"`           // Complete segment size.
	Index        uint32 `json:"index"          yaml:"index"`          // Globally aligned segment index.
	MinAccountID uint32 `json:"min_account_id" yaml:"min_account_id"` // First AccountID in the segment.
	MaxAccountID uint32 `json:"max_account_id" yaml:"max_account_id"` // Last AccountID in the segment.
}

// RemoteInfo contains the last known remote selection metadata for a release.
type RemoteInfo struct {
	Reference      string `json:"reference"              yaml:"reference"`              // Credential-free registry reference.
	ManifestDigest string `json:"manifest_digest"        yaml:"manifest_digest"`        // Canonical OCI manifest digest.
	LastChecked    string `json:"last_checked,omitempty" yaml:"last_checked,omitempty"` // Last remote check time.
	LastUpdated    string `json:"last_updated,omitempty" yaml:"last_updated,omitempty"` // Last successful update time.
}

// Info describes one installed index without exposing its wire-format manifest.
type Info struct {
	Remote             *RemoteInfo   `json:"remote,omitempty"     yaml:"remote,omitempty"`     // Last known remote selection.
	Algorithm          string        `json:"algorithm"            yaml:"algorithm"`            // Canonical digest algorithm.
	EntryCount         string        `json:"entry_count"          yaml:"entry_count"`          // Canonical decimal number of IDs.
	ManifestSHA256     string        `json:"manifest_sha256"      yaml:"manifest_sha256"`      // Digest of canonical manifest JSON.
	Segments           []SegmentInfo `json:"segments"             yaml:"segments"`             // Ordered segment metadata.
	MinSteamID         uint64        `json:"min_steamid"          yaml:"min_steamid"`          // First indexed SteamID64.
	MaxSteamID         uint64        `json:"max_steamid"          yaml:"max_steamid"`          // Last indexed SteamID64.
	SegmentCount       int           `json:"segment_count"        yaml:"segment_count"`        // Number of immutable segments.
	SizeBytes          uint64        `json:"size"                 yaml:"size"`                 // Aggregate size from release metadata.
	MinAccountID       uint32        `json:"min_account_id"       yaml:"min_account_id"`       // First indexed AccountID.
	MaxAccountID       uint32        `json:"max_account_id"       yaml:"max_account_id"`       // Last indexed AccountID.
	Profile            Profile       `json:"profile"              yaml:"profile"`              // Immutable codec profile.
	IndexFormatVersion uint8         `json:"index_format_version" yaml:"index_format_version"` // Segment format version.
}

// Release summarizes one installed profile using bounded metadata only.
type Release struct {
	Remote         *RemoteInfo `json:"remote,omitempty"          yaml:"remote,omitempty"`          // Last known remote selection.
	Algorithm      string      `json:"algorithm"                 yaml:"algorithm"`                 // Canonical digest algorithm.
	Profile        string      `json:"profile"                   yaml:"profile"`                   // Canonical profile key or invalid directory name.
	MinSteamID     string      `json:"min_steamid,omitempty"     yaml:"min_steamid,omitempty"`     // First indexed SteamID64.
	MaxSteamID     string      `json:"max_steamid,omitempty"     yaml:"max_steamid,omitempty"`     // Last indexed SteamID64.
	EntryCount     string      `json:"entry_count,omitempty"     yaml:"entry_count,omitempty"`     // Number of indexed IDs.
	ManifestSHA256 string      `json:"manifest_sha256,omitempty" yaml:"manifest_sha256,omitempty"` // Digest of canonical manifest JSON.
	State          string      `json:"state"                     yaml:"state"`                     // ready or invalid.
	Error          string      `json:"error,omitempty"           yaml:"error,omitempty"`           // Bounded metadata error.
	SegmentCount   int         `json:"segment_count,omitempty"   yaml:"segment_count,omitempty"`   // Number of segment files.
	SizeBytes      uint64      `json:"size,omitempty"            yaml:"size,omitempty"`            // Aggregate segment size from metadata.
}

// Resolution is one successful identifier lookup.
type Resolution struct {
	Source  string `json:"source" yaml:"source"`     // Lookup source, such as cache or index.
	SteamID uint64 `json:"steam_id" yaml:"steam_id"` // Resolved SteamID64.
}

// Cache is an optional positive cache used to accelerate repeated index queries.
type Cache interface {
	// Lookup returns candidate SteamIDs associated with the raw algorithm digest.
	Lookup(context.Context, Algorithm, []byte) ([]uint64, error)
	// Store records an ID verified by a complete index lookup for one algorithm.
	Store(context.Context, Algorithm, uint64) error
}

// Index represents one opened immutable algorithm/profile index set. Resolve and Close must not run concurrently.
type Index struct {
	resolver  resolve.Resolver // Resolver bound to this set and optional cache.
	set       *indexset.Set    // Open segment readers and validated manifest.
	algorithm Algorithm        // Algorithm used to parse and resolve identifiers.
}

type openConfig struct {
	cache Cache // Optional caller-provided positive cache.
}

// OpenOption configures an opened index without exposing internal storage types.
type OpenOption func(*openConfig)

// WithCache attaches an optional best-effort cache to the opened index.
func WithCache(value Cache) OpenOption {
	return func(options *openConfig) { options.cache = value }
}

type cacheAdapter struct {
	cache Cache // Caller-provided cache translated to the internal resolver contract.
}

// String returns the persistent canonical spelling of the algorithm.
func (a Algorithm) String() string {
	switch a {
	case AlgorithmDayZ:
		return "dayz"

	case AlgorithmBattlEye:
		return "battleye"

	default:
		return ""
	}
}

// ParseAlgorithm accepts only canonical persistent algorithm names.
func ParseAlgorithm(value string) (Algorithm, error) {
	switch value {
	case "dayz":
		return AlgorithmDayZ, nil

	case "battleye":
		return AlgorithmBattlEye, nil

	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidAlgorithm, value)
	}
}

// DefaultProfile returns the production profile used by the CLI.
func DefaultProfile() Profile {
	return fromInternalProfile(profile.Default())
}

// ParseProfile parses one canonical local profile key.
func ParseProfile(value string) (Profile, error) {
	parsed, err := profile.Parse(value)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %w", ErrInvalidProfile, err)
	}

	return fromInternalProfile(parsed), nil
}

// ParseTag parses one canonical algorithm/profile OCI tag.
func ParseTag(value string) (Algorithm, Profile, error) {
	name, parsed, err := profile.ParseTag(value)
	if err != nil {
		return 0, Profile{}, fmt.Errorf("parse OCI index tag: %w", err)
	}

	algorithm, err := ParseAlgorithm(name)
	if err != nil {
		return 0, Profile{}, err
	}

	return algorithm, fromInternalProfile(parsed), nil
}

// String returns the canonical local-store profile key.
func (p Profile) String() string {
	return p.internal().String()
}

// Validate checks the profile against the supported on-disk format.
func (p Profile) Validate() error {
	if err := p.internal().Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidProfile, err)
	}

	return nil
}

// Tag returns the canonical OCI tag for one algorithm/profile pair.
func (p Profile) Tag(algorithm Algorithm) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if algorithm.String() == "" {
		return "", fmt.Errorf("%w: %d", ErrInvalidAlgorithm, algorithm)
	}

	return p.internal().Tag(algorithm.String())
}

// Validate checks that the inclusive SteamID64 interval is supported and ordered.
func (r SteamIDRange) Validate() error {
	return r.internal().Validate()
}

// Count returns the inclusive number of SteamIDs in a valid interval.
func (r SteamIDRange) Count() (uint64, error) {
	return r.internal().Count()
}

// DayZID returns the padded Base64URL DayZ digest for a SteamID64.
func DayZID(steamID uint64) string {
	return digest.FormatDayZ(digest.DayZ(steamID))
}

// BattlEyeGUID returns the lowercase BattlEye GUID for a SteamID64.
func BattlEyeGUID(steamID uint64) string {
	return digest.FormatBattleye(digest.Battleye(steamID))
}

// ValidateIdentifier checks one encoded identifier against its algorithm.
func ValidateIdentifier(algorithm Algorithm, value string) error {
	kind, err := resolveKind(algorithm)
	if err != nil {
		return err
	}
	if _, err := resolve.Parse(value, kind); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidIdentifier, err)
	}

	return nil
}

// Build creates or extends one canonical index release beneath root.
func Build(ctx context.Context, root string, options BuildOptions) (BuildResult, error) {
	if ctx == nil {
		return BuildResult{}, ErrNilContext
	}
	if err := ctx.Err(); err != nil {
		return BuildResult{}, err
	}
	if root == "" {
		return BuildResult{}, store.ErrIndexRootRequired
	}

	algorithm, err := internalAlgorithm(options.Algorithm)
	if err != nil {
		return BuildResult{}, err
	}
	if err := options.Profile.Validate(); err != nil {
		return BuildResult{}, err
	}
	if err := options.Range.Validate(); err != nil {
		return BuildResult{}, err
	}
	if options.Jobs < 0 {
		return BuildResult{}, generate.ErrInvalidJobs
	}

	localStore, err := store.New(root)
	if err != nil {
		return BuildResult{}, err
	}

	output, err := localStore.ProfilePath(options.Algorithm.String(), options.Profile.internal())
	if err != nil {
		return BuildResult{}, err
	}

	config := generate.Config{
		Context:     ctx,
		Force:       options.Force,
		Output:      output,
		Range:       options.Range.internal(),
		Jobs:        options.Jobs,
		Algorithm:   algorithm,
		PrefixBits:  options.Profile.PrefixBits,
		RiceK:       options.Profile.RiceK,
		RiceKSet:    true,
		SegmentBits: options.Profile.SegmentBits,
	}
	if options.OnProgress != nil {
		config.OnProgress = func(event generate.ProgressEvent) { options.OnProgress(fromProgress(event)) }
	}
	if err := config.Validate(); err != nil {
		return BuildResult{}, err
	}
	if _, err := generate.BuildWithResult(config); err != nil {
		return BuildResult{}, err
	}

	info, err := Inspect(root, options.Algorithm, options.Profile)
	if err != nil {
		return BuildResult{}, err
	}

	entries, _ := options.Range.Count()
	return BuildResult{
		Path:      output,
		Entries:   entries,
		Segments:  info.SegmentCount,
		SizeBytes: info.SizeBytes,
	}, nil
}

// Estimate calculates advisory resource requirements without creating files.
func Estimate(options BuildOptions) (EstimateResult, error) {
	algorithm, err := internalAlgorithm(options.Algorithm)
	if err != nil {
		return EstimateResult{}, err
	}
	if err := options.Profile.Validate(); err != nil {
		return EstimateResult{}, err
	}

	config := generate.Config{
		Range:       options.Range.internal(),
		Jobs:        options.Jobs,
		Algorithm:   algorithm,
		PrefixBits:  options.Profile.PrefixBits,
		RiceK:       options.Profile.RiceK,
		RiceKSet:    true,
		SegmentBits: options.Profile.SegmentBits,
	}
	result, err := config.Estimate()
	if err != nil {
		return EstimateResult{}, err
	}

	return EstimateResult{
		Range:              options.Range,
		Entries:            result.TotalEntries,
		Segments:           len(result.Segments),
		ExpectedCandidates: result.ExpectedCandidatesPerSet,
		ExpectedBytes:      result.ExpectedOutputBytes,
		ExpectedRAMBytes:   result.ExpectedRAMBytes,
		PeakRAMBytes:       result.PeakRAMBytes,
		Publishable:        result.PublishableFullSegments,
	}, nil
}

// Open opens one local index from root and optionally attaches a caller-owned cache.
func Open(root string, algorithm Algorithm, selected Profile, options ...OpenOption) (*Index, error) {
	if algorithm.String() == "" {
		return nil, fmt.Errorf("%w: %d", ErrInvalidAlgorithm, algorithm)
	}
	if err := selected.Validate(); err != nil {
		return nil, err
	}

	openOptions := openConfig{}
	for _, option := range options {
		if option != nil {
			option(&openOptions)
		}
	}

	localStore, err := store.New(root)
	if err != nil {
		return nil, err
	}

	set, err := localStore.OpenProfile(algorithm.String(), selected.internal())
	if err != nil {
		return nil, err
	}

	index := &Index{algorithm: algorithm, set: set}
	if algorithm == AlgorithmDayZ {
		index.resolver.DayZSet = set
	} else {
		index.resolver.BattleyeSet = set
	}
	if openOptions.cache != nil {
		index.resolver.Cache = cacheAdapter(openOptions)
	}

	return index, nil
}

// Resolve validates and resolves one identifier using the opened index.
func (i *Index) Resolve(ctx context.Context, identifier string) (Resolution, error) {
	if i == nil || i.set == nil {
		return Resolution{}, ErrIndexClosed
	}
	if ctx == nil {
		return Resolution{}, ErrNilContext
	}

	kind, err := resolveKind(i.algorithm)
	if err != nil {
		return Resolution{}, err
	}

	query, err := resolve.Parse(identifier, kind)
	if err != nil {
		return Resolution{}, fmt.Errorf("%w: %w", ErrInvalidIdentifier, err)
	}

	result, err := i.resolver.Resolve(ctx, query)
	if errors.Is(err, resolve.ErrNotFound) {
		return Resolution{}, fmt.Errorf("%w: %w", ErrIdentifierNotFound, err)
	}
	if errors.Is(err, resolve.ErrAmbiguous) {
		return Resolution{}, fmt.Errorf("%w: %w", ErrAmbiguousIdentifier, err)
	}
	if err != nil {
		return Resolution{}, err
	}

	return Resolution{SteamID: result.SteamID, Source: result.Source}, nil
}

// Close releases all files held by the opened index. The caller must stop Resolve calls before closing it.
func (i *Index) Close() error {
	if i == nil || i.set == nil {
		return nil
	}

	set := i.set
	i.set = nil
	return set.Close()
}

// Inspect returns semantic metadata for one installed profile without hashing payloads.
func Inspect(root string, algorithm Algorithm, selected Profile) (Info, error) {
	index, err := Open(root, algorithm, selected)
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = index.Close() }()

	manifest := index.set.Manifest
	manifestBytes, err := manifest.Marshal()
	if err != nil {
		return Info{}, err
	}

	minID, err := strconv.ParseUint(manifest.MinSteamID, 10, 64)
	if err != nil {
		return Info{}, err
	}

	maxID, err := strconv.ParseUint(manifest.MaxSteamID, 10, 64)
	if err != nil {
		return Info{}, err
	}

	minAccount, err := steamid.AccountID(minID)
	if err != nil {
		return Info{}, err
	}

	maxAccount, err := steamid.AccountID(maxID)
	if err != nil {
		return Info{}, err
	}

	segments := make([]SegmentInfo, 0, len(manifest.Segments))
	var size uint64
	for _, segment := range manifest.Segments {
		size += segment.Size
		segments = append(segments, SegmentInfo{
			File:         segment.File,
			SHA256:       segment.SHA256,
			SizeBytes:    segment.Size,
			Index:        segment.Index,
			MinAccountID: segment.MinAccountID,
			MaxAccountID: segment.MaxAccountID,
		})
	}

	info := Info{
		Algorithm:          algorithm.String(),
		Profile:            selected,
		IndexFormatVersion: manifest.IndexFormatVersion,
		MinSteamID:         minID,
		MaxSteamID:         maxID,
		MinAccountID:       minAccount,
		MaxAccountID:       maxAccount,
		EntryCount:         manifest.EntryCount,
		SegmentCount:       len(segments),
		SizeBytes:          size,
		ManifestSHA256:     hashBytes(manifestBytes),
		Segments:           segments,
	}

	localStore, err := store.New(root)
	if err == nil {
		if state, stateErr := localStore.ReadRemoteState(manifest); stateErr == nil {
			info.Remote = &RemoteInfo{
				Reference:      state.Reference,
				ManifestDigest: state.ManifestDigest,
				LastChecked:    state.LastChecked,
				LastUpdated:    state.LastUpdated,
			}
		}
	}

	return info, nil
}

// Verify checks whole-file digests for every segment in one installed profile.
func Verify(root string, algorithm Algorithm, selected Profile) error {
	return VerifyWithProgress(root, algorithm, selected, nil)
}

// VerifyWithProgress verifies every segment and reports byte-oriented progress when a callback is supplied.
func VerifyWithProgress(root string, algorithm Algorithm, selected Profile, onProgress func(ProgressEvent)) error {
	index, err := Open(root, algorithm, selected)
	if err != nil {
		return err
	}

	verifyErr := index.set.VerifyWithProgress(func(event indexset.VerifyEvent) {
		if onProgress == nil {
			return
		}

		state, processed := event.State, event.Bytes
		if event.State == "operation-started" {
			processed = 0
		}
		onProgress(ProgressEvent{
			State:     state,
			Unit:      "bytes",
			Phase:     "verify",
			Segment:   event.Segment,
			Segments:  event.Segments,
			Processed: processed,
			Total:     event.Total,
			Percent:   percent(processed, event.Total),
		})
	})

	closeErr := index.Close()
	return errors.Join(verifyErr, closeErr)
}

// List returns installed profile metadata without opening segment payloads.
func List(root string) ([]Release, error) {
	localStore, err := store.New(root)
	if err != nil {
		return nil, err
	}

	items, err := localStore.List()
	if err != nil {
		return nil, err
	}

	releases := make([]Release, 0, len(items))
	for _, item := range items {
		release := Release{
			Algorithm:      item.Algorithm,
			Profile:        item.Profile,
			MinSteamID:     item.MinSteamID,
			MaxSteamID:     item.MaxSteamID,
			EntryCount:     item.EntryCount,
			ManifestSHA256: item.ManifestSHA256,
			State:          item.State,
			Error:          item.Error,
			SegmentCount:   item.SegmentCount,
			SizeBytes:      item.Size,
		}

		if item.Remote != nil {
			release.Remote = &RemoteInfo{
				Reference:      item.Remote.Reference,
				ManifestDigest: item.Remote.ManifestDigest,
				LastChecked:    item.Remote.LastChecked,
				LastUpdated:    item.Remote.LastUpdated,
			}
		}
		releases = append(releases, release)
	}

	return releases, nil
}

// Remove deletes exactly one canonical algorithm/profile directory.
func Remove(root string, algorithm Algorithm, selected Profile) error {
	if _, err := internalAlgorithm(algorithm); err != nil {
		return err
	}
	if err := selected.Validate(); err != nil {
		return err
	}

	localStore, err := store.New(root)
	if err != nil {
		return err
	}

	return localStore.Remove(algorithm.String(), selected.internal())
}

// Lookup adapts a caller cache to the resolver's internal digest-kind contract.
func (a cacheAdapter) Lookup(ctx context.Context, kind cacheapi.Kind, raw []byte) ([]uint64, error) {
	algorithm := AlgorithmDayZ
	if kind == cacheapi.Battleye {
		algorithm = AlgorithmBattlEye
	}

	return a.cache.Lookup(ctx, algorithm, raw)
}

// PutMatch stores only the digest algorithm verified by a complete index scan.
func (a cacheAdapter) PutMatch(ctx context.Context, kind cacheapi.Kind, id uint64) error {
	algorithm := AlgorithmDayZ
	if kind == cacheapi.Battleye {
		algorithm = AlgorithmBattlEye
	}

	return a.cache.Store(ctx, algorithm, id)
}

func resolveKind(algorithm Algorithm) (resolve.Kind, error) {
	switch algorithm {
	case AlgorithmDayZ:
		return resolve.DayZ, nil

	case AlgorithmBattlEye:
		return resolve.Battleye, nil

	default:
		return resolve.Auto, fmt.Errorf("%w: %d", ErrInvalidAlgorithm, algorithm)
	}
}

func internalAlgorithm(algorithm Algorithm) (segment.Algorithm, error) {
	switch algorithm {
	case AlgorithmDayZ:
		return segment.DayZ, nil

	case AlgorithmBattlEye:
		return segment.Battleye, nil

	default:
		return 0, fmt.Errorf("%w: %d", ErrInvalidAlgorithm, algorithm)
	}
}

func (p Profile) internal() profile.Profile {
	return profile.Profile{
		Version:     p.Version,
		PrefixBits:  p.PrefixBits,
		RiceK:       p.RiceK,
		SegmentBits: p.SegmentBits,
	}
}

func fromInternalProfile(value profile.Profile) Profile {
	return Profile{
		Version:     value.Version,
		PrefixBits:  value.PrefixBits,
		RiceK:       value.RiceK,
		SegmentBits: value.SegmentBits,
	}
}

func (r SteamIDRange) internal() steamid.Range { return steamid.Range{Min: r.Min, Max: r.Max} }

func fromProgress(value generate.ProgressEvent) ProgressEvent {
	return ProgressEvent{
		State:     value.State,
		Unit:      "items",
		Phase:     value.Phase,
		Segment:   value.Segment,
		Segments:  value.Segments,
		Processed: value.Processed,
		Total:     value.Total,
		Percent:   value.Percent,
	}
}

// percent calculates an integer percentage without overflowing multiplication.
func percent(processed, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	if processed >= total {
		return 100
	}

	return processed * 100 / total
}

func hashBytes(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}
