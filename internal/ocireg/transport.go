// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package ocireg publishes and pulls Inverz index sets as OCI artifacts.
package ocireg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/limits"
	"github.com/woozymasta/inverz/internal/profile"
	"github.com/woozymasta/inverz/internal/store"
	"github.com/woozymasta/orascope"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	// artifactType identifies complete Inverz index-set artifacts.
	artifactType = "application/vnd.woozymasta.inverz.index.v1"
	// configType identifies the release manifest stored as the OCI config blob.
	configType = "application/vnd.woozymasta.inverz.index.config.v1+json"
	// layerType identifies immutable Inverz segment blobs.
	layerType = "application/vnd.woozymasta.inverz.index.segment.v1"
)

// PublishOptions controls release policy during OCI publication.
type PublishOptions struct {
	OnProgress   func(ProgressEvent) // Optional transfer lifecycle callback.
	AllowPartial bool                // Permits publishing a release with any partial segment.
}

// ProgressEvent reports one OCI transfer lifecycle or byte counter update.
type ProgressEvent struct {
	State    string // Operation or segment lifecycle state.
	Phase    string // Verification or transfer phase.
	Segment  uint32 // Global segment index, zero for operation-level events.
	Segments int    // Number of release segments.
	Reused   int    // Number of existing local or remote layers excluded from transfer.
	Bytes    uint64 // Cumulative bytes transferred for the active segment.
	Total    uint64 // Required transfer bytes or the active segment size.
}

// IndexArtifact describes one canonical remote index tag and its small metadata.
type IndexArtifact struct {
	Algorithm      string `json:"algorithm"       yaml:"algorithm"`       // Canonical digest algorithm.
	Profile        string `json:"profile"         yaml:"profile"`         // Canonical format profile key.
	Tag            string `json:"tag"             yaml:"tag"`             // Canonical OCI tag.
	MinSteamID     string `json:"min_steamid"     yaml:"min_steamid"`     // Inclusive first SteamID64.
	MaxSteamID     string `json:"max_steamid"     yaml:"max_steamid"`     // Inclusive last SteamID64.
	EntryCount     string `json:"entry_count"     yaml:"entry_count"`     // Number of indexed identifiers.
	ManifestDigest string `json:"manifest_digest" yaml:"manifest_digest"` // OCI manifest digest.
	SegmentCount   int    `json:"segment_count"   yaml:"segment_count"`   // Number of segment blobs.
	Size           uint64 `json:"size"            yaml:"size"`            // Aggregate segment size from descriptors.
}

// ExpectedIndex identifies the algorithm and format profile requested by a pull operation.
type ExpectedIndex struct {
	Algorithm string          // Canonical digest algorithm.
	Profile   profile.Profile // Expected on-disk index format parameters.
}

