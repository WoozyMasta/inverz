// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package oci publishes, discovers, and pulls Inverz indexes through OCI registries.
package oci

import (
	"context"
	"fmt"
	"strconv"

	"github.com/woozymasta/inverz"
	"github.com/woozymasta/inverz/internal/ocireg"
	internalprofile "github.com/woozymasta/inverz/internal/profile"
	"github.com/woozymasta/inverz/internal/store"
)

// Option configures registry authentication or transport behavior.
type Option func(*Client)

// Client performs OCI operations using configured registry transport options.
type Client struct {
	authConfig string // Docker-compatible authentication configuration path.
	plainHTTP  bool   // Whether unencrypted HTTP registries are allowed.
}

// PushOptions controls publication policy for partial final segments.
type PushOptions struct {
	OnProgress   func(inverz.ProgressEvent) // Optional callback for actual missing-blob transfers.
	AllowPartial bool                       // Permits publishing a range with incomplete aligned segments.
}

// Artifact describes one canonical remote index artifact without exposing ORAS types.
type Artifact struct {
	Tag            string              `json:"tag"             yaml:"tag"`             // Canonical OCI tag.
	ManifestDigest string              `json:"manifest_digest" yaml:"manifest_digest"` // OCI manifest digest.
	Range          inverz.SteamIDRange `json:"range"           yaml:"range"`           // Inclusive SteamID64 range.
	Entries        uint64              `json:"entries"         yaml:"entries"`         // Number of indexed identifiers.
	Segments       int                 `json:"segments"        yaml:"segments"`        // Number of segment layers.
	SizeBytes      uint64              `json:"size_bytes"      yaml:"size_bytes"`      // Aggregate segment size.
	Profile        inverz.Profile      `json:"profile"         yaml:"profile"`         // Immutable index profile.
	Algorithm      inverz.Algorithm    `json:"algorithm"       yaml:"algorithm"`       // Canonical digest algorithm.
}

// WithAuthConfig selects a Docker-compatible registry authentication file.
func WithAuthConfig(path string) Option {
	return func(client *Client) { client.authConfig = path }
}

// WithPlainHTTP allows registry traffic over unencrypted HTTP.
func WithPlainHTTP(allowed bool) Option {
	return func(client *Client) { client.plainHTTP = allowed }
}

// New creates a registry client with the supplied transport options.
func New(options ...Option) (*Client, error) {
	client := &Client{}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}

	return client, nil
}

// Pull downloads and atomically activates one selected artifact under the local index root.
func (c *Client) Pull(
	ctx context.Context,
	root, repository string,
	algorithm inverz.Algorithm,
	selected inverz.Profile,
) (string, error) {
	return c.PullWithProgress(ctx, root, repository, algorithm, selected, nil)
}

// PullWithProgress pulls an artifact and reports only bytes downloaded from the registry.
func (c *Client) PullWithProgress(
	ctx context.Context,
	root, repository string,
	algorithm inverz.Algorithm,
	selected inverz.Profile,
	onProgress func(inverz.ProgressEvent),
) (string, error) {
	if ctx == nil {
		return "", inverz.ErrNilContext
	}

	tag, err := selected.Tag(algorithm)
	if err != nil {
		return "", err
	}

	localStore, err := store.New(root)
	if err != nil {
		return "", err
	}

	return ocireg.Pull(
		ctx,
		repository+":"+tag,
		c.authConfig,
		ocireg.ExpectedIndex{Algorithm: algorithm.String(), Profile: toInternalProfile(selected)},
		c.plainHTTP,
		localStore,
		transferProgress(onProgress),
	)
}

// Push verifies and publishes one installed profile, returning its OCI manifest digest.
func (c *Client) Push(
	ctx context.Context,
	root, repository string,
	algorithm inverz.Algorithm,
	selected inverz.Profile,
	options PushOptions,
) (string, error) {
	if ctx == nil {
		return "", inverz.ErrNilContext
	}

	tag, err := selected.Tag(algorithm)
	if err != nil {
		return "", err
	}

	localStore, err := store.New(root)
	if err != nil {
		return "", err
	}

	set, err := localStore.OpenProfile(algorithm.String(), toInternalProfile(selected))
	if err != nil {
		return "", err
	}
	defer func() { _ = set.Close() }()

	return ocireg.Publish(
		ctx,
		repository+":"+tag,
		c.authConfig,
		c.plainHTTP, set,
		ocireg.PublishOptions{
			AllowPartial: options.AllowPartial,
			OnProgress:   transferProgress(options.OnProgress),
		},
	)
}

// transferProgress adapts internal OCI transfer events to the renderer-independent public event.
func transferProgress(onProgress func(inverz.ProgressEvent)) func(ocireg.ProgressEvent) {
	if onProgress == nil {
		return nil
	}

	return func(event ocireg.ProgressEvent) {
		processed := event.Bytes
		phase := event.Phase
		if phase == "" {
			phase = "transfer"
		}
		if event.State == "operation-started" {
			processed = 0
		}
		onProgress(inverz.ProgressEvent{
			State:     event.State,
			Unit:      "bytes",
			Phase:     phase,
			Segment:   event.Segment,
			Segments:  event.Segments,
			Reused:    event.Reused,
			Processed: processed,
			Total:     event.Total,
			Percent:   progressPercent(processed, event.Total),
		})
	}
}

// progressPercent computes a capped transfer percentage for public progress events.
func progressPercent(current, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	if current >= total {
		return 100
	}

	return current * 100 / total
}

// List returns canonical index artifacts without downloading segment layers.
func (c *Client) List(ctx context.Context, repository string) ([]Artifact, error) {
	if ctx == nil {
		return nil, inverz.ErrNilContext
	}

	items, err := ocireg.ListIndexArtifacts(ctx, repository, c.authConfig, c.plainHTTP)
	if err != nil {
		return nil, err
	}

	artifacts := make([]Artifact, 0, len(items))
	for _, item := range items {
		algorithm, profile, err := inverz.ParseTag(item.Tag)
		if err != nil {
			return nil, fmt.Errorf("convert registry artifact tag %q: %w", item.Tag, err)
		}

		minID, err := strconv.ParseUint(item.MinSteamID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("convert registry artifact minimum SteamID64: %w", err)
		}

		maxID, err := strconv.ParseUint(item.MaxSteamID, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("convert registry artifact maximum SteamID64: %w", err)
		}

		entries, err := strconv.ParseUint(item.EntryCount, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("convert registry artifact entry count: %w", err)
		}

		artifacts = append(artifacts, Artifact{
			Algorithm:      algorithm,
			Profile:        profile,
			Tag:            item.Tag,
			Range:          inverz.SteamIDRange{Min: minID, Max: maxID},
			Entries:        entries,
			ManifestDigest: item.ManifestDigest,
			Segments:       item.SegmentCount,
			SizeBytes:      item.Size,
		})
	}

	return artifacts, nil
}

func toInternalProfile(value inverz.Profile) internalprofile.Profile {
	return internalprofile.Profile{
		Version:     value.Version,
		PrefixBits:  value.PrefixBits,
		RiceK:       value.RiceK,
		SegmentBits: value.SegmentBits,
	}
}
