// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package inverz

import (
	"context"
	"errors"
	"testing"
)

func TestPublicProfileAndHashHelpers(t *testing.T) {
	profile := DefaultProfile()
	if got, want := profile.String(), "v1-p18-k17-s28"; got != want {
		t.Fatalf("DefaultProfile().String() = %q, want %q", got, want)
	}
	if got, err := ParseProfile(profile.String()); err != nil || got != profile {
		t.Fatalf("ParseProfile() = %#v, %v; want %#v", got, err, profile)
	}
	if got, err := profile.Tag(AlgorithmDayZ); err != nil || got != "dayz-v1-p18-k17-s28" {
		t.Fatalf("Profile.Tag() = %q, %v", got, err)
	}
	if got := DayZID(76561197960265728); got != "JCNShusG41ALBHLwqmAu5wINMUMVGiGONjUqccYdh4I=" {
		t.Fatalf("DayZID() = %q", got)
	}
	if got := BattlEyeGUID(76561197960265728); got != "4fc867abf98b934e9e7eeaf15170258c" {
		t.Fatalf("BattlEyeGUID() = %q", got)
	}
}

func TestPublicBuildOpenResolveAndInspect(t *testing.T) {
	root := t.TempDir()
	selected := Profile{Version: 1, PrefixBits: 4, RiceK: 3, SegmentBits: 4}
	rangeValue := SteamIDRange{Min: 76561197960265728, Max: 76561197960265743}
	var progressEvents []ProgressEvent
	result, err := Build(context.Background(), root, BuildOptions{
		Algorithm: AlgorithmDayZ,
		Profile:   selected,
		Range:     rangeValue,
		Jobs:      1,
		OnProgress: func(event ProgressEvent) {
			progressEvents = append(progressEvents, event)
		},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if result.Entries != 16 || result.Segments != 1 || result.SizeBytes == 0 || len(progressEvents) == 0 {
		t.Fatalf("Build() result = %#v, progress events %d", result, len(progressEvents))
	}
	if progressEvents[0].State != "started" ||
		progressEvents[0].Unit != "items" ||
		progressEvents[len(progressEvents)-1].State != "completed" {
		t.Fatalf("Build() lifecycle events = %#v", progressEvents)
	}

	index, err := Open(root, AlgorithmDayZ, selected)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	resolved, err := index.Resolve(context.Background(), DayZID(rangeValue.Min+7))
	if err != nil || resolved.SteamID != rangeValue.Min+7 || resolved.Source != "index" {
		t.Fatalf("Resolve() = %#v, %v", resolved, err)
	}
	if err := index.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := index.Resolve(context.Background(), DayZID(rangeValue.Min)); !errors.Is(err, ErrIndexClosed) {
		t.Fatalf("Resolve() after Close() error = %v, want ErrIndexClosed", err)
	}

	info, err := Inspect(root, AlgorithmDayZ, selected)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	if info.Algorithm != "dayz" ||
		info.Profile != selected ||
		info.EntryCount != "16" ||
		info.SizeBytes != result.SizeBytes {
		t.Fatalf("Inspect() = %#v", info)
	}
}

func TestPublicErrorsAreClassifiable(t *testing.T) {
	root := t.TempDir()
	if _, err := Build(context.Background(), root, BuildOptions{
		Algorithm: Algorithm(99),
		Profile:   DefaultProfile(),
		Range:     SteamIDRange{Min: 76561197960265728, Max: 76561197960265728},
	}); !errors.Is(err, ErrInvalidAlgorithm) {
		t.Fatalf("Build() invalid algorithm error = %v", err)
	}

	var nilContext context.Context
	if _, err := Build(nilContext, root, BuildOptions{}); !errors.Is(err, ErrNilContext) {
		t.Fatalf("Build() nil context error = %v", err)
	}
	if err := ValidateIdentifier(AlgorithmDayZ, "not-a-guid"); !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("ValidateIdentifier() error = %v", err)
	}
}
