// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import "errors"

var (
	// errInvalidRiceK reports a Rice parameter outside the supported range.
	errInvalidRiceK = errors.New("--rice-k must be in range 0..31")
)
