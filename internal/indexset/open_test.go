// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package indexset

import (
	"errors"
	"testing"
)

// TestHashReaderProgressPreservesPartialBytes checks failure progress against bytes actually consumed.
func TestHashReaderProgressPreservesPartialBytes(t *testing.T) {
	wantErr := errors.New("reader failed")
	input := []byte("partial segment data")
	var reported []uint64
	_, size, err := hashReaderProgress(&failingReader{data: input, err: wantErr}, func(value uint64) {
		reported = append(reported, value)
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("hash error = %v, want %v", err, wantErr)
	}
	if size != uint64(len(input)) {
		t.Fatalf("bytes read = %d, want %d", size, len(input))
	}
	if len(reported) == 0 || reported[len(reported)-1] != size {
		t.Fatalf("reported progress = %v, want final value %d", reported, size)
	}
}

// failingReader returns its complete payload and a terminal error in one read.
type failingReader struct {
	data []byte
	err  error
}

// Read delivers the payload with the configured error so io.Copy retains its byte count.
func (r *failingReader) Read(p []byte) (int, error) {
	return copy(p, r.data), r.err
}
