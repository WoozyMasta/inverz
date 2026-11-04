// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package oci

import (
	"context"
	"errors"
	"testing"

	"github.com/woozymasta/inverz"
)

func TestClientRejectsNilContext(t *testing.T) {
	client, err := New()
	if err != nil {
		t.Fatal(err)
	}
	profile := inverz.DefaultProfile()
	var nilContext context.Context
	if _, err := client.Pull(nilContext, "", "example.invalid/index", inverz.AlgorithmDayZ, profile); !errors.Is(err, inverz.ErrNilContext) {
		t.Fatalf("Pull() error = %v, want ErrNilContext", err)
	}
	if _, err := client.Push(nilContext, "", "example.invalid/index", inverz.AlgorithmDayZ, profile, PushOptions{}); !errors.Is(err, inverz.ErrNilContext) {
		t.Fatalf("Push() error = %v, want ErrNilContext", err)
	}
	if _, err := client.List(nilContext, "example.invalid/index"); !errors.Is(err, inverz.ErrNilContext) {
		t.Fatalf("List() error = %v, want ErrNilContext", err)
	}
}
