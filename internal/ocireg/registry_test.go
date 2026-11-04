// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package ocireg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	steamdigest "github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/generate"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/limits"
	internalprofile "github.com/woozymasta/inverz/internal/profile"
	"github.com/woozymasta/inverz/internal/resolve"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
	"github.com/woozymasta/inverz/internal/store"
)

// memoryRegistry implements the OCI Distribution calls used by this test.
type memoryRegistry struct {
	mu                sync.Mutex                     // Protects registry contents and test counters.
	blobs             map[string][]byte              // Content-addressed blobs stored by digest.
	manifests         map[string]memoryManifest      // Manifest content stored by repository and reference.
	tags              map[string]map[string]struct{} // Mutable tags exposed by repository listings.
	uploads           map[string][]byte              // Blob upload data indexed by the synthetic upload ID.
	putBlobs          map[string]struct{}            // Unique blobs accepted since the last counter reset.
	blobDownloads     int                            // Number of blob GET requests observed.
	manifestDownloads int                            // Number of manifest GET requests observed.
	nextUpload        uint64                         // Monotonic ID source for synthetic uploads.
}

// memoryManifest stores the bytes and headers for one OCI manifest.
type memoryManifest struct {
	content   []byte // Serialized OCI manifest bytes.
	mediaType string // Content-Type returned for the manifest.
	digest    string // Content digest returned for the manifest.
}

