// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package rice encodes and decodes unsigned integers with Rice coding.
package rice

import "fmt"

// zeroBytes supplies aligned unary zero runs without allocating a buffer per value.
var zeroBytes [1024]byte

// Decoder reads a fixed number of Rice-coded values without allocating a slice.
type Decoder struct {
	reader bitReader // Source bit stream.
	count  uint32    // Number of values declared by the caller.
	read   uint32    // Number of values already decoded.
	k      uint8     // Rice remainder width.
	done   bool      // Whether all values and padding have been validated.
}

// Encoder incrementally encodes values into one canonical Rice stream.
type Encoder struct {
	writer bitWriter // Current bit stream state.
	k      uint8     // Remainder width shared by every encoded value.
}

// bitWriter appends bits in the canonical Rice stream order.
type bitWriter struct {
	bytes    []byte // Encoded output bytes.
	buffer   uint64 // Pending bits stored in the low used bits.
	used     uint8  // Number of pending bits not yet emitted as whole bytes.
	returned bool   // Whether Bytes appended a padded snapshot byte.
}

// bitReader reads a Rice stream from its most-significant bit onward.
type bitReader struct {
	data []byte // Encoded input bytes.
	pos  uint64 // Current bit offset.
}

// Encode encodes values and pads the final byte with zero bits.
func Encode(values []uint32, k uint8) ([]byte, error) {
	return AppendEncode(nil, values, k)
}

// AppendEncode encodes values into dst, reusing its capacity when possible, and pads the final byte.
func AppendEncode(dst []byte, values []uint32, k uint8) ([]byte, error) {
	if k > 31 {
		return nil, fmt.Errorf("rice parameter %d: %w", k, ErrInvalidParameter)
	}

	w := bitWriter{bytes: dst[:0]}
	for _, value := range values {
		w.rice(value, k)
	}

	return w.Bytes(), nil
}

// NewEncoder creates a stream encoder for one supported Rice remainder width.
func NewEncoder(k uint8) (Encoder, error) {
	if k > 31 {
		return Encoder{}, fmt.Errorf("rice parameter %d: %w", k, ErrInvalidParameter)
	}

	return Encoder{k: k}, nil
}

// Append adds one value to the stream without finalizing its current byte.
func (e *Encoder) Append(value uint32) {
	e.writer.rice(value, e.k)
}

// Bytes returns the encoded stream with canonical zero padding in its final byte.
func (e *Encoder) Bytes() []byte {
	return e.writer.Bytes()
}

// NewDecoder validates bounds and creates a decoder for one canonical stream.
func NewDecoder(data []byte, count uint32, k uint8) (*Decoder, error) {
	if k > 31 {
		return nil, fmt.Errorf("rice parameter %d: %w", k, ErrInvalidParameter)
	}

	bitCount := uint64(len(data)) * 8
	// Each value needs at least one unary terminator and k remainder bits.
	if uint64(count)*(uint64(k)+1) > bitCount {
		return nil, fmt.Errorf("%w: %d values in %d bytes", ErrCountExceedsData, count, len(data))
	}

	d := &Decoder{reader: bitReader{data: data}, count: count, k: k}
	if count == 0 {
		if len(data) != 0 {
			return nil, ErrDataWithoutValues
		}

		d.done = true
	}

	return d, nil
}

// Decode materializes all decoded values in a slice.
func Decode(data []byte, count uint32, k uint8) ([]uint32, error) {
	d, err := NewDecoder(data, count, k)
	if err != nil {
		return nil, err
	}

	values := make([]uint32, 0, count)
	for {
		value, ok, err := d.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return values, nil
		}

		values = append(values, value)
	}
}