// Publish verifies and publishes a release, returning its OCI digest.
func Publish(
	ctx context.Context,
	reference, authConfig string,
	plainHTTP bool,
	set *indexset.Set,
	options PublishOptions,
) (string, error) {
	if set == nil {
		return "", ErrIndexSetRequired
	}

	if !options.AllowPartial {
		segmentSize := uint64(1) << set.Manifest.SegmentBits
		for _, record := range set.Manifest.Segments {
			entries := uint64(record.MaxAccountID) - uint64(record.MinAccountID) + 1
			if entries != segmentSize {
				return "", ErrPartialSegmentsNotAllowed
			}
		}
	}

	// Verify every source file before any content is sent to the registry.
	if options.OnProgress != nil {
		var verifyTotal uint64
		for _, record := range set.Manifest.Segments {
			verifyTotal += record.Size
		}
		options.OnProgress(ProgressEvent{
			State:    "operation-started",
			Phase:    "verify",
			Segments: len(set.Manifest.Segments),
			Total:    verifyTotal,
		})
	}

	if err := set.VerifyWithProgress(func(event indexset.VerifyEvent) {
		if options.OnProgress != nil && event.State != "operation-started" {
			options.OnProgress(ProgressEvent{
				State:    event.State,
				Phase:    "verify",
				Segment:  event.Segment,
				Segments: event.Segments,
				Bytes:    event.Bytes,
				Total:    event.Total,
			})
		}
	}); err != nil {
		return "", fmt.Errorf("verify index set: %w", err)
	}

	repo, ref, err := newRepository(reference, authConfig, plainHTTP)
	if err != nil {
		return "", err
	}

	config, err := set.Manifest.Marshal()
	if err != nil {
		return "", err
	}

	layers := make([]ocispec.Descriptor, 0, len(set.Manifest.Segments))
	for _, record := range set.Manifest.Segments {
		layer := ocispec.Descriptor{
			MediaType: layerType,
			Digest:    digest.Digest("sha256:" + record.SHA256),
			Size:      int64(record.Size), // #nosec G115 -- manifest validation caps the size at 2 GiB.
			Annotations: map[string]string{
				ocispec.AnnotationTitle: record.File,
			},
		}
		layers = append(layers, layer)
	}

	var uploadTotal int64
	needed := make([]bool, len(layers))
	var present int
	for i, layer := range layers {
		exists, err := repo.Blobs().Exists(ctx, layer)
		if err != nil {
			return "", fmt.Errorf("check remote segment %s: %w", layer.Annotations[ocispec.AnnotationTitle], err)
		}

		if exists {
			present++
			continue
		}

		needed[i] = true
		uploadTotal += layer.Size
	}

	if options.OnProgress != nil {
		options.OnProgress(ProgressEvent{
			State:    "operation-started",
			Phase:    "transfer",
			Segments: len(layers),
			Reused:   present,
			Total:    uint64(uploadTotal),
		})
	}

	for i, layer := range layers {
		if !needed[i] {
			continue
		}

		path := filepath.Join(set.Root, "segments", set.Manifest.Segments[i].File)
		if err := pushFile(ctx, repo, layer, path, options.OnProgress, set.Manifest.Segments[i].Index, len(layers)); err != nil {
			return "", fmt.Errorf("push segment %s: %w", set.Manifest.Segments[i].File, err)
		}
	}

	configDescriptor := descriptor(configType, config)

	if err := pushBytes(ctx, repo, configDescriptor, config); err != nil {
		return "", fmt.Errorf("push release config: %w", err)
	}

	manifestBytes, err := marshalManifest(configDescriptor, layers)
	if err != nil {
		return "", fmt.Errorf("encode OCI manifest: %w", err)
	}

	manifestDescriptor := descriptor(ocispec.MediaTypeImageManifest, manifestBytes)
	if err := repo.PushReference(ctx, manifestDescriptor, bytes.NewReader(manifestBytes), ref.Reference); err != nil {
		return "", fmt.Errorf("publish OCI manifest: %w", err)
	}

	return manifestDescriptor.Digest.String(), nil
}

