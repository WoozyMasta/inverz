// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/woozymasta/inverz"
	"github.com/woozymasta/inverz/internal/steamid"
	"github.com/woozymasta/inverz/oci"
)

// hashResult is the stable JSON schema for one direct identifier calculation.
type hashResult struct {
	DayZID       string `json:"dayz_id,omitempty"`
	BattleyeGUID string `json:"battleye_guid,omitempty"`
	SteamID      uint64 `json:"steam_id"`
}

// writeHashResults emits either ordered plain identifiers or a JSON result array.
func writeHashResults(writer io.Writer, format string, items []hashResult, dayz bool) error {
	if format == "json" {
		return encodeJSON(writer, items)
	}

	for _, item := range items {
		value := item.BattleyeGUID
		if dayz {
			value = item.DayZID
		}
		if _, err := fmt.Fprintln(writer, value); err != nil {
			return err
		}
	}

	return nil
}

// writeQueryResults emits one line per input or a single ordered JSON array.
func writeQueryResults(writer io.Writer, format string, results []queryResult) error {
	if format == "json" {
		return encodeJSON(writer, results)
	}

	for _, result := range results {
		if result.SteamID == nil {
			if _, err := fmt.Fprintln(writer, "-"); err != nil {
				return err
			}

			continue
		}

		if _, err := fmt.Fprintln(writer, *result.SteamID); err != nil {
			return err
		}
	}

	return nil
}

// printEstimate formats the requested generator estimate without creating index files.
func printEstimate(writer io.Writer, algorithm inverz.Algorithm, p inverz.Profile, estimate inverz.EstimateResult) error {
	return writeKeyValues(writer, [][2]string{
		{"algorithm", algorithm.String()},
		{"profile", p.String()},
		{"SteamID range", fmt.Sprintf("%d..%d", estimate.Range.Min, estimate.Range.Max)},
		{"entry count", strconv.FormatUint(estimate.Entries, 10)},
		{"segment count", strconv.Itoa(estimate.Segments)},
		{"expected candidates", fmt.Sprintf("%.2f", estimate.ExpectedCandidates)},
		{"estimated bytes", strconv.FormatUint(estimate.ExpectedBytes, 10)},
		{"expected RAM", strconv.FormatUint(estimate.ExpectedRAMBytes, 10)},
		{"peak RAM", strconv.FormatUint(estimate.PeakRAMBytes, 10)},
		{"public full segments", strconv.FormatBool(estimate.Publishable)},
	})
}

// writeKeyValues prints report fields with one separator and no column padding.
func writeKeyValues(writer io.Writer, values [][2]string) error {
	for _, value := range values {
		if _, err := fmt.Fprintf(writer, "%s: %s\n", value[0], value[1]); err != nil {
			return err
		}
	}

	return nil
}

// printReleaseTable writes a deterministic plain-text summary of local profiles.
func printReleaseTable(writer io.Writer, releases []inverz.Release) error {
	rows := make([][]string, 1, 1+len(releases))
	rows[0] = []string{"TYPE", "PROFILE", "RANGE", "SEGMENTS", "SIZE", "STATE"}
	for _, item := range releases {
		rangeText := "-"
		if item.MinSteamID != "" {
			rangeText = item.MinSteamID + ".." + item.MaxSteamID
		}
		rows = append(rows, []string{
			item.Algorithm,
			item.Profile,
			rangeText,
			strconv.Itoa(item.SegmentCount),
			formatBytes(item.SizeBytes),
			item.State,
		})
	}

	return writeAlignedTable(writer, rows)
}

// printArtifactTable writes a deterministic plain-text summary of remote artifacts.
func printArtifactTable(writer io.Writer, artifacts []oci.Artifact) error {
	rows := make([][]string, 1, 1+len(artifacts))
	rows[0] = []string{"TYPE", "PROFILE", "RANGE", "SEGMENTS", "SIZE", "TAG"}
	for _, item := range artifacts {
		rows = append(rows, []string{
			item.Algorithm.String(),
			item.Profile.String(),
			fmt.Sprintf("%d..%d", item.Range.Min, item.Range.Max),
			strconv.Itoa(item.Segments),
			formatBytes(item.SizeBytes),
			item.Tag,
		})
	}

	return writeAlignedTable(writer, rows)
}

// writeAlignedTable pads intermediate columns and leaves the final column unpadded.
func writeAlignedTable(writer io.Writer, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}

	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for column, value := range row {
			if len(value) > widths[column] {
				widths[column] = len(value)
			}
		}
	}

	for _, row := range rows {
		for column, value := range row {
			if column == len(row)-1 {
				if _, err := fmt.Fprintln(writer, value); err != nil {
					return err
				}
				continue
			}

			if _, err := fmt.Fprintf(writer, "%-*s  ", widths[column], value); err != nil {
				return err
			}
		}
	}

	return nil
}

