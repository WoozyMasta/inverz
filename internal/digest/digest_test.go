// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package digest

import (
	"errors"
	"testing"
)

// TestGoldenAndRoundTrip checks protocol vectors and both parser/formatter pairs.
func TestGoldenAndRoundTrip(t *testing.T) {
	const id = uint64(76561197960265728)
	if got := FormatDayZ(DayZ(id)); got != "JCNShusG41ALBHLwqmAu5wINMUMVGiGONjUqccYdh4I=" {
		t.Fatalf("DayZ = %q", got)
	}
	if got := FormatBattleye(Battleye(id)); got != "4fc867abf98b934e9e7eeaf15170258c" {
		t.Fatalf("BattlEye = %q", got)
	}

	d, err := ParseDayZ(FormatDayZ(DayZ(id)))
	if err != nil || d != DayZ(id) {
		t.Fatalf("DayZ parse: %v", err)
	}

	b, err := ParseBattleye("4FC867ABF98B934E9E7EEAF15170258C")
	if err != nil || b != Battleye(id) {
		t.Fatalf("BattlEye parse: %v", err)
	}
}

// TestParseDayZRejectsNewlinesAndNoncanonicalLength checks ignored line breaks and invalid padding shapes.
func TestParseDayZRejectsNewlinesAndNoncanonicalLength(t *testing.T) {
	valid := FormatDayZ(DayZ(76561197960265728))
	for _, input := range []string{
		valid[:20] + "\n" + valid[20:],
		valid[:len(valid)-1],
		valid + "\n",
		valid[:len(valid)-1] + "A",
	} {
		if _, err := ParseDayZ(input); !errors.Is(err, ErrInvalidDayZGUID) {
			t.Fatalf("malformed DayZ error = %v, want ErrInvalidDayZGUID", err)
		}
	}
}

// TestParseBattleyeRejectsMalformedInput checks that malformed hex has a classifiable error.
func TestParseBattleyeRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"", "abc", "00112233445566778899aabbccddeefg"} {
		if _, err := ParseBattleye(input); !errors.Is(err, ErrInvalidBattleyeGUID) {
			t.Fatalf("malformed BattlEye error = %v, want ErrInvalidBattleyeGUID", err)
		}
	}
}

// BenchmarkDayZ measures the allocation-free protocol digest implementation.
func BenchmarkDayZ(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DayZ(uint64(76561197960265728 + i))
	}
}

// BenchmarkBattleye measures the allocation-free protocol digest implementation.
func BenchmarkBattleye(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Battleye(uint64(76561197960265728 + i))
	}
}