// Pull downloads, validates, and atomically activates a release from an OCI reference.
func Pull(
	ctx context.Context,
	reference, authConfig string,
	expected ExpectedIndex,
	plainHTTP bool,
	destination *store.Store,
	progress ...func(ProgressEvent),
) (string, error) {
	var onProgress func(ProgressEvent)
	if len(progress) > 0 {
		onProgress = progress[0]
	}
	if destination == nil {
		return "", ErrLocalStoreRequired
	}

	repo, ref, err := newRepository(reference, authConfig, plainHTTP)
	if err != nil {
		return "", err
	}

	manifestDescriptor, body, err := repo.FetchReference(ctx, ref.Reference)
	if err != nil {
		return "", fmt.Errorf("resolve OCI reference: %w", err)
	}

	manifestBytes, readErr := io.ReadAll(io.LimitReader(body, limits.MaxOCIManifestBytes+1))
	closeErr := body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read OCI manifest: %w", readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close OCI manifest: %w", closeErr)
	}

	if len(manifestBytes) == 0 || len(manifestBytes) > limits.MaxOCIManifestBytes {
		return "", ErrOCIManifestSize
	}
	if int64(len(manifestBytes)) != manifestDescriptor.Size ||
		manifestDescriptor.MediaType != ocispec.MediaTypeImageManifest {
		return "", ErrOCIDescriptorMismatch
	}

	manifest, err := parseManifest(manifestBytes, manifestDescriptor)
	if err != nil {
		return "", err
	}

	configBytes, err := fetchBytes(ctx, repo, manifest.Config, limits.MaxManifestBytes)
	if err != nil {
		return "", fmt.Errorf("fetch release config: %w", err)
	}

	config, err := indexset.Parse(configBytes)
	if err != nil {
		return "", err
	}

	actualProfile := profile.Profile{
		Version:     config.IndexFormatVersion,
		PrefixBits:  config.PrefixBits,
		RiceK:       config.RiceK,
		SegmentBits: config.SegmentBits,
	}
	if config.Algorithm != expected.Algorithm || actualProfile != expected.Profile {
		return "", fmt.Errorf("%w: got %s/%s, want %s/%s",
			ErrExpectedIndexMismatch, config.Algorithm, actualProfile, expected.Algorithm, expected.Profile)
	}
	if err := validateLayers(manifest.Layers, config); err != nil {
		return "", err
	}

	active, activeErr := destination.Open(config.Algorithm, config.PrefixBits, config.RiceK, config.SegmentBits)
	if activeErr == nil {
		defer func() { _ = active.Close() }()

		state, stateErr := destination.ReadRemoteState(config)
		if stateErr == nil && state.ManifestDigest == manifestDescriptor.Digest.String() {
			// Require both the remote digest and canonical local config to match before taking the no-op path.
			activeConfig, marshalErr := active.Manifest.Marshal()
			if marshalErr != nil {
				return "", fmt.Errorf("encode local release: %w", marshalErr)
			}

			if bytes.Equal(activeConfig, configBytes) {
				state.Reference = ref.String()
				state.LastChecked = time.Now().UTC().Format(time.RFC3339)
				_ = destination.WriteRemoteState(config, state)

				return manifestDescriptor.Digest.String(), nil
			}
		}

		// Invalid or missing state is only a cache miss; permissions and I/O failures remain actionable.
		if stateErr != nil &&
			!errors.Is(stateErr, os.ErrNotExist) &&
			!errors.Is(stateErr, store.ErrRemoteStateInvalid) &&
			!errors.Is(stateErr, store.ErrRemoteStateFileInvalid) {
			return "", fmt.Errorf("read local remote state: %w", stateErr)
		}
	} else {
		// A release that cannot be opened cannot safely provide reusable segment files.
		active = nil
	}

	var downloadTotal int64
	var reused int
	// Enforce the incremental budget before creating staging files or fetching any layer.
	for i, record := range config.Segments {
		// Count only missing or changed segments toward this incremental update's download budget.
		if active != nil && segmentPresent(active.Manifest, record) {
			reused++
			continue
		}

		if manifest.Layers[i].Size > limits.MaxOCIUpdateDownloadSize-downloadTotal {
			return "", ErrUpdateDownloadLimit
		}

		downloadTotal += manifest.Layers[i].Size
	}

	if onProgress != nil {
		onProgress(ProgressEvent{
			State:    "operation-started",
			Phase:    "transfer",
			Segments: len(config.Segments),
			Reused:   reused,
			Total:    uint64(downloadTotal),
		})
	}

	if err := os.MkdirAll(destination.Root, 0700); err != nil {
		return "", fmt.Errorf("create local store root: %w", err)
	}

	stage, err := os.MkdirTemp(destination.Root, ".oci-stage-")
	if err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(stage) }()
	staged := make(map[string]string, len(config.Segments))

	for i, record := range config.Segments {
		// Reuse matching active segments and fetch only files absent from or different in the active release.
		if active != nil && segmentPresent(active.Manifest, record) {
			continue
		}

		layerSize := descriptorSize(manifest.Layers[i].Size)
		if onProgress != nil {
			onProgress(ProgressEvent{
				State:    "started",
				Segment:  record.Index,
				Segments: len(config.Segments),
				Total:    layerSize,
			})
		}

		path := filepath.Join(stage, record.File)
		var report func(uint64)
		if onProgress != nil {
			report = func(n uint64) {
				onProgress(ProgressEvent{
					State:    "progress",
					Segment:  record.Index,
					Segments: len(config.Segments),
					Bytes:    n,
					Total:    layerSize,
				})
			}
		}
		transferred, err := fetchFile(ctx, repo, manifest.Layers[i], path, report)
		if err != nil {
			if onProgress != nil {
				onProgress(ProgressEvent{
					State:    "failed",
					Segment:  record.Index,
					Segments: len(config.Segments),
					Bytes:    transferred,
					Total:    layerSize,
				})
			}
			return "", fmt.Errorf("download segment %s: %w", record.File, err)
		}
		if onProgress != nil {
			onProgress(ProgressEvent{
				State:    "completed",
				Segment:  record.Index,
				Segments: len(config.Segments),
				Bytes:    layerSize,
				Total:    layerSize,
			})
		}

		staged[record.File] = path
	}

	if err := destination.Activate(config, staged); err != nil {
		return "", fmt.Errorf("activate downloaded release: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_ = destination.WriteRemoteState(config, store.RemoteState{
		Reference:      ref.String(),
		ManifestDigest: manifestDescriptor.Digest.String(),
		LastChecked:    now,
		LastUpdated:    now,
	})

	return manifestDescriptor.Digest.String(), nil
}

