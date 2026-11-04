// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package digest computes and parses DayZ and BattlEye identifier digests.
package digest

import (
	"crypto/md5" // #nosec G501 - The BattlEye protocol specifies this digest.
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strings"
)

// DayZ computes SHA-256 over the decimal ASCII SteamID64.
func DayZ(id uint64) [32]byte {
	var buf [20]byte
	i := len(buf)

	// Fill backward to hash decimal ASCII directly without allocating a formatted string.
	for id >= 10 {
		i--
		buf[i] = byte('0' + id%10)
		id /= 10
	}
	i--
	buf[i] = byte('0' + id)

	return sha256.Sum256(buf[i:])
}

// Battleye computes the protocol-required MD5 digest.
func Battleye(id uint64) [16]byte {
	var input [10]byte
	input[0], input[1] = 'B', 'E'
	binary.LittleEndian.PutUint64(input[2:], id)

	// MD5 is required by the BattlEye identifier protocol.
	// #nosec G401 - The BattlEye protocol specifies this digest.
	return md5.Sum(input[:])
}

// FormatDayZ returns the padded Base64URL representation.
func FormatDayZ(d [32]byte) string {
	return base64.URLEncoding.EncodeToString(d[:])
}

// ParseDayZ parses a padded Base64URL digest.
func ParseDayZ(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 44 || s[43] != '=' || strings.ContainsAny(s, "\r\n") {
		return out, ErrInvalidDayZGUID
	}

	// Reject CR/LF because DecodeString ignores them; non-strict decoding preserves accepted padding-bit variants.
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil || len(b) != len(out) {
		return out, ErrInvalidDayZGUID
	}

	copy(out[:], b)
	return out, nil
}

// FormatBattleye returns lowercase hexadecimal.
func FormatBattleye(d [16]byte) string {
	return hex.EncodeToString(d[:])
}

// ParseBattleye parses case-insensitive hexadecimal BattlEye GUID.
func ParseBattleye(s string) ([16]byte, error) {
	var out [16]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(out) {
		return out, ErrInvalidBattleyeGUID
	}

	copy(out[:], b)
	return out, nil
}
