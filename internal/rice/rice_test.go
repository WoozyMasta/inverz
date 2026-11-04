// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package rice

import (
	"errors"
	"testing"
)

var benchmarkEncoded []byte

// TestEncodePreservesCanonicalBytes compares optimized output with a reference bit-at-a-time encoder.
func TestEncodePreservesCanonicalBytes(t *testing.T) {
	values := []uint32{0, 1, 2, 3, 127, 128, 65535, 1 << 20}
	for _, k := range []uint8{0, 1, 7, 17, 31} {
		got, err := Encode(values, k)
		if err != nil {
			t.Fatal(err)
		}

		want := referenceEncode(values, k)
		if string(got) != string(want) {
			t.Fatalf("k=%d encoded bytes differ: got %x, want %x", k, got, want)
		}
	}
}

// TestAppendEncodeReusesCapacity verifies reusable output and canonical bytes.
func TestAppendEncodeReusesCapacity(t *testing.T) {
	values := []uint32{0, 1, 2, 3, 127, 128, 65535, 1 << 20}
	dst := make([]byte, 3, 128)
	encoded, err := AppendEncode(dst, values, 17)
	if err != nil {
		t.Fatal(err)
	}
	if &encoded[:1][0] != &dst[:1][0] {
		t.Fatal("AppendEncode did not reuse dst backing array")
	}
	want, err := Encode(values, 17)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(want) {
		t.Fatalf("AppendEncode() = %x, want %x", encoded, want)
	}
}

// TestIncrementalEncoderMatchesBatchEncoding checks output identity across Append calls.
func TestIncrementalEncoderMatchesBatchEncoding(t *testing.T) {
	values := []uint32{0, 1, 2, 3, 127, 128, 65535, 1 << 20}
	for _, k := range []uint8{0, 1, 7, 17, 31} {
		encoder, err := NewEncoder(k)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			encoder.Append(value)
		}

		want, err := Encode(values, k)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoder.Bytes()) != string(want) {
			t.Fatalf("k=%d incremental bytes = %x, want %x", k, encoder.Bytes(), want)
		}
	}
}

// TestRoundTrip checks that encoding and decoding preserve representative values for every supported k profile.
func TestRoundTrip(t *testing.T) {
	for _, k := range []uint8{0, 1, 7, 17, 31} {
		values := []uint32{0, 1, 2, 3, 127, 128, 65535, 1 << 20}
		data, err := Encode(values, k)
		if err != nil {
			t.Fatal(err)
		}

		got, err := Decode(data, 8, k)
		if err != nil {
			t.Fatal(err)
		}
		for i := range values {
			if got[i] != values[i] {
				t.Fatalf("k=%d index=%d got=%d want=%d", k, i, got[i], values[i])
			}
		}
	}
}

// TestDecodeRejectsTruncation checks preflight count bounds and truncated unary/remainder paths.
func TestDecodeRejectsTruncation(t *testing.T) {
	if _, err := Decode(nil, 1, 17); !errors.Is(err, ErrCountExceedsData) {
		t.Fatalf("impossible count error = %v, want ErrCountExceedsData", err)
	}
	if _, err := Decode(nil, ^uint32(0), 17); !errors.Is(err, ErrCountExceedsData) {
		t.Fatalf("oversized count error = %v, want ErrCountExceedsData", err)
	}

	quotient, err := NewDecoder([]byte{0}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := quotient.Next(); !errors.Is(err, ErrTruncatedQuotient) {
		t.Fatalf("truncated quotient error = %v, want ErrTruncatedQuotient", err)
	}

	remainder, err := NewDecoder([]byte{0x01}, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := remainder.Next(); !errors.Is(err, ErrTruncatedRemainder) {
		t.Fatalf("truncated remainder error = %v, want ErrTruncatedRemainder", err)
	}
}

// TestRejectsInvalidParameters checks the supported remainder-width boundary.
func TestRejectsInvalidParameters(t *testing.T) {
	if _, err := Encode(nil, 32); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("Encode() error = %v, want ErrInvalidParameter", err)
	}
	if _, err := NewDecoder(nil, 0, 32); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("NewDecoder() error = %v, want ErrInvalidParameter", err)
	}
}

// TestDecoderRejectsValueOverflow checks that decoded values cannot exceed uint32.
func TestDecoderRejectsValueOverflow(t *testing.T) {
	decoder, err := NewDecoder([]byte{0x20, 0, 0, 0, 0}, 1, 31)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decoder.Next(); !errors.Is(err, ErrValueOverflow) {
		t.Fatalf("Next() error = %v, want ErrValueOverflow", err)
	}
}