// TestPublishPullOfflineAndIncrementalUpdate checks publication, idempotent pulls, and repairs.
func TestPublishPullOfflineAndIncrementalUpdate(t *testing.T) {
	workspace := registryTestDir(t)
	registry := newMemoryRegistry()
	authPath := filepath.Join(workspace, "docker-config.json")
	if err := os.WriteFile(authPath, []byte(`{"auths":{}}`), 0600); err != nil {
		t.Fatal(err)
	}

	base := steamid.SteamIDBase
	firstPath := filepath.Join(workspace, "first")
	firstConfig := generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: base, Max: base + 31},
		PrefixBits:  3,
		RiceK:       2,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        2,
		Output:      firstPath,
	}
	if err := generate.Build(firstConfig); err != nil {
		t.Fatal(err)
	}

	first, err := indexset.Open(firstPath)
	if err != nil {
		t.Fatal(err)
	}

	firstRecords := append([]indexset.SegmentRecord(nil), first.Manifest.Segments...)
	tag := "dayz.test/inverz/dayz:dayz-v1-p3-k2-s4"
	server := httptest.NewServer(registry)
	reference := strings.TrimPrefix(server.URL, "http://") + "/" + tag
	var publishEvents []ProgressEvent
	firstDigest, err := Publish(context.Background(), reference, authPath, true, first, PublishOptions{
		OnProgress: func(event ProgressEvent) { publishEvents = append(publishEvents, event) },
	})
	if err != nil {
		_ = first.Close()
		server.Close()
		t.Fatalf("publish initial release: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if len(registry.uploadedDigests()) != 3 {
		t.Fatalf("initial publish uploaded %d unique blobs, want config plus two segments", len(registry.uploadedDigests()))
	}
	if len(publishEvents) < 4 || publishEvents[0].State != "operation-started" || publishEvents[0].Reused != 0 {
		t.Fatalf("publish progress events = %#v", publishEvents)
	}

	storeRoot := filepath.Join(workspace, "store")
	localStore, err := store.New(storeRoot)
	if err != nil {
		t.Fatal(err)
	}

	var pullEvents []ProgressEvent
	expected := ExpectedIndex{
		Algorithm: "dayz",
		Profile:   internalprofile.Profile{Version: 1, PrefixBits: 3, RiceK: 2, SegmentBits: 4},
	}
	wrongProfiles := []struct {
		name     string
		expected ExpectedIndex
	}{
		{
			name: "algorithm",
			expected: ExpectedIndex{
				Algorithm: "battleye",
				Profile:   expected.Profile,
			},
		},
		{
			name: "version",
			expected: ExpectedIndex{
				Algorithm: "dayz",
				Profile:   internalprofile.Profile{Version: 2, PrefixBits: 3, RiceK: 2, SegmentBits: 4},
			},
		},
		{
			name: "prefix width",
			expected: ExpectedIndex{
				Algorithm: "dayz",
				Profile:   internalprofile.Profile{Version: 1, PrefixBits: 4, RiceK: 2, SegmentBits: 4},
			},
		},
		{
			name: "rice parameter",
			expected: ExpectedIndex{
				Algorithm: "dayz",
				Profile:   internalprofile.Profile{Version: 1, PrefixBits: 3, RiceK: 1, SegmentBits: 4},
			},
		},
		{
			name: "segment width",
			expected: ExpectedIndex{
				Algorithm: "dayz",
				Profile:   internalprofile.Profile{Version: 1, PrefixBits: 3, RiceK: 2, SegmentBits: 5},
			},
		},
	}
	for _, test := range wrongProfiles {
		t.Run("reject mismatched expected "+test.name, func(t *testing.T) {
			registry.resetCounters()
			if _, err := Pull(
				context.Background(),
				reference,
				authPath,
				test.expected,
				true,
				localStore,
			); !errors.Is(err, ErrExpectedIndexMismatch) {
				t.Fatalf("Pull() error = %v, want ErrExpectedIndexMismatch", err)
			}
			if got := registry.blobDownloadCount(); got != 1 {
				t.Fatalf("mismatched profile fetched %d blobs, want only the config", got)
			}

			releases, err := localStore.List()
			if err != nil || len(releases) != 0 {
				t.Fatalf("mismatched profile changed local releases: %#v, %v", releases, err)
			}
		})
	}

	// Failure to persist disposable remote metadata must not undo a successful activation.
	bestEffortStore, err := store.New(filepath.Join(workspace, "best-effort-store"))
	if err != nil {
		t.Fatal(err)
	}

	bestEffortRelease, err := bestEffortStore.ProfilePath("dayz", expected.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bestEffortRelease, "remote-state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Pull(context.Background(), reference, authPath, expected, true, bestEffortStore); err != nil {
		t.Fatalf("pull failed after remote-state write failure: %v", err)
	}

	installed, err := bestEffortStore.OpenProfile("dayz", expected.Profile)
	if err != nil {
		t.Fatalf("release was not active after remote-state write failure: %v", err)
	}
	if err := installed.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Pull(context.Background(), reference, authPath, expected, true, localStore,
		func(event ProgressEvent) { pullEvents = append(pullEvents, event) }); err != nil {
		server.Close()
		t.Fatalf("pull initial release: %v", err)
	}
	if len(pullEvents) < 4 || pullEvents[0].State != "operation-started" || pullEvents[0].Reused != 0 {
		t.Fatalf("pull progress events = %#v", pullEvents)
	}

	t.Run("no-op tolerates remote-state write failure", func(t *testing.T) {
		releasePath, err := localStore.ReleasePath(first.Manifest)
		if err != nil {
			t.Fatal(err)
		}

		remoteStatePath := filepath.Join(releasePath, "remote-state.json")
		state, err := localStore.ReadRemoteState(first.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(remoteStatePath, 0400); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(remoteStatePath, 0600) })
		if err := localStore.WriteRemoteState(first.Manifest, state); err == nil {
			t.Skip("filesystem permits replacing a read-only remote-state file")
		}
		if _, err := Pull(context.Background(), reference, authPath, expected, true, localStore); err != nil {
			t.Fatalf("no-op pull failed after remote-state write failure: %v", err)
		}
	})

	registry.resetCounters()
	unchangedDigest, err := Pull(context.Background(), reference, authPath, expected, true, localStore)
	if err != nil {
		server.Close()
		t.Fatalf("check unchanged release: %v", err)
	}
	if unchangedDigest != firstDigest {
		server.Close()
		t.Fatalf("unchanged release digest = %s, want %s", unchangedDigest, firstDigest)
	}
	if registry.manifestDownloadCount() == 0 {
		server.Close()
		t.Fatal("unchanged release check did not resolve remote manifest metadata")
	}
	if got := registry.blobDownloadCount(); got != 1 {
		server.Close()
		t.Fatalf("unchanged release check downloaded %d blobs, want only the release config", got)
	}

	activePath, err := localStore.ReleasePath(first.Manifest)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}

	corruptPath := filepath.Join(activePath, "segments", firstRecords[0].File)
	corruptFile, err := os.OpenFile(corruptPath, os.O_RDWR, 0)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}

	firstByte := []byte{0}
	if _, err := corruptFile.ReadAt(firstByte, 0); err != nil {
		_ = corruptFile.Close()
		server.Close()
		t.Fatal(err)
	}

	firstByte[0] ^= 0xff
	if _, err := corruptFile.WriteAt(firstByte, 0); err != nil {
		_ = corruptFile.Close()
		server.Close()
		t.Fatal(err)
	}
	if err := corruptFile.Close(); err != nil {
		server.Close()
		t.Fatal(err)
	}

	registry.resetCounters()
	repairedDigest, err := Pull(context.Background(), reference, authPath, expected, true, localStore)
	if err != nil {
		server.Close()
		t.Fatalf("repair corrupt local release: %v", err)
	}
	if repairedDigest != firstDigest {
		server.Close()
		t.Fatalf("repaired release digest = %s, want %s", repairedDigest, firstDigest)
	}
	if got, want := registry.blobDownloadCount(), 1+len(firstRecords); got != want {
		server.Close()
		t.Fatalf("corrupt release repair downloaded %d blobs, want config plus %d segments", got, len(firstRecords))
	}
	server.Close()
	resolveFromStore(t, localStore, []uint64{base + 7, base + 31})

	secondPath := filepath.Join(workspace, "second")
	secondConfig := firstConfig
	secondConfig.Range.Max = base + 47
	secondConfig.Output = secondPath
	if err := generate.Build(secondConfig); err != nil {
		t.Fatal(err)
	}
	second, err := indexset.Open(secondPath)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = second.Close() }()
	for i, previous := range firstRecords {
		if second.Manifest.Segments[i].SHA256 != previous.SHA256 || second.Manifest.Segments[i].Size != previous.Size {
			t.Fatalf("extension changed immutable segment %d", i)
		}
	}

	server = httptest.NewServer(registry)
	defer server.Close()
	reference = strings.TrimPrefix(server.URL, "http://") + "/" + tag
	registry.resetCounters()
	secondDigest, err := Publish(context.Background(), reference, authPath, true, second, PublishOptions{})
	if err != nil {
		t.Fatalf("publish extended release: %v", err)
	}
	if secondDigest == firstDigest {
		t.Fatal("mutable tag update produced the original manifest digest")
	}
	if got := len(registry.uploadedDigests()); got != 2 {
		t.Fatalf("extension uploaded %d unique blobs, want new config and one segment", got)
	}

	registry.resetCounters()
	newSegmentDigest := "sha256:" + second.Manifest.Segments[len(second.Manifest.Segments)-1].SHA256
	registry.mu.Lock()
	corruptedBlob := append([]byte(nil), registry.blobs[newSegmentDigest]...)
	if len(corruptedBlob) == 0 {
		registry.mu.Unlock()
		t.Fatalf("new segment blob %s is missing", newSegmentDigest)
	}

	registry.blobs[newSegmentDigest][0] ^= 0xff
	registry.mu.Unlock()
	if _, err := Pull(context.Background(), reference, authPath, expected, true, localStore); err == nil {
		t.Fatal("pull accepted a corrupted segment")
	}

	registry.mu.Lock()
	registry.blobs[newSegmentDigest] = corruptedBlob
	registry.mu.Unlock()
	resolveFromStore(t, localStore, []uint64{base + 7, base + 31})
	stagedEntries, err := os.ReadDir(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range stagedEntries {
		if strings.HasPrefix(entry.Name(), ".oci-stage-") {
			t.Fatalf("failed pull left staging directory %q", entry.Name())
		}
	}

	registry.resetCounters()
	tagDigest, err := Pull(context.Background(), reference, authPath, expected, true, localStore)
	if err != nil {
		t.Fatalf("pull extended release: %v", err)
	}
	if tagDigest != secondDigest {
		t.Fatalf("mutable tag resolved to %s, want updated digest %s", tagDigest, secondDigest)
	}
	if got := registry.blobDownloadCount(); got != 2 {
		t.Fatalf("extension downloaded %d blobs, want config and one new segment", got)
	}

	pinnedReference := strings.TrimSuffix(reference, ":small") + "@" + firstDigest
	pinnedStore, err := store.New(filepath.Join(workspace, "pinned-store"))
	if err != nil {
		t.Fatal(err)
	}

	pinnedDigest, err := Pull(context.Background(), pinnedReference, authPath, expected, true, pinnedStore)
	if err != nil {
		t.Fatalf("pull digest-pinned original release: %v", err)
	}
	if pinnedDigest != firstDigest {
		t.Fatalf("digest-pinned reference resolved to %s, want %s", pinnedDigest, firstDigest)
	}

	server.Close()
	resolveFromStore(t, localStore, []uint64{base + 7, base + 31, base + 47})
	resolveFromStore(t, pinnedStore, []uint64{base + 7, base + 31})
}

