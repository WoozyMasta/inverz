// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/woozymasta/inverz/internal/appdirs"
	"github.com/woozymasta/inverz/internal/digest"
	"github.com/woozymasta/inverz/internal/steamid"
)

func TestHashBatchAliasesAndFormats(t *testing.T) {
	paths := appdirs.Paths{DataRoot: t.TempDir(), DotEnv: t.TempDir() + "/.env"}
	var stdout, stderr bytes.Buffer
	code := runWithPaths(context.Background(), []string{
		"hash", "-f", "text", "dz", "76561197960265728", "76561197960265729",
	}, &stdout, &stderr, paths)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run() = %d, stderr=%q", code, stderr.String())
	}
	if got := strings.Split(strings.TrimSpace(stdout.String()), "\n"); len(got) != 2 || got[0] == got[1] {
		t.Fatalf("hash output = %q", stdout.String())
	}

	stdout.Reset()
	code = runWithPaths(context.Background(), []string{
		"hash", "-f", "json", "be", "76561197960265728", "76561197960265729",
	}, &stdout, &stderr, paths)
	if code != 0 ||
		!strings.Contains(stdout.String(), "battleye_guid") ||
		strings.Contains(stdout.String(), "BE") {
		t.Fatalf("Battleye JSON output = %q, code %d", stdout.String(), code)
	}
}

func TestByteSizeUnmarshalText(t *testing.T) {
	for input, want := range map[string]ByteSize{
		"256MiB": 256 << 20,
		"512MiB": 512 << 20,
		"1GiB":   1 << 30,
		"4096":   4096,
	} {
		var got ByteSize
		if err := got.UnmarshalText([]byte(input)); err != nil || got != want {
			t.Errorf("UnmarshalText(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0", "1GB", "9223372036854775807GiB", "-1"} {
		var got ByteSize
		if err := got.UnmarshalText([]byte(input)); err == nil {
			t.Errorf("UnmarshalText(%q) unexpectedly succeeded with %d", input, got)
		}
	}
}

// TestProfileCommandWorkflow checks CLI output and dispatch over one small local profile.
func TestProfileCommandWorkflow(t *testing.T) {
	paths := appdirs.Paths{DataRoot: t.TempDir(), DotEnv: t.TempDir() + "/.env"}
	maximum := steamid.SteamIDBase + 15
	generateArgs := []string{
		"-d", paths.DataRoot, "generate", "--max-steamid", strconv.FormatUint(maximum, 10),
		"--prefix-bits", "4", "--segment-bits", "4", "dayz",
	}

	var stdout, stderr bytes.Buffer
	code := runWithPaths(context.Background(), generateArgs, &stdout, &stderr, paths)
	if code != 0 ||
		!strings.Contains(stdout.String(), "generated: dayz v1-p4-k3-s4") ||
		!strings.Contains(stdout.String(), "segments: 1/1") ||
		!strings.Contains(stdout.String(), "entries: 16") ||
		!strings.Contains(stdout.String(), "range: 76561197960265728..76561197960265743") ||
		!strings.Contains(stdout.String(), "size:") {
		t.Fatalf("generate returned %d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(stdout.String(), "\n"), "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Fatalf("generate output has trailing whitespace: %q", line)
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithPaths(context.Background(), generateArgs, &stdout, &stderr, paths)
	if code != 0 ||
		stdout.String() != "status: skipped\nalgorithm: dayz\nprofile: v1-p4-k3-s4\n"+
			"reason: all requested segments already exist\naction: use --force to rebuild\n" ||
		stderr.Len() != 0 {
		t.Fatalf("repeat generate returned %d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}

	forceArgs := slices.Concat(generateArgs[:len(generateArgs)-1], []string{"--force", "dayz"})
	stdout.Reset()
	stderr.Reset()
	code = runWithPaths(context.Background(), forceArgs, &stdout, &stderr, paths)
	if code != 0 || !strings.Contains(stdout.String(), "generated: dayz v1-p4-k3-s4") {
		t.Fatalf("forced generate returned %d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "avg rate:") ||
		strings.Contains(stdout.String(), "max rate:") || strings.Contains(stdout.String(), "buckets/s") {
		t.Fatalf("generation summary has stale throughput fields: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	verifyArgs := []string{
		"-d", paths.DataRoot, "verify", "--prefix-bits", "4", "--segment-bits", "4", "--rice-k", "3", "dayz",
	}
	code = runWithPaths(context.Background(), verifyArgs, &stdout, &stderr, paths)
	if code != 0 || stdout.String() != "status: verified\nalgorithm: dayz\nprofile: v1-p4-k3-s4\nsegments: 1\n" {
		t.Fatalf("verify returned %d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "verify: completed,") {
		t.Fatalf("verify printed a duplicate progress summary: %q", stderr.String())
	}

	target := steamid.SteamIDBase + 9
	dayzID := digest.FormatDayZ(digest.DayZ(target))
	missingID := digest.FormatDayZ([32]byte{})
	for id := steamid.SteamIDBase; id <= maximum; id++ {
		if digest.FormatDayZ(digest.DayZ(id)) == missingID {
			t.Fatal("query fixture collides with the missing identifier")
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithPaths(context.Background(), []string{
		"-d", paths.DataRoot, "query", "-f", "text", "--prefix-bits", "4", "--segment-bits", "4", "dz",
		"--no-cache", "--", missingID, dayzID,
	}, &stdout, &stderr, paths)
	if code != partialFailureExitCode || stdout.String() != "-\n"+strconv.FormatUint(target, 10)+"\n" ||
		!strings.Contains(stderr.String(), "query input 1") {
		t.Fatalf("partial text query = code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithPaths(context.Background(), []string{
		"query", "-p", "4", "-s", "4", "-f", "json", "--no-cache", "dz", "--", dayzID, missingID,
	}, &stdout, &stderr, paths)
	var results []queryResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("decode partial JSON response: %v; output=%q", err, stdout.String())
	}
	if code != partialFailureExitCode ||
		len(results) != 2 ||
		results[0].SteamID == nil || *results[0].SteamID != target ||
		results[1].SteamID != nil || results[1].Error == "" {
		t.Fatalf("partial JSON query = code %d, results %#v, stderr %q", code, results, stderr.String())
	}
}

// TestInvalidArgumentsReturnUsageExitCode checks the app's process-facing parse-error code.
func TestInvalidArgumentsReturnUsageExitCode(t *testing.T) {
	paths := appdirs.Paths{DataRoot: t.TempDir(), DotEnv: t.TempDir() + "/.env"}
	var stdout, stderr bytes.Buffer
	code := runWithPaths(context.Background(), []string{"hash", "dayz", "not-a-steamid"}, &stdout, &stderr, paths)
	if code != 2 {
		t.Fatalf("invalid argument exit code = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
