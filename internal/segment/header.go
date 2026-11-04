// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package segment defines and reads the on-disk index format.
package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/woozymasta/inverz/internal/limits"

	"github.com/woozymasta/inverz/internal/steamid"
)

// HeaderSize is the fixed v1 index header size in bytes.
const HeaderSize = 192

const (
	// DayZ identifies indexes containing DayZ SHA-256 digests.
	DayZ Algorithm = 1
	// Battleye identifies indexes containing BattlEye MD5 digests.
	Battleye Algorithm = 2
)

// magic identifies the v1 index format.
var magic = [8]byte{'I', 'N', 'V', 'E', 'R', 'Z', '0', '1'}

// Algorithm identifies the digest stored in an index.
type Algorithm uint8

// Header is the fixed v1 index metadata.
type Header struct {
	Base            uint64    // Base SteamID used to encode account IDs.
	Min             uint64    // Smallest SteamID represented by the segment.
	Max             uint64    // Largest SteamID represented by the segment.
	EntryCount      uint64    // Number of contiguous SteamIDs represented.
	DirectoryOffset uint64    // Byte offset of the bucket directory.
	DirectorySize   uint64    // Serialized directory size in bytes.
	PayloadOffset   uint64    // Byte offset of the encoded bucket payload.
	PayloadSize     uint64    // Encoded payload size in bytes.
	BucketCount     uint32    // Number of prefix buckets in the directory.
	DirectorySHA256 [32]byte  // SHA-256 checksum of the serialized directory.
	PayloadSHA256   [32]byte  // SHA-256 checksum of the encoded payload.
	Algorithm       Algorithm // Digest algorithm represented by the index.
	PrefixBits      uint8     // Number of high digest bits used to select a bucket.
	RiceK           uint8     // Rice coding parameter used for account ID gaps.
}

// MarshalBinary serializes a valid header as exactly 192 little-endian bytes.
func (h Header) MarshalBinary() ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}

	b := make([]byte, HeaderSize)
	copy(b[:8], magic[:])

	binary.LittleEndian.PutUint16(b[8:10], 1)
	b[10], b[11], b[12] = byte(h.Algorithm), h.PrefixBits, h.RiceK

	binary.LittleEndian.PutUint64(b[16:24], h.Base)
	binary.LittleEndian.PutUint64(b[24:32], h.Min)
	binary.LittleEndian.PutUint64(b[32:40], h.Max)
	binary.LittleEndian.PutUint64(b[40:48], h.EntryCount)
	binary.LittleEndian.PutUint32(b[48:52], h.BucketCount)
	binary.LittleEndian.PutUint32(b[52:56], HeaderSize)
	binary.LittleEndian.PutUint64(b[56:64], h.DirectoryOffset)
	binary.LittleEndian.PutUint64(b[64:72], h.DirectorySize)
	binary.LittleEndian.PutUint64(b[72:80], h.PayloadOffset)
	binary.LittleEndian.PutUint64(b[80:88], h.PayloadSize)

	copy(b[88:120], h.DirectorySHA256[:])
	copy(b[120:152], h.PayloadSHA256[:])

	return b, nil
}

// ParseHeader validates and parses one v1 header.
func ParseHeader(b []byte) (Header, error) {
	var h Header
	if len(b) != HeaderSize {
		return h, fmt.Errorf("header length %d, want %d", len(b), HeaderSize)
	}

	if string(b[:8]) != string(magic[:]) {
		return h, ErrInvalidMagic
	}
	if binary.LittleEndian.Uint16(b[8:10]) != 1 {
		return h, ErrUnsupportedVersion
	}
	if binary.LittleEndian.Uint32(b[52:56]) != HeaderSize {
		return h, ErrInvalidHeaderSize
	}
	if b[13] != 0 || binary.LittleEndian.Uint16(b[14:16]) != 0 {
		return h, ErrUnsupportedHeaderFlags
	}

	// Reserved bytes must stay zero so future versions can assign them without
	// silently changing how older readers interpret a v1 header.
	for _, v := range b[152:] {
		if v != 0 {
			return h, ErrNonzeroReservedBytes
		}
	}

	h = Header{
		Algorithm:       Algorithm(b[10]),
		PrefixBits:      b[11],
		RiceK:           b[12],
		Base:            binary.LittleEndian.Uint64(b[16:24]),
		Min:             binary.LittleEndian.Uint64(b[24:32]),
		Max:             binary.LittleEndian.Uint64(b[32:40]),
		EntryCount:      binary.LittleEndian.Uint64(b[40:48]),
		BucketCount:     binary.LittleEndian.Uint32(b[48:52]),
		DirectoryOffset: binary.LittleEndian.Uint64(b[56:64]),
		DirectorySize:   binary.LittleEndian.Uint64(b[64:72]),
		PayloadOffset:   binary.LittleEndian.Uint64(b[72:80]),
		PayloadSize:     binary.LittleEndian.Uint64(b[80:88]),
	}
	copy(h.DirectorySHA256[:], b[88:120])
	copy(h.PayloadSHA256[:], b[120:152])

	return h, h.Validate()
}

// Validate checks all fixed format invariants.
func (h Header) Validate() error {
	if h.Algorithm != DayZ && h.Algorithm != Battleye {
		return ErrUnknownAlgorithm
	}

	if h.PrefixBits == 0 || h.PrefixBits > limits.MaxPrefixBits {
		return ErrUnsupportedPrefixWidth
	}

	if h.RiceK > 31 {
		return ErrUnsupportedRiceParameter
	}

	if h.Base != steamid.SteamIDBase ||
		h.Min < h.Base ||
		h.Max < h.Min ||
		h.Max > steamid.ProtocolMaxSteamID {
		return ErrInvalidSteamIDRange
	}

	// The format describes a contiguous interval, so its length is the expected
	// entry count and does not require scanning bucket contents.
	if h.EntryCount != h.Max-h.Min+1 {
		return ErrEntryCountMismatch
	}

	if h.BucketCount != uint32(1)<<h.PrefixBits {
		return ErrBucketCountMismatch
	}

	if h.DirectoryOffset != HeaderSize ||
		h.DirectorySize != (uint64(h.BucketCount)+1)*4 {
		return ErrInvalidDirectoryLayout
	}

	if h.PayloadOffset != h.DirectoryOffset+h.DirectorySize {
		return ErrInvalidPayloadOffset
	}

	if h.PayloadOffset > uint64(limits.MaxSegmentFileSize) ||
		h.PayloadSize > uint64(limits.MaxSegmentFileSize)-h.PayloadOffset {
		return ErrSegmentSizeLimit
	}

	return nil
}

// DirectoryHash returns the SHA-256 checksum for serialized directory bytes.
func DirectoryHash(b []byte) [32]byte {
	return sha256.Sum256(b)
}
