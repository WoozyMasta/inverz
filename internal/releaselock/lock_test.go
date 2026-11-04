// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package releaselock

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestAcquireIsExclusiveAndProfileScoped checks same-release exclusion and independent profiles.
func TestAcquireIsExclusiveAndProfileScoped(t *testing.T) {
	root := t.TempDir()
	first, err := Acquire(filepath.Join(root, "dayz", "v1-p18-k17-s28"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(filepath.Join(root, "dayz", "v1-p18-k17-s28")); !errors.Is(err, ErrReleaseBusy) {
		t.Fatalf("duplicate lock error = %v, want %v", err, ErrReleaseBusy)
	}

	other, err := Acquire(filepath.Join(root, "dayz", "v1-p16-k15-s28"))
	if err != nil {
		t.Fatalf("different profile lock: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	released, err := Acquire(filepath.Join(root, "dayz", "v1-p18-k17-s28"))
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := released.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseLockProcessHelper holds a lock only when launched by the subprocess test.
func TestReleaseLockProcessHelper(t *testing.T) {
	if os.Getenv("INVERZ_LOCK_HELPER") != "1" {
		return
	}

	lock, err := Acquire(os.Getenv("INVERZ_LOCK_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(os.Stdout, "locked\n")
	time.Sleep(500 * time.Millisecond)
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseLockIsSharedAcrossProcesses verifies the operating-system lock boundary.
func TestReleaseLockIsSharedAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dayz", "v1-p18-k17-s28")

	// #nosec G204 G702 - this test re-executes its own binary with fixed arguments.
	command := exec.Command(os.Args[0], "-test.run=^TestReleaseLockProcessHelper$")
	command.Env = append(os.Environ(), "INVERZ_LOCK_HELPER=1", "INVERZ_LOCK_PATH="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("read helper lock status: %v", scanner.Err())
	}
	if scanner.Text() != "locked" {
		t.Fatalf("helper output = %q, want locked", scanner.Text())
	}

	if _, err := Acquire(path); !errors.Is(err, ErrReleaseBusy) {
		t.Fatalf("cross-process lock error = %v, want %v", err, ErrReleaseBusy)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("lock helper process: %v", err)
	}

	released, err := Acquire(path)
	if err != nil {
		t.Fatalf("lock after child process exit: %v", err)
	}
	if err := released.Close(); err != nil {
		t.Fatal(err)
	}
}
