// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"crypto/sha256"

	"github.com/woozymasta/inverz/internal/prefix"
)

// dayZDecimalCursor hashes consecutive decimal IDs without formatting each ID from scratch.
type dayZDecimalCursor struct {
	digits [20]byte // Current uint64 decimal representation, right-aligned.
	start  int      // First occupied decimal byte.
}

// reset stores value as decimal ASCII for the next sequential hash.
func (c *dayZDecimalCursor) reset(value uint64) {
	c.start = len(c.digits)
	for value > 0 {
		c.start--
		c.digits[c.start] = byte('0' + value%10)
		value /= 10
	}
	if c.start == len(c.digits) {
		c.start--
		c.digits[c.start] = '0'
	}
}

// nextBucket hashes the current decimal ID, extracts its bucket, then advances by one.
func (c *dayZDecimalCursor) nextBucket(bits uint8) uint32 {
	digest := c.nextDigest()
	bucket, _ := prefix.Extract(digest[:], bits)

	return bucket
}

// nextDigest hashes the current decimal ID and advances by one.
func (c *dayZDecimalCursor) nextDigest() [32]byte {
	digest := sha256.Sum256(c.digits[c.start:])
	c.increment()

	return digest
}

// increment advances the decimal digits with carry and grows their width when needed.
func (c *dayZDecimalCursor) increment() {
	for i := len(c.digits) - 1; i >= c.start; i-- {
		if c.digits[i] < '9' {
			c.digits[i]++
			return
		}
		c.digits[i] = '0'
	}

	c.start--
	c.digits[c.start] = '1'
}