// printInfo emits stable key/value sections from detailed profile metadata.
func printInfo(writer io.Writer, info inverz.Info) error {
	values := [][2]string{
		{"algorithm", info.Algorithm},
		{"profile", info.Profile.String()},
		{"index_format_version", strconv.FormatUint(uint64(info.IndexFormatVersion), 10)},
		{"prefix_bits", strconv.FormatUint(uint64(info.Profile.PrefixBits), 10)},
		{"rice_k", strconv.FormatUint(uint64(info.Profile.RiceK), 10)},
		{"segment_bits", strconv.FormatUint(uint64(info.Profile.SegmentBits), 10)},
		{"min_steamid", strconv.FormatUint(info.MinSteamID, 10)},
		{"max_steamid", strconv.FormatUint(info.MaxSteamID, 10)},
		{"min_account_id", strconv.FormatUint(uint64(info.MinAccountID), 10)},
		{"max_account_id", strconv.FormatUint(uint64(info.MaxAccountID), 10)},
		{"entry_count", info.EntryCount},
		{"segment_count", strconv.Itoa(info.SegmentCount)},
		{"size", strconv.FormatUint(info.SizeBytes, 10)},
		{"manifest_sha256", info.ManifestSHA256},
	}

	if info.Remote != nil {
		values = append(values,
			[2]string{"remote_reference", info.Remote.Reference},
			[2]string{"remote_manifest_digest", info.Remote.ManifestDigest},
			[2]string{"remote_last_checked", info.Remote.LastChecked},
			[2]string{"remote_last_updated", info.Remote.LastUpdated},
		)
	}

	if err := writeKeyValues(writer, values); err != nil {
		return err
	}
	if len(info.Segments) > 0 {
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
		}
	}
	for index, segment := range info.Segments {
		if index > 0 {
			if _, err := fmt.Fprintln(writer); err != nil {
				return err
			}
		}

		if err := writeKeyValues(writer, [][2]string{
			{"segment", segment.File},
			{"sha256", segment.SHA256},
			{"size", strconv.FormatUint(segment.SizeBytes, 10)},
			{"index", strconv.FormatUint(uint64(segment.Index), 10)},
			{"min_account_id", strconv.FormatUint(uint64(segment.MinAccountID), 10)},
			{"max_account_id", strconv.FormatUint(uint64(segment.MaxAccountID), 10)},
		}); err != nil {
			return err
		}
	}

	return nil
}

// cliInfoJSON preserves the command's established flattened JSON shape over public metadata types.
func cliInfoJSON(info inverz.Info) map[string]any {
	value := map[string]any{
		"algorithm":            info.Algorithm,
		"profile":              info.Profile.String(),
		"index_format_version": info.IndexFormatVersion,
		"prefix_bits":          info.Profile.PrefixBits,
		"rice_k":               info.Profile.RiceK,
		"segment_bits":         info.Profile.SegmentBits,
		"min_steamid":          info.MinSteamID,
		"max_steamid":          info.MaxSteamID,
		"min_account_id":       info.MinAccountID,
		"max_account_id":       info.MaxAccountID,
		"entry_count":          info.EntryCount,
		"segment_count":        info.SegmentCount,
		"size":                 info.SizeBytes,
		"manifest_sha256":      info.ManifestSHA256,
		"segments":             info.Segments,
	}
	if info.Remote != nil {
		value["remote"] = info.Remote
	}

	return value
}

// encodeJSON writes one complete JSON value followed by a newline.
func encodeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	return encoder.Encode(value)
}

// printBuildSummary writes the completed profile details as plain key/value lines.
func printBuildSummary(
	writer io.Writer,
	algorithm inverz.Algorithm,
	profile inverz.Profile,
	namespace *GenerateNamespace,
	result inverz.BuildResult,
	elapsed time.Duration,
	generatedSegments uint64,
	averageRate float64,
) error {
	segmentsPerID := uint64(1) << profile.SegmentBits
	firstSegment := (namespace.MinSteamID - steamid.SteamIDBase) / segmentsPerID
	lastSegment := (namespace.MaxSteamID - steamid.SteamIDBase) / segmentsPerID
	totalSegments := lastSegment - firstSegment + 1
	values := [][2]string{
		{"generated", fmt.Sprintf("%s %s", algorithm, profile.String())},
		{"time", elapsed.Round(time.Millisecond).String()},
		{"avg rate", formatIDRate(averageRate)},
		{"segments", fmt.Sprintf("%d/%d", generatedSegments, totalSegments)},
		{"entries", strconv.FormatUint(result.Entries, 10)},
		{"range", fmt.Sprintf("%d..%d", namespace.MinSteamID, namespace.MaxSteamID)},
		{"size", formatBytes(result.SizeBytes)},
	}
	return writeKeyValues(writer, values)
}

// formatIDRate scales generated AccountID throughput with decimal SI prefixes.
func formatIDRate(rate float64) string {
	units := [...]string{"", "K", "M", "G", "T"}
	unit := 0
	for rate >= 1000 && unit < len(units)-1 {
		rate /= 1000
		unit++
	}

	return fmt.Sprintf("%.1f %sIDs/s", rate, units[unit])
}
