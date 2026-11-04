// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package releaselock provides fail-fast process-shared locks for one local release path.
package releaselock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Lock owns one operating-system lock until Close releases its file handle.
type Lock struct {
	closeErr  error
	file      *os.File
	closeOnce sync.Once
}

// Acquire locks the release path without waiting for another process to finish.
func Acquire(releasePath string) (*Lock, error) {
	if releasePath == "" {
		return nil, ErrReleasePathRequired
	}

	parent := filepath.Dir(releasePath)
	// #nosec G703 - callers pass the validated local release directory selected by Store.
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, fmt.Errorf("create release lock directory: %w", err)
	}

	lockPath := filepath.Join(parent, "."+filepath.Base(releasePath)+".lock")
	// #nosec G703 - lockPath is derived from the validated local release directory.
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open release lock: %w", err)
	}

	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrReleaseBusy) {
			return nil, fmt.Errorf("%s: %w", filepath.Base(releasePath), ErrReleaseBusy)
		}

		return nil, fmt.Errorf("lock release %s: %w", filepath.Base(releasePath), err)
	}

	return &Lock{file: file}, nil
}

// Close unlocks the release and closes the persistent lock file.
func (l *Lock) Close() error {
	l.closeOnce.Do(func() {
		unlockErr := unlockFile(l.file)
		closeErr := l.file.Close()
		l.closeErr = errors.Join(unlockErr, closeErr)
	})

	return l.closeErr
}
