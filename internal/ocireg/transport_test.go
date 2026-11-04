// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package ocireg

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/limits"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestTransferReaderReportsMiBBatchesAndFinalBytes(t *testing.T) {
	data := bytes.Repeat([]byte{0x5a}, 1<<20+7)
	var reports []uint64
	reader := &transferReader{
		reader: bytes.NewReader(data),
		report: func(value uint64) { reports = append(reports, value) },
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("transfer reader changed the source bytes")
	}
	if len(reports) == 0 || reports[len(reports)-1] != uint64(len(data)) {
		t.Fatalf("progress reports = %v, want a final byte count", reports)
	}
}

// TestMarshalManifestIsDeterministic checks stable OCI JSON for identical descriptors.
func TestMarshalManifestIsDeterministic(t *testing.T) {
	config := ocispec.Descriptor{MediaType: configType, Digest: digest.SHA256.FromString("config"), Size: 6}
	layers := []ocispec.Descriptor{{
		MediaType: layerType,
		Digest:    digest.SHA256.FromString("segment"),
		Size:      7,
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "00000000-000000ff.ivz",
		},
	}}
	first, err := marshalManifest(config, layers)
	if err != nil {
		t.Fatal(err)
	}
	second, err := marshalManifest(config, layers)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("identical descriptors produced different OCI manifest bytes")
	}
}

// TestParseManifestRejectsUntrustedMetadata checks artifact, config, and layer limits.
func TestParseManifestRejectsUntrustedMetadata(t *testing.T) {
	config := ocispec.Descriptor{MediaType: configType, Digest: digest.SHA256.FromString("config"), Size: 6}
	valid := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2}, //nolint:modernize // Name the embedded OCI version field explicitly.
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       config,
		Layers:       []ocispec.Descriptor{{MediaType: layerType, Digest: digest.SHA256.FromString("segment"), Size: 7}},
	}
	tests := []struct {
		name   string                  // Invalid manifest property.
		mutate func(*ocispec.Manifest) // Applies the invalid property to a valid manifest.
		want   error                   // Sentinel expected from manifest validation.
	}{
		{
			name:   "wrong artifact type",
			mutate: func(manifest *ocispec.Manifest) { manifest.ArtifactType = "application/unknown" },
			want:   ErrUnsupportedOCIArtifact,
		},
		{
			name:   "oversized config",
			mutate: func(manifest *ocispec.Manifest) { manifest.Config.Size = limits.MaxManifestBytes + 1 },
			want:   ErrOCIConfigSize,
		},
		{
			name:   "too many layers",
			mutate: func(manifest *ocispec.Manifest) { manifest.Layers = make([]ocispec.Descriptor, limits.MaxSegments+1) },
			want:   ErrOCILayerCount,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := valid
			test.mutate(&manifest)
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			descriptor := descriptor(ocispec.MediaTypeImageManifest, data)
			if _, err := parseManifest(data, descriptor); !errors.Is(err, test.want) {
				t.Fatalf("parseManifest() error = %v, want %v", err, test.want)
			}
		})
	}
}

// TestValidateLayersRequiresExactConfigMatch checks descriptor-to-record agreement.
func TestValidateLayersRequiresExactConfigMatch(t *testing.T) {
	record := indexset.SegmentRecord{
		File:   "00000000-000000ff.ivz",
		Size:   7,
		SHA256: digest.SHA256.FromString("segment").Encoded(),
	}
	config := indexset.Manifest{Segments: []indexset.SegmentRecord{record}}
	layer := ocispec.Descriptor{
		MediaType:   layerType,
		Digest:      digest.Digest("sha256:" + record.SHA256),
		Size:        7,
		Annotations: map[string]string{ocispec.AnnotationTitle: record.File},
	}
	if err := validateLayers([]ocispec.Descriptor{layer}, config); err != nil {
		t.Fatalf("valid layer rejected: %v", err)
	}

	layer.Annotations[ocispec.AnnotationTitle] = "../segment.ivz"
	if err := validateLayers([]ocispec.Descriptor{layer}, config); !errors.Is(err, ErrOCILayerMismatch) {
		t.Fatal("accepted unsafe OCI title")
	}
	if err := validateLayers(nil, config); !errors.Is(err, ErrOCILayerCountMismatch) {
		t.Fatalf("unexpected layer count error: %v", err)
	}
}