// TestListIndexArtifactsFiltersTagsWithoutDownloadingSegments verifies metadata-only discovery.
func TestListIndexArtifactsFiltersTagsWithoutDownloadingSegments(t *testing.T) {
	workspace := registryTestDir(t)
	registry := newMemoryRegistry()
	authPath := filepath.Join(workspace, "docker-config.json")
	if err := os.WriteFile(authPath, []byte(`{"auths":{}}`), 0600); err != nil {
		t.Fatal(err)
	}

	indexPath := filepath.Join(workspace, "index")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 3,
		Jobs:        1,
		Output:      indexPath,
	}); err != nil {
		t.Fatal(err)
	}
	set, err := indexset.Open(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(registry)
	defer server.Close()

	repository := "inverz/dayz"
	registry.addTags(repository, "0.1.2", "latest", "dayz-v1-p2-k1-s3-invalid")
	malformedTag := "dayz-v1-p2-k1-s4"
	registry.addTags(repository, malformedTag)
	malformedConfig := []byte("not json")
	malformedConfigDescriptor := descriptor(configType, malformedConfig)
	malformedLayer := ocispec.Descriptor{
		MediaType: layerType,
		Digest:    digest.FromString("unused segment"),
		Size:      1,
		Annotations: map[string]string{
			ocispec.AnnotationTitle: "00000000-00000001.ivz",
		},
	}

	malformedManifest, err := marshalManifest(malformedConfigDescriptor, []ocispec.Descriptor{malformedLayer})
	if err != nil {
		t.Fatal(err)
	}

	malformedManifestDescriptor := descriptor(ocispec.MediaTypeImageManifest, malformedManifest)
	registry.blobs[malformedConfigDescriptor.Digest.String()] = malformedConfig
	registry.manifests[repository+"/"+malformedTag] = memoryManifest{
		content:   malformedManifest,
		mediaType: ocispec.MediaTypeImageManifest,
		digest:    malformedManifestDescriptor.Digest.String(),
	}
	tag := "dayz-v1-p2-k1-s3"
	reference := strings.TrimPrefix(server.URL, "http://") + "/" + repository + ":" + tag
	if _, err := Publish(context.Background(), reference, authPath, true, set, PublishOptions{}); err != nil {
		_ = set.Close()
		t.Fatalf("publish test artifact: %v", err)
	}
	if err := set.Close(); err != nil {
		t.Fatal(err)
	}
	registry.resetCounters()

	artifacts, err := ListIndexArtifacts(context.Background(), strings.TrimPrefix(server.URL, "http://")+"/"+repository, authPath, true)
	if err != nil {
		t.Fatalf("list index artifacts: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("listed %d artifacts, want 1: %#v", len(artifacts), artifacts)
	}

	artifact := artifacts[0]
	if artifact.Algorithm != "dayz" || artifact.Profile != "v1-p2-k1-s3" || artifact.Tag != tag || artifact.SegmentCount != 2 {
		t.Fatalf("unexpected artifact metadata: %#v", artifact)
	}
	if registry.manifestDownloadCount() != 2 || registry.blobDownloadCount() != 2 {
		t.Fatalf("remote discovery fetched %d manifests and %d blobs; want both canonical configs only",
			registry.manifestDownloadCount(), registry.blobDownloadCount())
	}
}

// TestListIndexArtifactsReturnsRegistryFailures prevents discovery from hiding transport errors.
func TestListIndexArtifactsReturnsRegistryFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		code   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, code: "UNAUTHORIZED"},
		{name: "forbidden", status: http.StatusForbidden, code: "DENIED"},
		{name: "rate limited", status: http.StatusTooManyRequests, code: "UNKNOWN"},
		{name: "server error", status: http.StatusInternalServerError, code: "UNKNOWN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch {
				case request.URL.Path == "/v2/":
					writer.WriteHeader(http.StatusOK)

				case strings.HasSuffix(request.URL.Path, "/tags/list"):
					writer.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(writer, `{"name":"inverz/dayz","tags":["dayz-v1-p2-k1-s3"]}`)

				default:
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(test.status)
					_ = json.NewEncoder(writer).Encode(map[string]any{
						"errors": []map[string]string{{"code": test.code, "message": test.code}},
					})
				}
			}))
			defer server.Close()

			repository := strings.TrimPrefix(server.URL, "http://") + "/inverz/dayz"
			if _, err := ListIndexArtifacts(context.Background(), repository, "", true); err == nil {
				t.Fatal("ListIndexArtifacts() succeeded despite registry failure")
			}
		})
	}
}

