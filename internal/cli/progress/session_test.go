// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package progress

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vbauerster/mpb/v8/decor"
	"github.com/woozymasta/inverz"
)

// gatedWriter blocks after the operation header until the test allows output to continue.
type gatedWriter struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	output  bytes.Buffer
	writes  int
}

// Write delays the first queued line and then records all bytes after release.
func (w *gatedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	block := w.writes == 2
	if block {
		close(w.started)
	}
	w.mu.Unlock()
	if block {
		<-w.release
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.Write(data)
}

// String returns the output collected by the writer.
func (w *gatedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}

// TestPlainSessionKeepsLifecycleMessagesWhenProgressQueueIsFull protects terminal status delivery.
func TestPlainSessionKeepsLifecycleMessagesWhenProgressQueueIsFull(t *testing.T) {
	writer := &gatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	session := New(writer, "pull", "test release")
	session.Report(inverz.ProgressEvent{State: "started", Segment: 0, Segments: 1, Total: 1})
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("plain output writer did not reach the queued lifecycle line")
	}

	for range plainProgressCapacity {
		session.line("intermediate progress")
	}
	session.Report(inverz.ProgressEvent{State: "failed", Segment: 0, Segments: 1})

	closed := make(chan struct{})
	go func() {
		session.Close(errors.New("operation failed"))
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned before queued output was drained")
	case <-time.After(25 * time.Millisecond):
	}

	close(writer.release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after stderr recovered")
	}
	output := writer.String()
	if !strings.Contains(output, "seg-000 started") || !strings.Contains(output, "seg-000 failed") {
		t.Fatalf("important lifecycle output was lost: %q", output)
	}
}

func TestPlainSessionUsesSparseLines(t *testing.T) {
	var stderr bytes.Buffer
	session := New(&stderr, "pull", "dayz-v1-p4-k3-s4")
	session.Report(inverz.ProgressEvent{
		State:    "operation-started",
		Unit:     "bytes",
		Phase:    "transfer",
		Segments: 2,
		Reused:   1,
		Total:    2048,
	})
	session.Report(inverz.ProgressEvent{
		State:    "started",
		Unit:     "bytes",
		Segment:  1,
		Segments: 2,
		Total:    2048,
	})
	session.Report(inverz.ProgressEvent{
		State:     "progress",
		Unit:      "bytes",
		Phase:     "transfer",
		Segment:   1,
		Segments:  2,
		Processed: 2048,
		Total:     2048,
		Percent:   100,
	})
	session.Report(inverz.ProgressEvent{
		State:     "completed",
		Unit:      "bytes",
		Phase:     "transfer",
		Segment:   1,
		Segments:  2,
		Processed: 2048,
		Total:     2048,
		Percent:   100,
	})
	session.Close(nil)

	output := stderr.String()
	for _, expected := range []string{
		"1 reused, 1 required, 2.0 KiB", "seg-001 started", "seg-001 completed", "completed, 2.0 KiB",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("stderr output %q does not contain %q", output, expected)
		}
	}
	if strings.Contains(output, "\x1b[") {
		t.Fatalf("non-TTY output contains ANSI control sequences: %q", output)
	}
}

func TestPlainSessionSerializesConcurrentCallbacks(t *testing.T) {
	var stderr bytes.Buffer
	session := New(&stderr, "generate", "dayz-v1-p4-k3-s4")
	session.Report(inverz.ProgressEvent{
		State:    "started",
		Unit:     "items",
		Segment:  0,
		Segments: 1,
	})
	session.Report(inverz.ProgressEvent{
		State:    "phase-started",
		Unit:     "items",
		Phase:    "build",
		Segment:  0,
		Segments: 1,
		Total:    100,
	})

	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for n := uint64(1); n <= 100; n++ {
				session.Report(inverz.ProgressEvent{
					State:     "progress",
					Unit:      "items",
					Phase:     "build",
					Segment:   0,
					Segments:  1,
					Processed: n,
					Total:     100,
					Percent:   n,
				})
			}
		})
	}
	workers.Wait()
	session.Report(inverz.ProgressEvent{State: "completed", Unit: "items", Segment: 0, Segments: 1})
	session.Close(nil)

	if !strings.Contains(stderr.String(), "completed, 1/1 segments") {
		t.Fatalf("concurrent session output = %q", stderr.String())
	}
}

func TestTerminalLayoutAndTruncation(t *testing.T) {
	for _, test := range []struct {
		width int
		want  layout
	}{
		{width: 120, want: layoutFull},
		{width: 79, want: layoutMedium},
		{width: 55, want: layoutMedium},
		{width: 54, want: layoutNarrow},
		{width: 20, want: layoutNarrow},
	} {
		if got := terminalLayout(test.width); got != test.want {
			t.Errorf("terminalLayout(%d) = %d, want %d", test.width, got, test.want)
		}
	}

	if got := truncate("segment-007 write", 8); got != "segment~" {
		t.Fatalf("truncate() = %q, want %q", got, "segment~")
	}
}

func TestProgressDecoratorsSeparateCountsAndRates(t *testing.T) {
	decorators := progressDecorators(layoutFull, "items", "seg-004 write")
	stats := decor.Statistics{Current: 131_072, Total: 262_144}
	var output strings.Builder
	for _, item := range decorators {
		if average, ok := item.(decor.EwmaDecorator); ok {
			average.EwmaUpdate(stats.Current, time.Second)
			average.EwmaUpdate(stats.Current, time.Second)
		}
		value, _ := item.Decor(stats)
		output.WriteString(value)
	}

	got := output.String()
	if !strings.Contains(got, "131K / 262K 50.0%") {
		t.Fatalf("formatted counters and percentage are not separated: %q", got)
	}
	if !strings.Contains(got, "50.0% 131.1 Kbuckets/s") {
		t.Fatalf("bucket rate should be spaced and scaled: %q", got)
	}
	if strings.Contains(got, "50.0%buckets/s") || strings.Contains(got, "buckets/s00:") {
		t.Fatalf("formatted rate and ETA are concatenated: %q", got)
	}
}

// TestGeneratedCountsBuildWorkButNotReusedSegments checks actual generated ID accounting.
func TestGeneratedCountsBuildWorkButNotReusedSegments(t *testing.T) {
	session := New(io.Discard, "generate", "building index")
	session.Report(inverz.ProgressEvent{State: "started", Segment: 2, Segments: 2})
	session.Report(inverz.ProgressEvent{State: "phase-started", Phase: "build", Segment: 2, Total: 1000})
	session.Report(inverz.ProgressEvent{State: "phase-started", Phase: "write", Segment: 2, Total: 16})
	session.Report(inverz.ProgressEvent{State: "completed", Segment: 1, Segments: 2})
	if got := session.GeneratedEntries(); got != 1000 {
		t.Fatalf("GeneratedEntries() = %d, want 1000", got)
	}
	if got := session.GeneratedSegments(); got != 1 {
		t.Fatalf("GeneratedSegments() = %d, want 1", got)
	}
	session.Close(nil)
}

func TestByteProgressDecoratorsUseReadableUnits(t *testing.T) {
	decorators := progressDecorators(layoutFull, "bytes", "seg-004 transfer")
	stats := decor.Statistics{Current: 1 << 20, Total: 2 << 20}
	value, _ := decorators[0].Decor(stats)
	if value != "1.0 MiB / 2.0 MiB" {
		t.Fatalf("byte counters = %q, want human-readable MiB values", value)
	}
}