// TestDecoderCanonicalEncoding checks accepted byte padding and rejects trailing or nonzero bits.
func TestDecoderCanonicalEncoding(t *testing.T) {
	encoded, err := Encode([]uint32{1}, 1)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string // Case label used in test output.
		data []byte // Encoded stream supplied to the decoder.
		want bool   // Whether the stream is canonical.
	}{
		{name: "valid zero padding", data: encoded, want: true},
		{name: "nonzero padding", data: []byte{encoded[0] | 1}},
		{name: "extra byte", data: append(append([]byte(nil), encoded...), 0)},
		{name: "no values with data", data: []byte{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := uint32(1)
			if tc.name == "no values with data" {
				count = 0
			}
			decoder, err := NewDecoder(tc.data, count, 1)
			if err == nil {
				_, _, err = decoder.Next()
			}
			wantErr := error(nil)
			switch tc.name {
			case "nonzero padding":
				wantErr = ErrNonzeroPadding
			case "extra byte":
				wantErr = ErrTrailingData
			case "no values with data":
				wantErr = ErrDataWithoutValues
			}
			if tc.want && err != nil {
				t.Fatalf("valid stream rejected: %v", err)
			}
			if !tc.want && !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
		})
	}
}

// TestDecoderStreamsValues checks Next returns values one at a time and then reports exhaustion.
func TestDecoderStreamsValues(t *testing.T) {
	want := []uint32{0, 1, 2, 3, 127, 128, 65535, 1 << 20}
	data, err := Encode(want, 17)
	if err != nil {
		t.Fatal(err)
	}

	decoder, err := NewDecoder(data, 8, 17)
	if err != nil {
		t.Fatal(err)
	}

	for i, expected := range want {
		got, ok, err := decoder.Next()
		if err != nil || !ok || got != expected {
			t.Fatalf("Next() %d = %d, %t, %v; want %d", i, got, ok, err, expected)
		}
	}
	if _, ok, err := decoder.Next(); err != nil || ok {
		t.Fatalf("Next() after end = %t, %v", ok, err)
	}
}

// FuzzRiceDecoder checks arbitrary bounded streams without panicking or overrunning input.
func FuzzRiceDecoder(f *testing.F) {
	f.Add([]byte{0x80}, uint32(1), uint8(0))
	f.Add([]byte{0xff}, uint32(8), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, count uint32, k uint8) {
		if len(data) > 1<<16 {
			t.Skip()
		}

		count %= 4097
		decoder, err := NewDecoder(data, count, k)
		if err != nil {
			return
		}

		for {
			_, ok, err := decoder.Next()
			if err != nil || !ok {
				return
			}
		}
	})
}

// BenchmarkDecode measures the slice-materializing convenience decoder.
func BenchmarkDecode(b *testing.B) {
	values := make([]uint32, 8000)
	for i := range values {
		values[i] = uint32(i * 170)
	}

	data, err := Encode(values, 17)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Decode(data, 8000, 17); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRiceAppend measures the incremental encoder used by ordered generation.
func BenchmarkRiceAppend(b *testing.B) {
	values := make([]uint32, 1<<16)
	for i := range values {
		values[i] = uint32(i * 7919 % 262144)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(values) * 4))
	b.ResetTimer()
	for b.Loop() {
		encoder, err := NewEncoder(17)
		if err != nil {
			b.Fatal(err)
		}
		for _, value := range values {
			encoder.Append(value)
		}
		benchmarkEncoded = encoder.Bytes()
	}
}

// BenchmarkRiceBatch measures the batch encoder used as the compatibility reference.
func BenchmarkRiceBatch(b *testing.B) {
	values := make([]uint32, 1<<16)
	for i := range values {
		values[i] = uint32(i * 7919 % 262144)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(values) * 4))
	b.ResetTimer()
	for b.Loop() {
		encoded, err := AppendEncode(benchmarkEncoded[:0], values, 17)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkEncoded = encoded
	}
}

// referenceEncode implements the bit-at-a-time definition used to verify the optimized writer.
func referenceEncode(values []uint32, k uint8) []byte {
	var data []byte
	var used uint8
	writeBit := func(value bool) {
		if used == 0 {
			data = append(data, 0)
		}
		if value {
			data[len(data)-1] |= 1 << (7 - used)
		}
		used = (used + 1) % 8
	}

	for _, value := range values {
		for quotient := value >> k; quotient > 0; quotient-- {
			writeBit(false)
		}
		writeBit(true)
		for i := int(k) - 1; i >= 0; i-- {
			writeBit(value&(1<<i) != 0)
		}
	}

	return data
}