// TestListIndexArtifactsReturnsUnclassifiedNotFound avoids masking ORAS's lossy 404 response.
func TestListIndexArtifactsReturnsUnclassifiedNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/v2/":
			writer.WriteHeader(http.StatusOK)

		case strings.HasSuffix(request.URL.Path, "/tags/list"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"name":"inverz/dayz","tags":["dayz-v1-p2-k1-s3"]}`)

		default:
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"errors": []map[string]string{{"code": "MANIFEST_UNKNOWN", "message": "missing"}},
			})
		}
	}))
	defer server.Close()

	repository := strings.TrimPrefix(server.URL, "http://") + "/inverz/dayz"
	if _, err := ListIndexArtifacts(context.Background(), repository, "", true); err == nil {
		t.Fatal("ListIndexArtifacts() hid a 404 that ORAS could not classify as MANIFEST_UNKNOWN")
	}
}

// TestListIndexArtifactsReturnsContextCancellation keeps canceled discovery distinguishable from empty results.
func TestListIndexArtifactsReturnsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v2/" {
			writer.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repository := strings.TrimPrefix(server.URL, "http://") + "/inverz/dayz"
	if _, err := ListIndexArtifacts(ctx, repository, "", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListIndexArtifacts() error = %v, want context.Canceled", err)
	}
}

// TestListIndexArtifactsReturnsManifestReadErrors preserves truncated response-body failures.
func TestListIndexArtifactsReturnsManifestReadErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/v2/":
			writer.WriteHeader(http.StatusOK)

		case strings.HasSuffix(request.URL.Path, "/tags/list"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"name":"inverz/dayz","tags":["dayz-v1-p2-k1-s3"]}`)

		default:
			writer.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			writer.Header().Set("Docker-Content-Digest", digest.FromString("manifest").String())
			writer.Header().Set("Content-Length", "100")
			writer.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(writer, "{\"broken\"")
		}
	}))
	defer server.Close()

	repository := strings.TrimPrefix(server.URL, "http://") + "/inverz/dayz"
	if _, err := ListIndexArtifacts(context.Background(), repository, "", true); err == nil {
		t.Fatal("ListIndexArtifacts() hid a truncated manifest response")
	}
}

