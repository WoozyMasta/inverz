// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package progress

import (
	"sync"
	"testing"
)

func TestConcurrentTaskCountersAndLifecycle(t *testing.T) {
	var model Model
	task := model.Start(Spec{ID: "verify", Unit: UnitBytes, Total: 1024})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 128 {
				task.Add(1)
			}
		})
	}
	workers.Wait()

	snapshot := model.Snapshots()
	if len(snapshot) != 1 || snapshot[0].Current != 1024 || snapshot[0].State != Running {
		t.Fatalf("Snapshots() = %#v", snapshot)
	}

	task.Complete()
	snapshot = model.Snapshots()
	if snapshot[0].Current != 1024 || snapshot[0].State != Done {
		t.Fatalf("completed Snapshots() = %#v", snapshot)
	}
	model.Remove("verify")
	if snapshot = model.Snapshots(); len(snapshot) != 0 {
		t.Fatalf("Snapshots() after Remove() = %#v", snapshot)
	}
}