// Next returns the next value, or false when the declared stream is exhausted.
func (d *Decoder) Next() (uint32, bool, error) {
	if d.done {
		return 0, false, nil
	}

	var q uint32
	for {
		b, ok := d.reader.bit()
		if !ok {
			return 0, false, ErrTruncatedQuotient
		}
		if b {
			break
		}
		// Reject before incrementing so malformed unary input cannot wrap the quotient.
		if q == ^uint32(0) {
			return 0, false, ErrQuotientOverflow
		}
		q++
	}

	var rem uint32
	for range d.k {
		b, ok := d.reader.bit()
		if !ok {
			return 0, false, ErrTruncatedRemainder
		}
		rem <<= 1
		if b {
			rem++
		}
	}

	// Check the shifted quotient before combining it with the remainder to prevent uint32 truncation.
	if q > (^uint32(0) >> d.k) {
		return 0, false, ErrValueOverflow
	}

	value := q<<d.k | rem
	d.read++
	if d.read == d.count {
		// Validate padding only after the declared values are consumed to reject appended data.
		if err := d.validatePadding(); err != nil {
			return 0, false, err
		}
		d.done = true
	}

	return value, true, nil
}

// validatePadding accepts only up to seven zero bits needed to finish the final byte.
func (d *Decoder) validatePadding() error {
	remaining := d.reader.remaining()
	// A full remaining byte is appended data; canonical padding only completes the current byte.
	if remaining > 7 {
		return ErrTrailingData
	}

	for range remaining {
		b, _ := d.reader.bit()
		if b {
			return ErrNonzeroPadding
		}
	}

	return nil
}

// Bytes returns the stream with canonical zero padding in its final byte.
func (w *bitWriter) Bytes() []byte {
	if w.used > 0 && !w.returned {
		w.bytes = append(w.bytes, byte(w.buffer<<(8-w.used))) // #nosec G115 -- used bounds the value to one byte.
		w.returned = true
	}

	return w.bytes
}

// removePadding drops the snapshot byte before the stream is extended again.
func (w *bitWriter) removePadding() {
	if w.returned {
		w.bytes = w.bytes[:len(w.bytes)-1]
		w.returned = false
	}
}

// zeroBits writes unary zeros while appending aligned runs in chunks.
func (w *bitWriter) zeroBits(count uint32) {
	w.removePadding()
	if count == 0 {
		return
	}

	if w.used != 0 {
		available := uint32(8 - w.used)
		written := min(count, available)
		w.bits(0, uint8(written))
		count -= written
	}

	// Append whole shared blocks directly so long unary quotients cost fewer append operations.
	for count >= uint32(len(zeroBytes))*8 {
		w.bytes = append(w.bytes, zeroBytes[:]...)
		count -= uint32(len(zeroBytes)) * 8
	}

	wholeBytes := count / 8
	if wholeBytes > 0 {
		w.bytes = append(w.bytes, zeroBytes[:wholeBytes]...)
		count -= wholeBytes * 8
	}

	if count > 0 {
		w.bits(0, uint8(count)) // #nosec G115 -- fewer than eight remainder bits remain.
	}
}

// rice appends the unary quotient, terminator, and remainder with one packed code write.
func (w *bitWriter) rice(value uint32, k uint8) {
	w.zeroBits(value >> k)
	w.bits(value|uint32(1)<<k, k+1)
}

// bits writes the low count bits of value in most-significant-bit-first order.
func (w *bitWriter) bits(value uint32, count uint8) {
	if count == 0 {
		return
	}

	w.removePadding()
	mask := (uint64(1) << count) - 1
	w.buffer = w.buffer<<count | uint64(value)&mask
	w.used += count
	for w.used >= 8 {
		w.used -= 8
		w.bytes = append(w.bytes, byte(w.buffer>>w.used)) // #nosec G115 -- the flushed chunk is exactly eight bits.
		w.buffer &= (uint64(1) << w.used) - 1
	}
}

// bit reads one bit and reports whether input remains.
func (r *bitReader) bit() (bool, bool) {
	if r.pos >= uint64(len(r.data))*8 {
		return false, false
	}

	v := r.data[r.pos/8]&(1<<(7-r.pos%8)) != 0
	r.pos++

	return v, true
}

// remaining returns the number of unread bits.
func (r *bitReader) remaining() uint64 {
	return uint64(len(r.data))*8 - r.pos
}
