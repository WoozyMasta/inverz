// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package profile

import (
	"errors"
	"testing"
)

func TestDefaultAndTagRoundTrip(t *testing.T) {
	t.Parallel()

	p := Default()
	if got := p.String(); got != "v1-p18-k17-s28" {
		t.Fatalf("String() = %q", got)
	}
	tag, err := p.Tag("dayz")
	if err != nil || tag != "dayz-v1-p18-k17-s28" {
		t.Fatalf("Tag() = %q, %v", tag, err)
	}

	algorithm, parsed, err := ParseTag(tag)
	if err != nil || algorithm != "dayz" || parsed != p {
		t.Fatalf("ParseTag() = %q, %#v, %v", algorithm, parsed, err)
	}
}

func TestParseRejectsNoncanonicalNames(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"v01-p18-k17-s28", "v1-p018-k17-s28", "v1-p18-k17-s33",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse(value); !errors.Is(err, ErrInvalidName) {
				t.Fatalf("Parse(%q) error = %v", value, err)
			}
		})
	}
}

func TestParseTagRejectsApplicationTagsAndAliases(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"0.1.2", "latest", "dz-v1-p18-k17-s28", "be-v1-p18-k17-s28"} {
		if _, _, err := ParseTag(value); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ParseTag(%q) error = %v", value, err)
		}
	}
}
