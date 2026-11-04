// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/woozymasta/inverz/internal/limits"
)

// Reader holds immutable segment metadata and a read-only file handle.
type Reader struct {
	file      *os.File // Open read-only segment descriptor.
	directory []uint32 // Per-bucket payload offsets, including the final end offset.
	Header    Header   // Parsed immutable segment metadata.
}

// Open validates header, file bounds, directory checksum and offsets.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}

	idx, err := openFile(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return idx, nil
}

// Close closes the underlying index file.
func (i *Reader) Close() error {
	return i.file.Close()
}

// ReadBucket reads exactly one bucket using one ReadAt call.
func (i *Reader) ReadBucket(bucket uint32) ([]byte, error) {
	return i.ReadBucketInto(bucket, nil)
}

// ReadBucketInto reads one bucket into dst, reusing its capacity when possible.
func (i *Reader) ReadBucketInto(bucket uint32, dst []byte) ([]byte, error) {
	if bucket >= i.Header.BucketCount {
		return nil, fmt.Errorf("bucket %d out of range", bucket)
	}

	start, end := uint64(i.directory[bucket]), uint64(i.directory[bucket+1])
	size := end - start
	maxInt := uint64(^uint(0) >> 1)
	if size > maxInt {
		return nil, ErrBucketLengthRange
	}

	bucketLength := int(size)
	if uint64(cap(dst)) < size {
		// The validated directory bounds each bucket,
		// so this allocation stays within the format limit.
		dst = make([]byte, bucketLength)
	} else {
		dst = dst[:bucketLength]
	}

	offset := i.Header.PayloadOffset + start
	if offset > math.MaxInt64 {
		return nil, ErrBucketOffsetRange
	}

	// #nosec G115 -- checked against MaxInt64 above.
	n, err := i.file.ReadAt(dst, int64(offset))
	if err != nil || n != len(dst) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}

		return nil, fmt.Errorf("read bucket: %w", err)
	}

	return dst, nil
}

// VerifyPayload checks the stored payload SHA-256 checksum.
func (i *Reader) VerifyPayload() error {
	h := sha256.New()
	if i.Header.PayloadOffset > math.MaxInt64 || i.Header.PayloadSize > math.MaxInt64 {
		return ErrPayloadOffsetRange
	}

	// Opening validates metadata and the directory only;
	// payload hashing stays explicit because it requires reading every encoded bucket from disk.
	if _, err := io.CopyN(h, io.NewSectionReader(
		i.file,
		int64(i.Header.PayloadOffset),
		int64(i.Header.PayloadSize),
	), int64(i.Header.PayloadSize),
	); err != nil { // #nosec G115 -- both conversions are checked above.
		return fmt.Errorf("hash payload: %w", err)
	}

	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	if sum != i.Header.PayloadSHA256 {
		return ErrPayloadChecksumMismatch
	}

	return nil
}

// openFile validates and indexes an already-open segment file.
func openFile(f *os.File) (*Reader, error) {
	var raw [HeaderSize]byte
	if _, err := f.ReadAt(raw[:], 0); err != nil {
		return nil, fmt.Errorf("read index header: %w", err)
	}

	h, err := ParseHeader(raw[:])
	if err != nil {
		return nil, err
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat index: %w", err)
	}
	if stat.Size() < 0 {
		return nil, ErrInvalidFileSize
	}
	if stat.Size() > limits.MaxSegmentFileSize {
		return nil, ErrSegmentFileSizeLimit
	}

	// #nosec G115 - negative sizes are rejected above.
	fileSize := uint64(stat.Size())
	if h.DirectoryOffset > fileSize ||
		h.DirectorySize > fileSize-h.DirectoryOffset ||
		h.PayloadOffset > fileSize ||
		h.PayloadSize != fileSize-h.PayloadOffset {
		return nil, ErrFileSizeMismatch
	}

	buf := make([]byte, h.DirectorySize)
	if h.DirectoryOffset > math.MaxInt64 {
		return nil, ErrDirectoryOffsetRange
	}
	// #nosec G115 -- checked against MaxInt64 above.
	if _, err := f.ReadAt(buf, int64(h.DirectoryOffset)); err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	if DirectoryHash(buf) != h.DirectorySHA256 {
		return nil, ErrDirectoryChecksumMismatch
	}

	// Read the complete directory once;
	// subsequent bucket reads need only two in-memory offsets and one positional file read.
	dir := make([]uint32, h.BucketCount+1)
	for i := range dir {
		offset := binary.LittleEndian.Uint32(buf[i*4:])
		// The initial offset anchors the first bucket; later offsets must be monotonic
		// so each bucket occupies a valid contiguous payload slice.
		if i == 0 && offset != 0 {
			return nil, ErrDirectoryMustStartAtZero
		}
		if i > 0 && offset < dir[i-1] {
			return nil, ErrDirectoryOffsetsNotMonotonic
		}
		if uint64(offset) > h.PayloadSize {
			return nil, ErrDirectoryOffsetExceedsPayload
		}

		dir[i] = offset
		if i > 0 {
			bucketSize := uint64(offset - dir[i-1])
			// Each encoded bucket needs its length prefix and must fit the decoder's
			// configured bound before any bucket data is read.
			if bucketSize < 4 || bucketSize > limits.MaxBucketByteSize {
				return nil, ErrBucketSizeOutOfBounds
			}
		}
	}

	if uint64(dir[len(dir)-1]) != h.PayloadSize {
		return nil, ErrDirectoryEndMismatch
	}

	return &Reader{file: f, Header: h, directory: dir}, nil
}
