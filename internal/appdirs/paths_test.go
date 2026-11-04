// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package appdirs

import (
	"errors"
	"runtime"
	"testing"
)

func TestResolvePlatformPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		os   string
		env  map[string]string
		home string
		want Paths
	}{
		{
			name: "linux xdg data home",
			os:   "linux",
			env:  map[string]string{"XDG_DATA_HOME": "/data"},
			home: "/home/user",
			want: Paths{DataRoot: "/data/inverz"},
		},
		{
			name: "linux fallback",
			os:   "linux",
			env:  map[string]string{},
			home: "/home/user",
			want: Paths{DataRoot: "/home/user/.local/share/inverz"},
		},
		{
			name: "linux ignores relative data home",
			os:   "linux",
			env:  map[string]string{"XDG_DATA_HOME": "data"},
			home: "/home/user",
			want: Paths{DataRoot: "/home/user/.local/share/inverz"},
		},
		{
			name: "windows",
			os:   "windows",
			env:  map[string]string{"LOCALAPPDATA": `C:\Users\u\AppData\Local`},
			home: `C:\Users\u`,
			want: Paths{DataRoot: `C:\Users\u\AppData\Local\inverz`},
		},
		{
			name: "macos",
			os:   "darwin",
			env:  map[string]string{},
			home: "/Users/u",
			want: Paths{DataRoot: "/Users/u/Library/Application Support/inverz"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.os != runtime.GOOS {
				t.Skip("path semantics are provided by the current OS")
			}

			got, err := Resolve(lookup(test.env), func() (string, error) { return test.home, nil })
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("Resolve() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveRequiresHomeWhenNeeded(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows app data paths do not use the home directory")
	}

	_, err := Resolve(lookup(
		map[string]string{}),
		func() (string, error) { return "", errors.New("home unavailable") },
	)
	if !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrHomeUnavailable)
	}
}

// TestWindowsDataRootsMustBeAbsolute rejects relative Windows app-data variables.
func TestWindowsDataRootsMustBeAbsolute(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("Windows path rules are provided by the current OS")
	}

	_, err := Resolve(lookup(map[string]string{
		"LOCALAPPDATA": `relative\local`,
	}), func() (string, error) { return `C:\Users\foo`, nil })
	if !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrHomeUnavailable)
	}
}

// lookup creates an injected environment lookup for path tests.
func lookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
