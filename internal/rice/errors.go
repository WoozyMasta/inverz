// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package rice

import "errors"

// Sentinel errors classify invalid Rice parameters and malformed bit streams.
var (
	// ErrInvalidParameter means the Rice remainder width exceeds the supported range.
	ErrInvalidParameter = errors.New("invalid rice parameter")
	// ErrCountExceedsData means the declared values cannot fit in the supplied encoded bits.
	ErrCountExceedsData = errors.New("rice value count cannot fit in encoded data")
	// ErrDataWithoutValues means a zero-value stream contains encoded bytes.
	ErrDataWithoutValues = errors.New("rice stream has data but no values")
	// ErrTruncatedQuotient means the stream ends before a unary quotient terminator.
	ErrTruncatedQuotient = errors.New("truncated Rice quotient")
	// ErrQuotientOverflow means a unary quotient exceeds the uint32 representation.
	ErrQuotientOverflow = errors.New("rice quotient overflow")
	// ErrTruncatedRemainder means the stream ends before all remainder bits are read.
	ErrTruncatedRemainder = errors.New("truncated Rice remainder")
	// ErrValueOverflow means the quotient and remainder do not fit in uint32.
	ErrValueOverflow = errors.New("rice value overflow")
	// ErrTrailingData means complete encoded bits remain after the declared values.
	ErrTrailingData = errors.New("rice stream contains trailing data")
	// ErrNonzeroPadding means final byte-alignment bits contain a one.
	ErrNonzeroPadding = errors.New("rice stream has non-zero padding")
)
