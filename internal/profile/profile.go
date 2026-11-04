// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package profile defines canonical index profile names and OCI tags.
package profile

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/woozymasta/inverz/internal/limits"
)

var (
	// ErrInvalidProfile identifies unsupported profile parameters.
	ErrInvalidProfile = errors.New("invalid index profile")
	// ErrInvalidAlgorithm identifies a noncanonical index algorithm name.
	ErrInvalidAlgorithm = errors.New("invalid index algorithm")
	// ErrInvalidName identifies a malformed canonical profile or index tag.
	ErrInvalidName = errors.New("invalid canonical profile or tag")
)

// Profile describes the codec parameters that determine an index artifact.
type Profile struct {
	Version     uint8 // On-disk index format version.
	PrefixBits  uint8 // Digest prefix width.
	RiceK       uint8 // Rice remainder width.
	SegmentBits uint8 // AccountID segment width.
}

// Default returns the canonical production profile.
func Default() Profile {
	return Profile{Version: 1, PrefixBits: 18, RiceK: 17, SegmentBits: 28}
}

// Validate checks the profile against the supported index format.
func (p Profile) Validate() error {
	if p.Version != 1 ||
		p.PrefixBits == 0 ||
		p.PrefixBits > limits.MaxPrefixBits ||
		p.RiceK > limits.MaxRiceK ||
		p.SegmentBits == 0 || p.SegmentBits > limits.MaxSegmentBits {
		return fmt.Errorf("%w: v%d-p%d-k%d-s%d", ErrInvalidProfile, p.Version, p.PrefixBits, p.RiceK, p.SegmentBits)
	}

	return nil
}

// String returns the stable local-store key for the profile.
func (p Profile) String() string {
	return fmt.Sprintf("v%d-p%d-k%d-s%d", p.Version, p.PrefixBits, p.RiceK, p.SegmentBits)
}

// Tag returns the canonical OCI index tag for algorithm and profile.
func (p Profile) Tag(algorithm string) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if !validAlgorithm(algorithm) {
		return "", fmt.Errorf("%w: %q", ErrInvalidAlgorithm, algorithm)
	}

	return algorithm + "-" + p.String(), nil
}

// Parse decodes and validates a canonical profile key.
func Parse(value string) (Profile, error) {
	if !strings.HasPrefix(value, "v") {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	parts := strings.Split(value, "-")
	if len(parts) != 4 {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	version, err := parsePart(parts[0], 'v')
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	prefixBits, err := parsePart(parts[1], 'p')
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	riceK, err := parsePart(parts[2], 'k')
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	segmentBits, err := parsePart(parts[3], 's')
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	p := Profile{
		Version:     version,
		PrefixBits:  prefixBits,
		RiceK:       riceK,
		SegmentBits: segmentBits,
	}
	if err := p.Validate(); err != nil || p.String() != value {
		return Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	return p, nil
}

// ParseTag decodes a canonical algorithm/profile OCI index tag.
func ParseTag(value string) (string, Profile, error) {
	algorithm, key, ok := strings.Cut(value, "-")
	if !ok || !validAlgorithm(algorithm) {
		return "", Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	p, err := Parse(key)
	if err != nil {
		return "", Profile{}, fmt.Errorf("%w: %q", ErrInvalidName, value)
	}

	return algorithm, p, nil
}

// parsePart parses one single-letter canonical decimal profile component.
func parsePart(value string, prefix byte) (uint8, error) {
	if len(value) < 2 || value[0] != prefix || (len(value) > 2 && value[1] == '0') {
		return 0, ErrInvalidName
	}

	n, err := strconv.ParseUint(value[1:], 10, 8)
	return uint8(n), err
}

// validAlgorithm accepts only persistent canonical algorithm names.
func validAlgorithm(value string) bool {
	return value == "dayz" || value == "battleye"
}
