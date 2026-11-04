// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package indexset_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/woozymasta/inverz/internal/generate"
	"github.com/woozymasta/inverz/internal/indexset"
	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// TestVerifyDigestMismatchReportsBytesRead preserves the full byte count on digest failures.
func TestVerifyDigestMismatchReportsBytesRead(t *testing.T) {
	root, err := os.MkdirTemp(".", ".indexset-verify-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove test index: %v", err)
		}
		lockPath := filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+".lock")
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove test index lock: %v", err)
		}
	})
	if err := generate.Build(generate.Config{
		Algorithm:   segment.DayZ,
		Range:       steamid.Range{Min: steamid.SteamIDBase, Max: steamid.SteamIDBase + 15},
		PrefixBits:  2,
		RiceK:       1,
		RiceKSet:    true,
		SegmentBits: 4,
		Jobs:        1,
		Output:      root,
	}); err != nil {
		t.Fatal(err)
	}

	manifest, err := indexset.ReadManifest(root)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "segments", manifest.Segments[0].File)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}

	lastByte := []byte{0}
	if _, err := file.ReadAt(lastByte, info.Size()-1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}

	lastByte[0] ^= 0xff
	if _, err := file.WriteAt(lastByte, info.Size()-1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	set, err := indexset.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = set.Close() }()

	var failed indexset.VerifyEvent
	err = set.VerifyWithProgress(func(event indexset.VerifyEvent) {
		if event.State == "failed" {
			failed = event
		}
	})
	if err == nil {
		t.Fatal("VerifyWithProgress() accepted a same-size digest mismatch")
	}

	if failed.State != "failed" ||
		failed.Bytes != manifest.Segments[0].Size ||
		failed.Total != manifest.Segments[0].Size {
		t.Fatalf(
			"failed verify event = %#v, want Bytes and Total equal %d",
			failed, manifest.Segments[0].Size)
	}
}
