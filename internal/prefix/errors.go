// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package prefix

import "errors"

// ErrInvalidPrefixWidth means the requested prefix is empty, too wide, or exceeds the digest length.
var ErrInvalidPrefixWidth = errors.New("invalid digest prefix width")