// TestValidateLayersAllowsArtifactAboveIncrementalDownloadLimit checks that artifact limits remain
// independent from the incremental update budget.
func TestValidateLayersAllowsArtifactAboveIncrementalDownloadLimit(t *testing.T) {
	size := uint64(limits.MaxSegmentFileSize) // #nosec G115 - this configured limit is positive.
	sha256 := strings.Repeat("a", 64)
	makeLayers := func(count uint32) ([]ocispec.Descriptor, indexset.Manifest) {
		layers := make([]ocispec.Descriptor, 0, count)
		config := indexset.Manifest{Segments: make([]indexset.SegmentRecord, 0, count)}

		for index := range count {
			minAccount := index * 2
			maxAccount := minAccount + 1
			filename := indexset.SegmentFilename(minAccount, maxAccount)
			config.Segments = append(config.Segments, indexset.SegmentRecord{
				File:         filename,
				SHA256:       sha256,
				Size:         size,
				Index:        index,
				MinAccountID: minAccount,
				MaxAccountID: maxAccount,
			})
			layers = append(layers, ocispec.Descriptor{
				MediaType: layerType,
				Digest:    digest.Digest("sha256:" + sha256),
				Size:      limits.MaxSegmentFileSize,
				Annotations: map[string]string{
					ocispec.AnnotationTitle: filename,
				},
			})
		}

		return layers, config
	}

	largeLayers, largeConfig := makeLayers(5)
	if err := validateLayers(largeLayers, largeConfig); err != nil {
		t.Fatalf("rejected valid artifact metadata over incremental limit: %v", err)
	}
}

// TestRepositorySupportsIsolatedAuthAndExplicitPlainHTTP checks credential scopes and transport options.
func TestRepositorySupportsIsolatedAuthAndExplicitPlainHTTP(t *testing.T) {
	directory, err := os.MkdirTemp(".", ".ocireg-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	configPath := filepath.Join(directory, "config.json")
	config := struct {
		Auths map[string]map[string]string `json:"auths"` // Isolated credentials for two repositories.
	}{Auths: map[string]map[string]string{
		"localhost:5000/team-a": {"auth": base64.StdEncoding.EncodeToString([]byte("team-a:token-a"))},
		"localhost:5000/team-b": {"auth": base64.StdEncoding.EncodeToString([]byte("team-b:token-b"))},
	}}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOCKER_AUTH_CONFIG_BASE64", "not-base64")
	repository, reference, err := newRepository("localhost:5000/dayz/index:p18", configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if !repository.PlainHTTP || reference.Reference != "p18" {
		t.Fatalf("repository settings = plainHTTP:%t reference:%q", repository.PlainHTTP, reference.Reference)
	}

	client, ok := repository.Client.(*auth.Client)
	if !ok {
		t.Fatalf("repository client has type %T, want *auth.Client", repository.Client)
	}
	for _, test := range []struct {
		repository string // Repository path used for scoped credential lookup.
		username   string // Expected registry username.
		password   string // Expected registry password.
	}{
		{repository: "team-a/dayz", username: "team-a", password: "token-a"},
		{repository: "team-b/battleye", username: "team-b", password: "token-b"},
	} {
		ctx := auth.AppendRepositoryScope(context.Background(), registry.Reference{
			Registry:   "localhost:5000",
			Repository: test.repository,
		}, auth.ActionPull)
		credential, err := client.Credential(ctx, "localhost:5000")
		if err != nil {
			t.Fatalf("resolve scoped credential for %s: %v", test.repository, err)
		}
		if credential.Username != test.username || credential.Password != test.password {
			t.Errorf("credential for %s = (%q, %q), want (%q, %q)",
				test.repository, credential.Username, credential.Password, test.username, test.password)
		}
	}

	if err := os.Unsetenv("DOCKER_AUTH_CONFIG_BASE64"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Setenv("DOCKER_AUTH_CONFIG_BASE64", "not-base64") })
	t.Setenv("DOCKER_CONFIG", directory)
	ambientRepository, _, err := newRepository("localhost:5000/team-b/battleye:p18", "", false)
	if err != nil {
		t.Fatalf("initialize repository with ambient Docker config: %v", err)
	}
	if ambientRepository.PlainHTTP {
		t.Fatal("ambient repository unexpectedly enables plain HTTP")
	}

	ambientClient, ok := ambientRepository.Client.(*auth.Client)
	if !ok {
		t.Fatalf("ambient repository client has type %T, want *auth.Client", ambientRepository.Client)
	}

	ctx := auth.AppendRepositoryScope(context.Background(), registry.Reference{
		Registry:   "localhost:5000",
		Repository: "team-b/battleye",
	}, auth.ActionPull)
	credential, err := ambientClient.Credential(ctx, "localhost:5000")
	if err != nil {
		t.Fatalf("resolve ambient scoped credential: %v", err)
	}
	if credential.Username != "team-b" || credential.Password != "token-b" {
		t.Fatalf("ambient credential = (%q, %q), want (%q, %q)", credential.Username, credential.Password, "team-b", "token-b")
	}
}