// TestPublishRequiresExplicitPermissionForPartialSegments checks the publication policy guard.
func TestPublishRequiresExplicitPermissionForPartialSegments(t *testing.T) {
	root := registryTestDir(t)
	output := filepath.Join(root, "partial")
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase + 1, Max: steamid.SteamIDBase + 2},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      output,
	}); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()

	if _, err := Publish(context.Background(), "", "", false, set, PublishOptions{}); !errors.Is(err, ErrPartialSegmentsNotAllowed) {
		t.Fatalf("partial publish without permission error = %v", err)
	}
	if _, err := Publish(context.Background(), "", "", false, set, PublishOptions{AllowPartial: true}); err == nil ||
		errors.Is(err, ErrPartialSegmentsNotAllowed) {
		t.Fatalf("explicit partial publish did not pass policy: %v", err)
	}
}

// TestPullErrorDoesNotExposeRegistryCredentials ensures auth data is absent from returned errors.
func TestPullErrorDoesNotExposeRegistryCredentials(t *testing.T) {
	workspace := registryTestDir(t)
	username := "inverz-test-user"
	fixtureValue := "inverz-test-token"
	authValue := base64.StdEncoding.EncodeToString([]byte(username + ":" + fixtureValue))
	var authMu sync.Mutex
	authSeen := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		expectedAuth := "Basic " + authValue
		if request.Header.Get("Authorization") != expectedAuth {
			writer.Header().Set("WWW-Authenticate", `Basic realm="registry-test"`)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}

		authMu.Lock()
		authSeen = true
		authMu.Unlock()
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(writer, "registry test failure")
	}))
	defer server.Close()

	authConfig := filepath.Join(workspace, "docker-config.json")
	config := struct {
		Auths map[string]map[string]string `json:"auths"` // Registry credentials used by the test client.
	}{Auths: map[string]map[string]string{
		strings.TrimPrefix(server.URL, "http://"): {"auth": authValue},
	}}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authConfig, configBytes, 0600); err != nil {
		t.Fatal(err)
	}

	localStore, err := store.New(filepath.Join(workspace, "store"))
	if err != nil {
		t.Fatal(err)
	}

	reference := strings.TrimPrefix(server.URL, "http://") + "/dayz/index:stable"
	if _, err := Pull(context.Background(), reference, authConfig, ExpectedIndex{Algorithm: "dayz", Profile: internalprofile.Default()}, true, localStore); err == nil {
		t.Fatal("pull unexpectedly succeeded against a failing registry")
	} else {
		for _, sensitive := range []string{username, fixtureValue, authValue, "Basic " + authValue} {
			if strings.Contains(err.Error(), sensitive) {
				t.Fatal("registry error exposed configured credentials")
			}
		}
	}

	authMu.Lock()
	defer authMu.Unlock()
	if !authSeen {
		t.Fatal("test registry did not receive configured credentials")
	}
}

