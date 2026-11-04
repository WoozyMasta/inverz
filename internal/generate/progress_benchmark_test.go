// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package generate

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/woozymasta/inverz/internal/segment"
	"github.com/woozymasta/inverz/internal/steamid"
)

// BenchmarkBuildProgress compares generation with progress disabled and with a no-op observer.
func BenchmarkBuildProgress(b *testing.B) {
	for _, benchmark := range []struct {
		name       string
		onProgress func(ProgressEvent)
	}{
		{name: "disabled"},
		{name: "observer", onProgress: func(ProgressEvent) {}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			root := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				config := Config{
					Context: context.Background(),
					Output:  filepath.Join(root, fmt.Sprintf("build-%d", i)),
					Range: steamid.Range{
						Min: steamid.SteamIDBase,
						Max: steamid.SteamIDBase + 65_535,
					},
					Jobs:        2,
					Algorithm:   segment.DayZ,
					PrefixBits:  4,
					RiceK:       3,
					RiceKSet:    true,
					SegmentBits: 16,
					OnProgress:  benchmark.onProgress,
				}
				if err := Build(config); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
