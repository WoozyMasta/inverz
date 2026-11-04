// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/woozymasta/inverz"
)

// DayZValue validates one DayZ identifier through the core digest parser.
type DayZValue struct {
	Input string
}

// UnmarshalFlag parses a padded DayZ Base64URL identifier.
func (v *DayZValue) UnmarshalFlag(input string) error {
	if err := inverz.ValidateIdentifier(inverz.AlgorithmDayZ, input); err != nil {
		return err
	}
	v.Input = input
	return nil
}

// MarshalFlag returns the supplied identifier for help and completion rendering.
func (v DayZValue) MarshalFlag() (string, error) {
	return v.Input, nil
}

// BattleyeValue validates one BattlEye GUID through the core digest parser.
type BattleyeValue struct {
	Input string
}

// UnmarshalFlag parses one hexadecimal BattlEye GUID.
func (v *BattleyeValue) UnmarshalFlag(input string) error {
	if err := inverz.ValidateIdentifier(inverz.AlgorithmBattlEye, input); err != nil {
		return err
	}
	v.Input = input
	return nil
}

// MarshalFlag returns the supplied GUID for help and completion rendering.
func (v BattleyeValue) MarshalFlag() (string, error) {
	return v.Input, nil
}

// ByteSize parses a non-zero byte count with optional binary KiB, MiB, or GiB units.
type ByteSize uint64

// UnmarshalText parses a byte count such as 512MiB for CLI configuration.
func (s *ByteSize) UnmarshalText(text []byte) error {
	value := strings.TrimSpace(string(text))
	unit := uint64(1)
	if parsed, ok := strings.CutSuffix(value, "KiB"); ok {
		value, unit = strings.TrimSpace(parsed), 1<<10
	} else if parsed, ok := strings.CutSuffix(value, "MiB"); ok {
		value, unit = strings.TrimSpace(parsed), 1<<20
	} else if parsed, ok := strings.CutSuffix(value, "GiB"); ok {
		value, unit = strings.TrimSpace(parsed), 1<<30
	}

	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == 0 || n > uint64(maxCacheBytes)/unit {
		return fmt.Errorf("invalid cache size %q: expected a positive byte count or KiB/MiB/GiB value", text)
	}

	*s = ByteSize(n * unit)
	return nil
}
