// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package segment

import (
	"encoding/binary"
	"testing"
)

// TestHeaderLayoutRoundTrip checks the fixed v1 wire layout and round-trip behavior.
func TestHeaderLayoutRoundTrip(t *testing.T) {
	h := Header{
		Algorithm:       DayZ,
		PrefixBits:      2,
		RiceK:           1,
		Base:            76561197960265728,
		Min:             76561197960265728,
		Max:             76561197960265730,
		EntryCount:      3,
		BucketCount:     4,
		DirectoryOffset: 192,
		DirectorySize:   20,
		PayloadOffset:   212,
	}
	b, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 192 || string(b[:8]) != "INVERZ01" || binary.LittleEndian.Uint64(b[40:48]) != 3 {
		t.Fatal("unexpected v1 header layout")
	}

	got, err := ParseHeader(b)
	if err != nil || got != h {
		t.Fatalf("ParseHeader() = %#v, %v", got, err)
	}

	b[8] = 2
	if _, err := ParseHeader(b); err == nil {
		t.Fatal("accepted unknown version")
	}
}

// TestHeaderRejectsMalformedDirectoryOffsets rejects inconsistent serialized offsets.
func TestHeaderRejectsMalformedDirectoryOffsets(t *testing.T) {
	base := Header{
		Algorithm:       DayZ,
		PrefixBits:      2,
		RiceK:           1,
		Base:            76561197960265728,
		Min:             76561197960265728,
		Max:             76561197960265730,
		EntryCount:      3,
		BucketCount:     4,
		DirectoryOffset: 192,
		DirectorySize:   20,
		PayloadOffset:   212,
	}
	encoded, err := base.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string // Case name shown by the test runner.
		offset int    // Header byte offset of the mutated field.
		value  uint64 // Invalid field value written into the header.
	}{
		{name: "directory offset", offset: 56, value: 193},
		{name: "directory size", offset: 64, value: ^uint64(0)},
		{name: "payload offset", offset: 72, value: ^uint64(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), encoded...)
			binary.LittleEndian.PutUint64(data[tc.offset:tc.offset+8], tc.value)
			if _, err := ParseHeader(data); err == nil {
				t.Fatal("malformed directory layout accepted")
			}
		})
	}
}

// TestHeaderAllowsProtocolRangeAboveDefault accepts valid IDs beyond the initial default range.
func TestHeaderAllowsProtocolRangeAboveDefault(t *testing.T) {
	const minSteamID = uint64(76561197960265728) + (1 << 31)
	h := Header{
		Algorithm:       DayZ,
		PrefixBits:      2,
		RiceK:           1,
		Base:            76561197960265728,
		Min:             minSteamID,
		Max:             minSteamID + 2,
		EntryCount:      3,
		BucketCount:     4,
		DirectoryOffset: HeaderSize,
		DirectorySize:   20,
		PayloadOffset:   212,
	}
	if _, err := h.MarshalBinary(); err != nil {
		t.Fatalf("header above default range rejected: %v", err)
	}
}

// FuzzParseHeader ensures arbitrary byte slices never cause unsafe parsing behavior.
func FuzzParseHeader(f *testing.F) {
	seed, err := (Header{
		Algorithm:       DayZ,
		PrefixBits:      2,
		RiceK:           1,
		Base:            76561197960265728,
		Min:             76561197960265728,
		Max:             76561197960265730,
		EntryCount:      3,
		BucketCount:     4,
		DirectoryOffset: 192,
		DirectorySize:   20,
		PayloadOffset:   212,
	}).MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(seed)
	f.Add([]byte("INVERZ01"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > HeaderSize*2 {
			t.Skip()
		}
		_, _ = ParseHeader(data)
	})
}