// ListIndexArtifacts lists canonical index tags without downloading segment layers.
func ListIndexArtifacts(ctx context.Context, repository, authConfig string, plainHTTP bool) ([]IndexArtifact, error) {
	ref, err := registry.ParseReference(repository)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOCIReference, err)
	}
	if ref.Registry == "" || ref.Repository == "" || ref.Reference != "" {
		return nil, ErrInvalidOCIReference
	}

	repo, err := newRepositoryForName(ref.Registry+"/"+ref.Repository, authConfig, plainHTTP)
	if err != nil {
		return nil, err
	}

	var tags []string
	if err := repo.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("list OCI repository tags: %w", err)
	}

	artifacts := make([]IndexArtifact, 0)
	for _, tag := range tags {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		algorithm, selectedProfile, err := profile.ParseTag(tag)
		if err != nil {
			continue
		}

		descriptor, body, err := repo.FetchReference(ctx, tag)
		if err != nil {
			return nil, fmt.Errorf("fetch manifest for tag %q: %w", tag, err)
		}

		manifestBytes, readErr := io.ReadAll(io.LimitReader(body, limits.MaxOCIManifestBytes+1))
		closeErr := body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("read manifest for tag %q: %w", tag, err)
		}
		if len(manifestBytes) > limits.MaxOCIManifestBytes {
			continue
		}

		manifest, err := parseManifest(manifestBytes, descriptor)
		if err != nil {
			continue
		}

		configBytes, err := fetchBytes(ctx, repo, manifest.Config, limits.MaxManifestBytes)
		if err != nil {
			if errors.Is(err, ErrOCIContentMismatch) {
				continue
			}

			return nil, fmt.Errorf("fetch config for tag %q: %w", tag, err)
		}

		config, err := indexset.Parse(configBytes)
		if err != nil ||
			validateLayers(manifest.Layers, config) != nil ||
			config.Algorithm != algorithm ||
			config.IndexFormatVersion != selectedProfile.Version ||
			config.PrefixBits != selectedProfile.PrefixBits ||
			config.RiceK != selectedProfile.RiceK ||
			config.SegmentBits != selectedProfile.SegmentBits {
			continue
		}

		artifact := IndexArtifact{
			Algorithm:      algorithm,
			Profile:        selectedProfile.String(),
			Tag:            tag,
			MinSteamID:     config.MinSteamID,
			MaxSteamID:     config.MaxSteamID,
			EntryCount:     config.EntryCount,
			SegmentCount:   len(config.Segments),
			ManifestDigest: descriptor.Digest.String(),
		}

		for _, record := range config.Segments {
			artifact.Size += record.Size
		}
		artifacts = append(artifacts, artifact)
	}

	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Algorithm != artifacts[j].Algorithm {
			return artifacts[i].Algorithm < artifacts[j].Algorithm
		}

		return artifacts[i].Profile < artifacts[j].Profile
	})

	return artifacts, nil
}

// parseManifest validates the size, digest, and artifact metadata of an OCI manifest.
func parseManifest(data []byte, descriptor ocispec.Descriptor) (ocispec.Manifest, error) {
	var manifest ocispec.Manifest
	if len(data) == 0 ||
		len(data) > limits.MaxOCIManifestBytes ||
		int64(len(data)) != descriptor.Size ||
		descriptor.MediaType != ocispec.MediaTypeImageManifest {
		return manifest, ErrInvalidOCIManifest
	}

	if digest.SHA256.FromBytes(data) != descriptor.Digest {
		return manifest, ErrOCIDigestMismatch
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("decode OCI manifest: %w", err)
	}

	if manifest.SchemaVersion != 2 ||
		manifest.MediaType != ocispec.MediaTypeImageManifest ||
		manifest.ArtifactType != artifactType ||
		manifest.Config.MediaType != configType {
		return ocispec.Manifest{}, ErrUnsupportedOCIArtifact
	}

	if manifest.Config.Size <= 0 || manifest.Config.Size > limits.MaxManifestBytes {
		return ocispec.Manifest{}, ErrOCIConfigSize
	}
	if len(manifest.Layers) == 0 || len(manifest.Layers) > limits.MaxSegments {
		return ocispec.Manifest{}, ErrOCILayerCount
	}

	return manifest, nil
}