// TestPullRejectsOversizedUpdateBeforeFetchingLayers checks the budget before layer downloads start.
func TestPullRejectsOversizedUpdateBeforeFetchingLayers(t *testing.T) {
	workspace := registryTestDir(t)
	registry := newMemoryRegistry()
	server := httptest.NewServer(registry)
	defer server.Close()

	// segmentCount exceeds the incremental budget using descriptor metadata only.
	const segmentCount uint32 = 5
	base := steamid.SteamIDBase
	segmentSize := uint64(limits.MaxSegmentFileSize) // #nosec G115 -- the configured limit is positive.
	sha256 := strings.Repeat("a", 64)
	config := indexset.Manifest{
		Algorithm:          "dayz",
		MinSteamID:         strconv.FormatUint(base, 10),
		MaxSteamID:         strconv.FormatUint(base+uint64(segmentCount)*2-1, 10),
		EntryCount:         strconv.FormatUint(uint64(segmentCount)*2, 10),
		SchemaVersion:      1,
		IndexFormatVersion: 1,
		PrefixBits:         1,
		RiceK:              0,
		SegmentBits:        1,
	}
	layers := make([]ocispec.Descriptor, 0, 5)
	for index := range segmentCount {
		minAccount := index * 2
		maxAccount := minAccount + 1
		filename := indexset.SegmentFilename(minAccount, maxAccount)
		config.Segments = append(config.Segments, indexset.SegmentRecord{
			File:         filename,
			SHA256:       sha256,
			Size:         segmentSize,
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

	configBytes, err := config.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	configDescriptor := descriptor(configType, configBytes)
	manifestBytes, err := marshalManifest(configDescriptor, layers)
	if err != nil {
		t.Fatal(err)
	}

	manifestDescriptor := descriptor(ocispec.MediaTypeImageManifest, manifestBytes)
	repository := "dayz.test/inverz/dayz"
	registry.blobs[configDescriptor.Digest.String()] = configBytes
	registry.manifests[repository+"/oversized"] = memoryManifest{
		content:   manifestBytes,
		mediaType: ocispec.MediaTypeImageManifest,
		digest:    manifestDescriptor.Digest.String(),
	}

	authConfig := filepath.Join(workspace, "docker-config.json")
	if err := os.WriteFile(authConfig, []byte(`{"auths":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	localStore, err := store.New(filepath.Join(workspace, "store"))
	if err != nil {
		t.Fatal(err)
	}
	reference := strings.TrimPrefix(server.URL, "http://") + "/" + repository + ":oversized"
	expected := ExpectedIndex{Algorithm: "dayz", Profile: internalprofile.Profile{Version: 1, PrefixBits: 1, RiceK: 0, SegmentBits: 1}}
	if _, err := Pull(context.Background(), reference, authConfig, expected, true, localStore); !errors.Is(err, ErrUpdateDownloadLimit) {
		t.Fatalf("unexpected update-size pull error: %v", err)
	}
	if got := registry.blobDownloadCount(); got != 1 {
		t.Fatalf("pull fetched %d blobs before rejecting size, want only the release config", got)
	}
}

// resolveFromStore checks that pulled IDs remain resolvable without a registry.
func resolveFromStore(t *testing.T, localStore *store.Store, ids []uint64) {
	t.Helper()
	set, err := localStore.Open("dayz", 3, 2, 4)
	if err != nil {
		t.Fatalf("open offline release: %v", err)
	}

	defer func() { _ = set.Close() }()
	resolver := resolve.Resolver{DayZSet: set}
	for _, id := range ids {
		query, err := resolve.Parse(steamdigest.FormatDayZ(steamdigest.DayZ(id)), resolve.DayZ)
		if err != nil {
			t.Fatalf("format query for SteamID64 %d: %v", id, err)
		}

		result, err := resolver.Resolve(context.Background(), query)
		if err != nil {
			t.Fatalf("resolve SteamID64 %d offline: %v", id, err)
		}

		if result.SteamID != id {
			t.Fatalf("resolved SteamID64 %d to %d", id, result.SteamID)
		}
	}
}

// registryTestDir creates a test workspace under the package directory.
func registryTestDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(".", ".oci-registry-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})

	return directory
}

// newMemoryRegistry constructs an empty in-memory registry backend.
func newMemoryRegistry() *memoryRegistry {
	return &memoryRegistry{
		blobs:     make(map[string][]byte),
		manifests: make(map[string]memoryManifest),
		tags:      make(map[string]map[string]struct{}),
		uploads:   make(map[string][]byte),
		putBlobs:  make(map[string]struct{}),
	}
}

// ServeHTTP handles the subset of OCI Distribution required by ORAS.
func (registry *memoryRegistry) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/v2/" {
		writer.WriteHeader(http.StatusOK)
		return
	}

	path := strings.TrimPrefix(request.URL.Path, "/v2/")
	if !strings.HasPrefix(request.URL.Path, "/v2/") {
		registry.writeError(writer, http.StatusNotFound, "NAME_UNKNOWN")
		return
	}
	if repository, uploadID, found := strings.Cut(path, "/blobs/uploads/"); found {
		// Upload routes must be checked before generic blob routes because both share the same prefix.
		registry.handleUpload(writer, request, repository, uploadID)
		return
	}
	if repository, blobDigest, found := strings.Cut(path, "/blobs/"); found {
		registry.handleBlob(writer, request, repository, blobDigest)
		return
	}
	if repository, found := strings.CutSuffix(path, "/tags/list"); found {
		registry.handleTags(writer, repository)
		return
	}
	if repository, reference, found := strings.Cut(path, "/manifests/"); found {
		registry.handleManifest(writer, request, repository, reference)
		return
	}

	registry.writeError(writer, http.StatusNotFound, "NAME_UNKNOWN")
}

// handleTags lists mutable manifest tags for one repository.
func (registry *memoryRegistry) handleTags(writer http.ResponseWriter, repository string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	tags := make([]string, 0, len(registry.tags[repository]))
	for tag := range registry.tags[repository] {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{Name: repository, Tags: tags})
}

// addTags seeds unrelated tags for tag-filtering tests.
func (registry *memoryRegistry) addTags(repository string, tags ...string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if registry.tags[repository] == nil {
		registry.tags[repository] = make(map[string]struct{})
	}
	for _, tag := range tags {
		registry.tags[repository][tag] = struct{}{}
	}
}

// handleUpload accepts monolithic blob uploads and verifies their digests.
func (registry *memoryRegistry) handleUpload(writer http.ResponseWriter, request *http.Request, repository, uploadID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	switch request.Method {
	case http.MethodPost:
		// POST begins a new upload session.
		registry.nextUpload++
		uploadID = strconv.FormatUint(registry.nextUpload, 10)
		registry.uploads[uploadID] = nil
		writer.Header().Set("Location", "/v2/"+repository+"/blobs/uploads/"+uploadID)
		writer.Header().Set("Docker-Upload-UUID", uploadID)
		writer.WriteHeader(http.StatusAccepted)

	case http.MethodPut:
		// PUT appends content and commits it only after the requested digest matches.
		data, err := io.ReadAll(request.Body)
		if err != nil {
			registry.writeError(writer, http.StatusInternalServerError, "UNKNOWN")
			return
		}

		data = append(registry.uploads[uploadID], data...)
		expected := request.URL.Query().Get("digest")
		if digest.FromBytes(data).String() != expected {
			registry.writeError(writer, http.StatusBadRequest, "DIGEST_INVALID")
			return
		}

		registry.blobs[expected] = data
		registry.putBlobs[expected] = struct{}{}
		delete(registry.uploads, uploadID)
		writer.Header().Set("Location", "/v2/"+repository+"/blobs/"+expected)
		writer.Header().Set("Docker-Content-Digest", expected)
		writer.WriteHeader(http.StatusCreated)

	default:
		registry.writeError(writer, http.StatusMethodNotAllowed, "UNSUPPORTED")
	}
}

// handleBlob serves content-addressed blobs and counts downloads.
func (registry *memoryRegistry) handleBlob(writer http.ResponseWriter, request *http.Request, _, blobDigest string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	data, ok := registry.blobs[blobDigest]
	if !ok {
		registry.writeError(writer, http.StatusNotFound, "BLOB_UNKNOWN")
		return
	}

	writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
	writer.Header().Set("Docker-Content-Digest", blobDigest)

	switch request.Method {
	case http.MethodHead:
		writer.WriteHeader(http.StatusOK)

	case http.MethodGet:
		// Count only content reads; HEAD requests are metadata probes by the ORAS client.
		registry.blobDownloads++
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(data)

	default:
		registry.writeError(writer, http.StatusMethodNotAllowed, "UNSUPPORTED")
	}
}

// handleManifest serves and records OCI image manifests.
func (registry *memoryRegistry) handleManifest(writer http.ResponseWriter, request *http.Request, repository, reference string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if request.Method == http.MethodPut {
		// Store published manifests under both their mutable reference and immutable digest.
		data, err := io.ReadAll(request.Body)
		if err != nil {
			registry.writeError(writer, http.StatusInternalServerError, "UNKNOWN")
			return
		}

		digestValue := digest.FromBytes(data).String()
		mediaType := request.Header.Get("Content-Type")
		registry.manifests[repository+"/"+reference] = memoryManifest{
			content:   data,
			mediaType: mediaType,
			digest:    digestValue,
		}
		registry.manifests[repository+"/"+digestValue] = memoryManifest{
			content:   data,
			mediaType: mediaType,
			digest:    digestValue,
		}
		if !strings.HasPrefix(reference, "sha256:") {
			if registry.tags[repository] == nil {
				registry.tags[repository] = make(map[string]struct{})
			}
			registry.tags[repository][reference] = struct{}{}
		}
		writer.Header().Set("Docker-Content-Digest", digestValue)
		writer.WriteHeader(http.StatusCreated)

		return
	}

	manifest, ok := registry.manifests[repository+"/"+reference]
	if !ok {
		registry.writeError(writer, http.StatusNotFound, "MANIFEST_UNKNOWN")
		return
	}

	writer.Header().Set("Content-Type", manifest.mediaType)
	writer.Header().Set("Content-Length", strconv.Itoa(len(manifest.content)))
	writer.Header().Set("Docker-Content-Digest", manifest.digest)

	switch request.Method {
	case http.MethodHead:
		writer.WriteHeader(http.StatusOK)

	case http.MethodGet:
		registry.manifestDownloads++
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(manifest.content)

	default:
		registry.writeError(writer, http.StatusMethodNotAllowed, "UNSUPPORTED")
	}
}

// writeError emits an OCI Distribution error envelope.
func (registry *memoryRegistry) writeError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": code}}})
}

// uploadedDigests returns unique blobs accepted since the last reset.
func (registry *memoryRegistry) uploadedDigests() []string {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	digests := make([]string, 0, len(registry.putBlobs))
	for value := range registry.putBlobs {
		digests = append(digests, value)
	}

	return digests
}

// blobDownloadCount returns the number of GET requests for blobs.
func (registry *memoryRegistry) blobDownloadCount() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.blobDownloads
}

// manifestDownloadCount returns the number of GET requests for manifests.
func (registry *memoryRegistry) manifestDownloadCount() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.manifestDownloads
}

// resetCounters clears upload and download observations.
func (registry *memoryRegistry) resetCounters() {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.putBlobs = make(map[string]struct{})
	registry.blobDownloads = 0
	registry.manifestDownloads = 0
}
