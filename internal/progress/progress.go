// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package progress contains renderer-independent progress state for long operations.
package progress

import (
	"sync"
	"sync/atomic"
)

// Unit describes the meaning of a task's current and total values.
type Unit uint8

const (
	// UnitItems represents identifiers or other discrete work items.
	UnitItems Unit = iota + 1
	// UnitBytes represents file or network bytes.
	UnitBytes
	// UnitSegments represents completed index segments.
	UnitSegments
)

// State describes the lifecycle of one progress task.
type State uint32

const (
	// Pending indicates that work has not started.
	Pending State = iota
	// Running indicates that work is in progress.
	Running
	// Done indicates that work completed successfully.
	Done
	// Failed indicates that work stopped with an error.
	Failed
)

// Spec defines stable metadata for one task.
type Spec struct {
	ID    string // Stable identifier used to find task snapshots.
	Name  string // Short renderer-facing task label.
	Phase string // Current phase name, changed only at lifecycle boundaries.
	Unit  Unit   // Meaning of Current and Total.
	Total uint64 // Stable total for the current phase, or zero when unknown.
}

// Snapshot is an immutable view of one task at a point in time.
type Snapshot struct {
	Spec           // Stable task identity and phase metadata.
	Current uint64 // Completed units observed at snapshot time.
	State   State  // Lifecycle state observed at snapshot time.
}

// Reporter starts tasks and provides immutable snapshots to presentation layers.
type Reporter interface {
	Start(Spec) *Task
	Snapshots() []Snapshot
}

// Model stores task lifecycle metadata and atomic work counters.
type Model struct {
	tasks map[string]*Task
	mu    sync.RWMutex
}

// Task tracks one unit of work with lock-free counter updates.
type Task struct {
	spec    Spec
	current atomic.Uint64
	mu      sync.RWMutex
	state   atomic.Uint32
}

// Nop returns a reporter that discards all progress updates.
func Nop() Reporter { return nopReporter{} }

// Start registers a task and marks it as running.
func (m *Model) Start(spec Spec) *Task {
	task := &Task{spec: spec}
	task.state.Store(uint32(Running))
	m.mu.Lock()
	if m.tasks == nil {
		m.tasks = make(map[string]*Task)
	}
	m.tasks[spec.ID] = task
	m.mu.Unlock()

	return task
}

// Snapshots returns a stable copy of all currently registered tasks.
func (m *Model) Snapshots() []Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Snapshot, 0, len(m.tasks))
	for _, task := range m.tasks {
		result = append(result, task.Snapshot())
	}

	return result
}

// Remove forgets a completed task so memory usage follows active work rather than history.
func (m *Model) Remove(id string) {
	m.mu.Lock()
	delete(m.tasks, id)
	m.mu.Unlock()
}

// Add advances completed work without requiring a renderer notification.
func (t *Task) Add(n uint64) {
	t.current.Add(n)
}

// SetPhase changes task metadata at a rare lifecycle boundary.
func (t *Task) SetPhase(phase string, total uint64) {
	t.mu.Lock()
	t.spec.Phase = phase
	t.spec.Total = total
	t.current.Store(0)
	t.mu.Unlock()
}

// Complete marks the task done and clamps its counter to the declared total.
func (t *Task) Complete() {
	t.mu.Lock()
	total := t.spec.Total
	if total > 0 {
		t.current.Store(total)
	}
	t.state.Store(uint32(Done))
	t.mu.Unlock()
}

// Fail marks the task as failed.
func (t *Task) Fail() {
	t.state.Store(uint32(Failed))
}

// Snapshot returns this task's immutable current state.
func (t *Task) Snapshot() Snapshot {
	t.mu.RLock()
	spec := t.spec
	current := t.current.Load()
	state := State(t.state.Load())
	t.mu.RUnlock()
	return Snapshot{Spec: spec, Current: current, State: state}
}

// nopReporter creates running tasks without retaining or exposing their progress.
type nopReporter struct{}

// Start creates a running task that can be updated without a reporter.
func (nopReporter) Start(spec Spec) *Task {
	task := &Task{spec: spec}
	task.state.Store(uint32(Running))
	return task
}

// Snapshots returns no tasks because nopReporter does not retain them.
func (nopReporter) Snapshots() []Snapshot {
	return nil
}