// validateLayers requires a one-to-one ordered match with the release segment records.
func validateLayers(layers []ocispec.Descriptor, config indexset.Manifest) error {
	if len(layers) != len(config.Segments) || len(layers) > limits.MaxSegments {
		return ErrOCILayerCountMismatch
	}

	for i, layer := range layers {
		record := config.Segments[i]
		// #nosec G115 -- negative layer sizes are rejected first.
		if layer.MediaType != layerType ||
			layer.Size < 0 ||
			layer.Size > limits.MaxSegmentFileSize ||
			layer.Digest.String() != "sha256:"+record.SHA256 ||
			uint64(layer.Size) != record.Size ||
			layer.Annotations[ocispec.AnnotationTitle] != record.File {
			return fmt.Errorf("%w: layer %d", ErrOCILayerMismatch, i)
		}
	}

	return nil
}

// marshalManifest creates the OCI image manifest for one Inverz artifact.
func marshalManifest(config ocispec.Descriptor, layers []ocispec.Descriptor) ([]byte, error) {
	manifest := ocispec.Manifest{
		SchemaVersion: 2,
		MediaType:     ocispec.MediaTypeImageManifest,
		ArtifactType:  artifactType,
		Config:        config,
		Layers:        layers,
	}

	return json.Marshal(manifest)
}

// segmentPresent reports whether a release already contains identical immutable segment bytes.
func segmentPresent(manifest indexset.Manifest, candidate indexset.SegmentRecord) bool {
	for _, current := range manifest.Segments {
		if current.File == candidate.File &&
			current.Size == candidate.Size &&
			current.SHA256 == candidate.SHA256 {
			return true
		}
	}

	return false
}

// newRepository configures an authenticated ORAS repository for one tagged or digested reference.
func newRepository(reference, authConfig string, plainHTTP bool) (*remote.Repository, registry.Reference, error) {
	ref, err := registry.ParseReference(reference)
	if err != nil {
		return nil, registry.Reference{}, fmt.Errorf("%w: %w", ErrInvalidOCIReference, err)
	}
	if ref.Reference == "" {
		return nil, registry.Reference{}, ErrInvalidOCIReference
	}

	repo, err := newRepositoryForName(ref.Registry+"/"+ref.Repository, authConfig, plainHTTP)
	if err != nil {
		return nil, registry.Reference{}, err
	}

	repo.Reference.Reference = ref.Reference
	return repo, ref, nil
}

// newRepositoryForName configures registry access for tag discovery or a parsed repository.
func newRepositoryForName(name, authConfig string, plainHTTP bool) (*remote.Repository, error) {
	repo, err := remote.NewRepository(name)
	if err != nil {
		return nil, fmt.Errorf("create OCI repository: %w", err)
	}

	repo.PlainHTTP = plainHTTP
	var adapter *orascope.Adapter
	if authConfig == "" {
		// Use normal discovery only when the caller did not select an isolated config file.
		adapter, err = orascope.NewDefault()
	} else {
		adapter, err = orascope.New(orascope.WithDockerConfigPath(authConfig), orascope.WithoutDiscovery())
	}

	if err != nil {
		return nil, fmt.Errorf("configure registry authentication: %w", err)
	}
	if err := adapter.WrapRepository(repo); err != nil {
		return nil, fmt.Errorf("wrap registry authentication: %w", err)
	}

	return repo, nil
}

// descriptor derives the content-addressed OCI descriptor for in-memory bytes.
func descriptor(mediaType string, data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.SHA256.FromBytes(data),
		Size:      int64(len(data)),
	}
}

// pushBytes skips blobs already present in the registry and uploads missing content.
func pushBytes(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, data []byte) error {
	exists, err := repo.Blobs().Exists(ctx, desc)
	if err != nil || exists {
		// Content-addressed blobs are immutable; an existing digest already satisfies this upload.
		return err
	}

	return repo.Blobs().Push(ctx, desc, bytes.NewReader(data))
}

