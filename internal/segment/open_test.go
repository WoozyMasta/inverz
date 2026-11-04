// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeTestIndex writes a minimal structurally valid index to a temporary file.
func writeTestIndex(t *testing.T, payload []byte) string {
	t.Helper()
	if len(payload) != 8 {
		t.Fatal("test segment payload must be eight bytes")
	}
	path := filepath.Join(t.TempDir(), "test.ivz")
	directory := make([]byte, 12)
	binary.LittleEndian.PutUint32(directory[4:], 4)
	binary.LittleEndian.PutUint32(directory[8:], 8)
	h := Header{
		Algorithm:       DayZ,
		PrefixBits:      1,
		RiceK:           0,
		Base:            76561197960265728,
		Min:             76561197960265728,
		Max:             76561197960265729,
		EntryCount:      2,
		BucketCount:     2,
		DirectoryOffset: HeaderSize,
		DirectorySize:   uint64(len(directory)),
		PayloadOffset:   HeaderSize + uint64(len(directory)),
		PayloadSize:     uint64(len(payload)),
		DirectorySHA256: DirectoryHash(directory),
		PayloadSHA256:   sha256.Sum256(payload),
	}
	hb, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	data := make([]byte, 0, len(hb)+len(directory)+len(payload))
	data = append(data, hb...)
	data = append(data, directory...)
	data = append(data, payload...)
	// #nosec G703 -- path is created under t.TempDir().
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	return path
}

// TestOpenAndVerifyPayload checks lazy payload checksum verification.
func TestOpenAndVerifyPayload(t *testing.T) {
	path := writeTestIndex(t, []byte{0, 0, 0, 0, 0, 0, 0, 0})
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.VerifyPayload(); err != nil {
		t.Fatal(err)
	}

	_ = idx.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	data[len(data)-1] ^= 1
	// #nosec G703 -- path is created under t.TempDir().
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	idx, err = Open(path)
	if err != nil {
		t.Fatalf("open should not hash payload: %v", err)
	}

	defer func() { _ = idx.Close() }()
	if err := idx.VerifyPayload(); err == nil {
		t.Fatal("accepted altered payload")
	}
}

// TestReadBucketIntoReusesCapacity checks that positional reads can reuse caller storage.
func TestReadBucketIntoReusesCapacity(t *testing.T) {
	idx, err := Open(writeTestIndex(t, []byte{0, 0, 0, 0, 0, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	dst := make([]byte, 0, 8)
	got, err := idx.ReadBucketInto(0, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || &got[0] != &dst[:1][0] {
		t.Fatal("ReadBucketInto did not reuse dst backing array")
	}
}

// TestOpenRejectsDirectoryAndTruncation rejects corrupted metadata and incomplete files.
func TestOpenRejectsDirectoryAndTruncation(t *testing.T) {
	path := writeTestIndex(t, []byte{0, 0, 0, 0, 0, 0, 0, 0})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	data[HeaderSize+8] ^= 1
	// #nosec G703 -- path is created under t.TempDir().
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("accepted altered directory")
	}
	// #nosec G703 -- path is created under t.TempDir().
	if err := os.WriteFile(path, data[:HeaderSize+5], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("accepted truncated index")
	}
}

// TestOpenRejectsBucketsOutsideStructuralSizeBounds rejects empty and undersized buckets.
func TestOpenRejectsBucketsOutsideStructuralSizeBounds(t *testing.T) {
	for _, tc := range []struct {
		name      string // Case name shown by the test runner.
		firstEdge uint32 // First bucket's end offset in the directory.
	}{
		{name: "empty", firstEdge: 0},
		{name: "short", firstEdge: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTestIndex(t, []byte{0, 0, 0, 0, 0, 0, 0, 0})
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			directory := data[HeaderSize : HeaderSize+12]
			binary.LittleEndian.PutUint32(directory[4:], tc.firstEdge)
			header, err := ParseHeader(data[:HeaderSize])
			if err != nil {
				t.Fatal(err)
			}

			header.DirectorySHA256 = DirectoryHash(directory)
			headerBytes, err := header.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}

			copy(data, headerBytes)
			// #nosec G703 -- path comes from writeTestIndex under t.TempDir().
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path); err == nil {
				t.Fatal("accepted bucket with invalid structural size")
			}
		})
	}
}