// transferReader counts streamed bytes without retaining or duplicating transfer data.
type transferReader struct {
	reader  io.Reader    // Source stream forwarded to ORAS.
	report  func(uint64) // Receives cumulative bytes at one-MiB boundaries.
	total   uint64       // Total bytes read from the source.
	pending uint64       // Bytes accumulated since the previous progress report.
}

// Read forwards a transfer chunk and reports its cumulative byte count.
func (r *transferReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.total += uint64(n)
		r.pending += uint64(n)
		if r.pending >= 1<<20 {
			r.report(r.total)
			r.pending = 0
		}
	} else if err == io.EOF && r.pending > 0 {
		r.report(r.total)
		r.pending = 0
	}
	return n, err
}

// descriptorSize converts a size after OCI layer validation has rejected negative values.
func descriptorSize(size int64) uint64 {
	// #nosec G115 -- parseManifest and validateLayers reject negative descriptor sizes.
	return uint64(size)
}

// pushFile streams one previously checked missing segment to the registry.
func pushFile(
	ctx context.Context,
	repo *remote.Repository,
	desc ocispec.Descriptor,
	path string,
	onProgress func(ProgressEvent),
	segment uint32,
	segments int,
) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()
	if onProgress != nil {
		onProgress(ProgressEvent{
			State:    "started",
			Segment:  segment,
			Segments: segments,
			Total:    descriptorSize(desc.Size),
		})
	}

	var reader io.Reader = file
	var transfer *transferReader
	if onProgress != nil {
		transfer = &transferReader{reader: file, report: func(n uint64) {
			onProgress(ProgressEvent{
				State:    "progress",
				Segment:  segment,
				Segments: segments,
				Bytes:    n,
				Total:    descriptorSize(desc.Size),
			})
		}}
		reader = transfer
	}

	err = repo.Blobs().Push(ctx, desc, reader)
	state := "completed"
	if err != nil {
		state = "failed"
	}

	if onProgress != nil {
		var transferred uint64
		if transfer != nil {
			transferred = transfer.total
		}
		onProgress(ProgressEvent{
			State:    state,
			Segment:  segment,
			Segments: segments,
			Bytes:    transferred,
			Total:    descriptorSize(desc.Size),
		})
	}

	return err
}

// fetchBytes reads bounded content and verifies its declared size and digest.
func fetchBytes(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, maxBytes int64) ([]byte, error) {
	body, err := repo.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}

	data, readErr := io.ReadAll(io.LimitReader(body, maxBytes+1))
	closeErr := body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	if int64(len(data)) != desc.Size ||
		int64(len(data)) > maxBytes ||
		digest.SHA256.FromBytes(data) != desc.Digest {
		return nil, ErrOCIContentMismatch
	}

	return data, nil
}

// fetchFile streams a bounded layer to a new file and verifies it before returning success.
func fetchFile(
	ctx context.Context,
	repo *remote.Repository,
	desc ocispec.Descriptor,
	path string,
	onProgress func(uint64),
) (uint64, error) {
	body, err := repo.Fetch(ctx, desc)
	if err != nil {
		return 0, err
	}

	defer func() { _ = body.Close() }()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}

	hash := sha256.New()
	// The staged path must be removed if reading, syncing, closing, or digest validation fails.
	// Read one byte past the descriptor size to detect oversized responses without buffering the layer.
	reader := io.LimitReader(body, desc.Size+1)
	if onProgress != nil {
		reader = &transferReader{reader: reader, report: onProgress}
	}

	size, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return copiedBytes(size), copyErr
	}

	if closeErr != nil {
		_ = os.Remove(path)
		return copiedBytes(size), closeErr
	}

	if syncErr != nil {
		_ = os.Remove(path)
		return copiedBytes(size), syncErr
	}

	if err := body.Close(); err != nil {
		_ = os.Remove(path)
		return copiedBytes(size), err
	}

	if size != desc.Size || hex.EncodeToString(hash.Sum(nil)) != desc.Digest.Encoded() {
		_ = os.Remove(path)
		return copiedBytes(size), ErrOCILayerContentMismatch
	}

	return copiedBytes(size), nil
}

// copiedBytes converts the nonnegative byte count returned by io.Copy to an unsigned progress value.
func copiedBytes(size int64) uint64 {
	if size <= 0 {
		return 0
	}

	return uint64(size) // #nosec G115 -- every positive int64 is representable as uint64.
}
