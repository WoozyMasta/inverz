// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package progress renders long-operation progress without coupling renderers to domain logic.
package progress

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"github.com/woozymasta/flags"
	"github.com/woozymasta/inverz"
	"github.com/woozymasta/inverz/internal/limits"
	modelprogress "github.com/woozymasta/inverz/internal/progress"
)

const (
	// maxProgressWidth keeps reported TTY widths from producing unusably long bars.
	maxProgressWidth = 120
	// plainProgressCapacity bounds best-effort messages while reserving room for lifecycle events.
	plainProgressCapacity = 128
	// plainLifecycleCapacity covers four lifecycle messages per possible segment plus operation events.
	plainLifecycleCapacity = limits.MaxSegments*4 + 16

	// layoutNarrow keeps only a short label and percentage.
	layoutNarrow layout = iota
	// layoutMedium adds counters and throughput.
	layoutMedium
	// layoutFull adds exact counts and ETA.
	layoutFull
)

// Session adapts operation callbacks to one TTY renderer or sparse plain logger.
type Session struct {
	model          modelprogress.Model            // Renderer-independent task state.
	startedAt      time.Time                      // Start time for the active progress summary.
	lastLine       time.Time                      // Time of the latest line used to throttle plain heartbeats.
	writer         io.Writer                      // Destination for progress output, normally stderr.
	operationErr   error                          // Original operation error used during renderer cleanup.
	progress       *mpb.Progress                  // TTY backend, accessed only by its renderer goroutine.
	current        map[uint32]uint64              // Last cumulative count used to calculate atomic deltas.
	tasks          map[uint32]*modelprogress.Task // Current task handle for each segment index.
	overallTask    *modelprogress.Task            // Completed-segment or transferred-byte counter.
	segmentStarted map[uint32]time.Time           // Start times for segment-level duration summaries.
	heartbeatDone  chan struct{}                  // Stops the non-TTY heartbeat goroutine.
	plainLines     chan string                    // Bounded queue that keeps worker callbacks away from stderr writes.
	outputDone     chan struct{}                  // Closed after queued plain lines have been written.
	renderStop     chan struct{}                  // Stops the TTY snapshot renderer.
	renderDone     chan struct{}                  // Closed after mpb bars have been cleaned up.
	operation      string                         // Short operation name used in status lines.
	header         string                         // Static operation context printed once on first progress.
	unit           string                         // Semantic unit for the active operation phase.
	currentPhase   string                         // Current lifecycle phase, such as verify or transfer.
	lastEvent      inverz.ProgressEvent           // Latest progress snapshot used by plain heartbeats.
	heartbeatWG    sync.WaitGroup                 // Waits for the non-TTY heartbeat goroutine.
	width          int                            // Terminal width captured when the renderer starts.
	total          uint64                         // Stable total for the active operation phase.
	completed      uint64                         // Number of segments completed in the current phase.
	generatedCount uint64                         // Segments whose build phase actually started.
	generatedIDs   uint64                         // AccountIDs included in started build phases.
	lastPercent    uint64                         // Last plain-mode ten-percent milestone.
	mu             sync.Mutex                     // Protects the model and lifecycle metadata.
	currentSegment uint32                         // Segment whose plain-mode percentage was last printed.
	tty            bool                           // Whether writer is an interactive stderr file.
	started        bool                           // Whether the selected presentation backend has started.
	closed         bool                           // Prevents callbacks after shutdown from touching task state.
}

// layout identifies the amount of detail that fits in the detected terminal width.
type layout uint8

// activeTask tracks one segment row and its current phase.
type activeTask struct {
	lastUpdate time.Time // Last sample time used for speed and ETA smoothing.
	bar        *mpb.Bar  // Transient phase bar owned by the TTY renderer.
	phase      string    // Phase represented by bar.
	current    uint64    // Last rendered cumulative count.
}

// bucketSpeed formats bucket throughput with dynamic decimal SI units.
type bucketSpeed struct {
	decor.WC
	rate        float64 // Smoothed serialized bucket rate in buckets per second.
	initialized bool    // Whether the first throughput sample has been recorded.
}

// EwmaUpdate records a serialized bucket throughput sample using an age-two moving average.
func (d *bucketSpeed) EwmaUpdate(count int64, elapsed time.Duration) {
	if count <= 0 || elapsed <= 0 {
		return
	}

	sample := float64(count) / elapsed.Seconds()
	if !d.initialized {
		d.rate = sample
		d.initialized = true
		return
	}

	d.rate += (sample - d.rate) * (2.0 / 3.0)
}

// Decor renders the smoothed rate with a separating space before its value.
func (d *bucketSpeed) Decor(_ decor.Statistics) (string, int) {
	return d.Format(" " + formatBucketRate(d.rate))
}

// New creates a progress session and defers output until the operation reports progress.
func New(writer io.Writer, operation, header string) *Session {
	s := &Session{
		writer: writer, operation: operation, header: header,
		current: make(map[uint32]uint64), tasks: make(map[uint32]*modelprogress.Task),
		segmentStarted: make(map[uint32]time.Time), lastLine: time.Now(),
	}
	if file, ok := writer.(*os.File); ok {
		s.tty = flags.DetectFileTTY(file)
	}

	return s
}

// Report consumes a coarse generator lifecycle or progress event.
func (s *Session) Report(event inverz.ProgressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.recordGenerated(event)
	if !s.started && s.operation == "generate" && event.State == "completed" {
		s.completed++
		if event.Segments > 0 {
			s.total = uint64(event.Segments)
		}
		return
	}

	if !s.started {
		s.startOutput()
	}

	if event.Segments > 0 {
		if event.Unit == "bytes" {
			if event.Segment == 0 && event.State == "operation-started" {
				s.total = event.Total
			}
		} else {
			s.total = uint64(event.Segments)
		}

		s.unit = event.Unit
		if s.overallTask == nil {
			unit := modelprogress.UnitSegments
			if event.Unit == "bytes" {
				unit = modelprogress.UnitBytes
			}

			s.overallTask = s.model.Start(modelprogress.Spec{
				ID:    "overall",
				Name:  "overall",
				Phase: event.Phase,
				Unit:  unit,
				Total: s.total,
			})

			if s.completed > 0 {
				s.overallTask.Add(s.completed)
			}
		}
	}

	if event.State == "operation-started" && event.Phase != "" && event.Phase != s.currentPhase {
		if s.currentPhase != "" {
			for index := range s.tasks {
				s.model.Remove(fmt.Sprintf("segment-%d", index))
			}

			s.tasks = make(map[uint32]*modelprogress.Task)
			s.current = make(map[uint32]uint64)
			s.completed = 0
			s.lastPercent = 0
			s.currentSegment = 0
			s.lastEvent = inverz.ProgressEvent{}

			if s.overallTask != nil {
				s.overallTask.SetPhase(event.Phase, s.total)
			}
		}

		s.currentPhase = event.Phase
	}

	if event.State == "progress" {
		s.lastEvent = event
	} else if (event.State == "completed" || event.State == "failed") && s.lastEvent.Segment == event.Segment {
		s.lastEvent = inverz.ProgressEvent{}
	}

	s.updateModel(event)
	if !s.tty {
		s.reportPlain(event)
	}
}

// GeneratedEntries returns IDs whose segments entered the actual build phase.
func (s *Session) GeneratedEntries() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.generatedIDs
}

// GeneratedSegments reports how many segments began hashing and Rice encoding.
func (s *Session) GeneratedSegments() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.generatedCount
}

// recordGenerated counts only segments that started generator work.
func (s *Session) recordGenerated(event inverz.ProgressEvent) {
	if event.Phase == "build" && event.State == "phase-started" {
		s.generatedCount++
		s.generatedIDs += event.Total
	}
}

// startOutput lazily selects the renderer so unchanged OCI pulls stay silent.
func (s *Session) startOutput() {
	s.started = true
	s.startedAt = time.Now()
	if s.tty {
		_, _ = fmt.Fprintln(s.writer, s.header)
		s.width, _ = flags.DetectTerminalSize()
		if s.width < 20 {
			s.width = 20
		}
		if s.width > maxProgressWidth {
			s.width = maxProgressWidth
		}

		s.progress = mpb.New(mpb.WithOutput(s.writer), mpb.WithWidth(s.width), mpb.WithRefreshRate(250*time.Millisecond))
		s.renderStop = make(chan struct{})
		s.renderDone = make(chan struct{})
		go s.renderTTY()

		return
	}

	_, _ = fmt.Fprintf(s.writer, "%s: %s\n", s.operation, s.header)
	s.plainLines = make(chan string, plainProgressCapacity+plainLifecycleCapacity)
	s.outputDone = make(chan struct{})
	go s.writePlainLines()

	s.heartbeatDone = make(chan struct{})
	s.heartbeatWG.Add(1)
	go s.heartbeat()
}

// updateModel applies lifecycle changes and counters without touching terminal state.
func (s *Session) updateModel(event inverz.ProgressEvent) {
	if event.State == "operation-started" {
		return
	}

	if event.State == "started" {
		s.tasks[event.Segment] = s.model.Start(modelprogress.Spec{
			ID:    fmt.Sprintf("segment-%d", event.Segment),
			Name:  fmt.Sprintf("seg-%03d", event.Segment),
			Phase: event.Phase,
			Unit:  progressUnit(s.unit),
			Total: event.Total,
		})

		return
	}

	task := s.tasks[event.Segment]
	if task == nil {
		task = s.model.Start(modelprogress.Spec{
			ID:    fmt.Sprintf("segment-%d", event.Segment),
			Name:  fmt.Sprintf("seg-%03d", event.Segment),
			Phase: event.Phase,
			Unit:  progressUnit(s.unit),
			Total: event.Total,
		})
		s.tasks[event.Segment] = task
	}

	if event.State == "phase-started" {
		if task.Snapshot().Phase != event.Phase {
			task.SetPhase(event.Phase, event.Total)
			s.current[event.Segment] = 0
		}

		return
	}

	switch event.State {
	case "completed":
		current := s.current[event.Segment]
		if s.unit == "bytes" && event.Total > current {
			remaining := event.Total - current
			task.Add(remaining)
			s.overallTask.Add(remaining)
		}

		task.Complete()
		s.completed++
		if s.unit != "bytes" {
			s.overallTask.Add(1)
		}

		delete(s.tasks, event.Segment)
		s.model.Remove(fmt.Sprintf("segment-%d", event.Segment))

	case "failed":
		task.Fail()
		delete(s.tasks, event.Segment)
		s.model.Remove(fmt.Sprintf("segment-%d", event.Segment))

	case "progress":
		if task.Snapshot().Phase != event.Phase {
			task.SetPhase(event.Phase, event.Total)
			s.current[event.Segment] = 0
		}

		if event.Processed > s.current[event.Segment] {
			delta := event.Processed - s.current[event.Segment]
			task.Add(delta)
			if s.unit == "bytes" {
				s.overallTask.Add(delta)
			}
			s.current[event.Segment] = event.Processed
		}
	}
}

// renderTTY periodically transfers model snapshots to mpb from one presentation goroutine.
func (s *Session) renderTTY() {
	defer close(s.renderDone)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	uiTasks := make(map[uint32]*activeTask)
	var overall *mpb.Bar
	var overallPhase string
	var overallCurrent uint64
	var overallLastUpdate time.Time

	for {
		select {
		case <-s.renderStop:
			s.mu.Lock()
			err := s.operationErr
			total := s.total
			s.mu.Unlock()

			for _, active := range uiTasks {
				active.bar.Abort(true)
			}

			if overall != nil {
				if err == nil {
					overall.SetCurrent(toInt64(total))
				} else {
					overall.Abort(true)
				}
			}
			s.progress.Wait()
			return

		case <-ticker.C:
		}

		s.mu.Lock()

		unit, total, currentPhase := s.unit, s.total, s.currentPhase
		overallSnapshot := modelprogress.Snapshot{}
		if s.overallTask != nil {
			overallSnapshot = s.overallTask.Snapshot()
		}

		tasks := make(map[uint32]modelprogress.Snapshot, len(s.tasks))
		for index, task := range s.tasks {
			tasks[index] = task.Snapshot()
		}

		progressBar := s.progress
		width := s.width

		s.mu.Unlock()

		if overall == nil && total > 0 {
			label := "overall segments"
			if unit == "bytes" {
				label = "overall bytes"
			}
			overall = newBar(progressBar, width, unit, total, label)
			overallPhase = currentPhase
			overallLastUpdate = time.Now()
		}

		if overall != nil && currentPhase != overallPhase {
			overall.Abort(true)
			overall = newBar(progressBar, width, unit, total, "overall "+currentPhase)
			overallPhase = currentPhase
			overallCurrent = 0
			overallLastUpdate = time.Now()
		}

		if overall != nil && overallSnapshot.Current > overallCurrent {
			if unit == "bytes" {
				overall.EwmaSetCurrent(toInt64(overallSnapshot.Current), time.Since(overallLastUpdate))
				overallLastUpdate = time.Now()
			} else {
				overall.SetCurrent(toInt64(overallSnapshot.Current))
			}
			overallCurrent = overallSnapshot.Current
		}

		seen := make(map[uint32]struct{}, len(tasks))
		for index, snapshot := range tasks {
			seen[index] = struct{}{}
			active := uiTasks[index]
			if snapshot.State == modelprogress.Done || snapshot.State == modelprogress.Failed {
				if active != nil {
					active.bar.Abort(true)
					delete(uiTasks, index)
				}

				continue
			}

			if active == nil || active.phase != snapshot.Phase {
				if active != nil {
					active.bar.Abort(true)
				}

				name := snapshot.Name + " " + snapshot.Phase
				started := time.Now()
				active = &activeTask{
					bar:   newBar(progressBar, width, unit, snapshot.Total, name),
					phase: snapshot.Phase, lastUpdate: started,
				}
				uiTasks[index] = active
			}

			if snapshot.Current > active.current {
				active.bar.EwmaSetCurrent(toInt64(snapshot.Current), time.Since(active.lastUpdate))
				active.lastUpdate = time.Now()
				active.current = snapshot.Current
			}
		}

		for index, active := range uiTasks {
			if _, exists := seen[index]; !exists {
				active.bar.Abort(true)
				delete(uiTasks, index)
			}
		}
	}
}

// reportPlain prints lifecycle lines and emits at most one heartbeat per minute.
func (s *Session) reportPlain(event inverz.ProgressEvent) {
	if event.Segment != s.currentSegment {
		s.currentSegment = event.Segment
		s.lastPercent = 0
	}
	if event.Phase != "" && event.Phase != s.currentPhase {
		s.currentPhase = event.Phase
		s.lastPercent = 0
	}

	switch event.State {
	case "operation-started":
		if event.Phase == "transfer" {
			s.importantLine(fmt.Sprintf("%s: %d reused, %d required, %s", s.operation,
				event.Reused, max(event.Segments-event.Reused, 0), formatBytes(event.Total)))
		}

	case "started":
		s.segmentStarted[event.Segment] = time.Now()
		if event.Unit == "bytes" {
			s.importantLine(fmt.Sprintf("%s: seg-%03d started, %s",
				s.operation, event.Segment, formatBytes(event.Total)))
		} else {
			s.importantLine(fmt.Sprintf("%s: seg-%03d started", s.operation, event.Segment))
		}

	case "phase-started":
		s.segmentStarted[event.Segment] = time.Now()
		s.importantLine(fmt.Sprintf("%s: seg-%03d %s started", s.operation, event.Segment, event.Phase))

	case "completed":
		elapsed := time.Since(s.segmentStarted[event.Segment])
		if event.Unit == "bytes" {
			s.importantLine(fmt.Sprintf("%s: seg-%03d completed, %s, %s, %s",
				s.operation,
				event.Segment,
				formatBytes(event.Processed),
				elapsed.Round(time.Millisecond),
				formatRate(event.Processed, elapsed),
			))
		} else {
			s.importantLine(fmt.Sprintf("%s: seg-%03d completed (%d/%d), %s", s.operation,
				event.Segment, s.completed, s.total, elapsed.Round(time.Millisecond)))
		}

	case "failed":
		s.importantLine(fmt.Sprintf("%s: seg-%03d failed", s.operation, event.Segment))

	case "progress":
		if s.total > 0 && event.Percent >= s.lastPercent+10 {
			s.lastPercent = event.Percent
			current, total := formatItems(event.Processed), formatItems(event.Total)
			if event.Unit == "bytes" {
				current, total = formatBytes(event.Processed), formatBytes(event.Total)
			}

			elapsed := time.Since(s.segmentStarted[event.Segment])
			rate := "-"

			if elapsed >= 2*time.Second {
				switch {
				case event.Unit == "bytes":
					rate = formatRate(event.Processed, elapsed)

				case event.Phase == "write":
					rate = formatBucketRate(float64(event.Processed) / elapsed.Seconds())

				default:
					rate = formatItemRate(event.Processed, elapsed)
				}
			}

			s.line(fmt.Sprintf("%s: seg-%03d %s: %d%%, %s/%s, %s",
				s.operation, event.Segment, event.Phase, event.Percent, current, total, rate))
		}
	}
}

// progressUnit maps public semantic labels to the neutral model's unit enum.
func progressUnit(value string) modelprogress.Unit {
	if value == "bytes" {
		return modelprogress.UnitBytes
	}
	return modelprogress.UnitItems
}

// heartbeat emits a plain status line only after a full minute without other output.
func (s *Session) heartbeat() {
	defer s.heartbeatWG.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.heartbeatDone:
			return

		case now := <-ticker.C:
			s.mu.Lock()
			if !s.closed && now.Sub(s.lastLine) >= time.Minute && s.lastEvent.Phase != "" {
				event := s.lastEvent
				current, total := formatItems(event.Processed), formatItems(event.Total)
				rate := formatItemRate(event.Processed, now.Sub(s.segmentStarted[event.Segment]))

				if event.Unit == "bytes" {
					current, total = formatBytes(event.Processed), formatBytes(event.Total)
					rate = formatRate(event.Processed, now.Sub(s.segmentStarted[event.Segment]))
				} else if event.Phase == "write" {
					rate = formatBucketRate(float64(event.Processed) / now.Sub(s.segmentStarted[event.Segment]).Seconds())
				}

				s.line(fmt.Sprintf("%s: seg-%03d %s: %d%%, %s/%s, %s",
					s.operation, event.Segment, event.Phase, event.Percent, current, total, rate))
			}
			s.mu.Unlock()
		}
	}
}

// line queues status text and records activity for heartbeat throttling.
func (s *Session) line(message string) {
	if len(s.plainLines) >= plainProgressCapacity {
		s.lastLine = time.Now()
		return
	}

	select {
	case s.plainLines <- message:
	default:
		// A stalled stderr consumer must not block generation or transfer workers.
	}
	s.lastLine = time.Now()
}

// importantLine queues a lifecycle message in a bounded buffer sized for one operation's events.
func (s *Session) importantLine(message string) {
	s.plainLines <- message
	s.lastLine = time.Now()
}

// writePlainLines serializes all non-TTY output away from operation workers.
func (s *Session) writePlainLines() {
	defer close(s.outputDone)
	for message := range s.plainLines {
		_, _ = fmt.Fprintln(s.writer, message)
	}
}

// newBar creates a compact ASCII-only bar with stable task labels.
func newBar(progress *mpb.Progress, width int, unit string, total uint64, name string) *mpb.Bar {
	if total == 0 {
		total = 1
	}
	selectedLayout := terminalLayout(width)
	labelWidth := 8
	if selectedLayout == layoutMedium {
		labelWidth = 14
	}
	if selectedLayout == layoutFull {
		labelWidth = 22
	}
	name = truncate(name, labelWidth)

	return progress.New(
		toInt64(total),
		mpb.BarStyle().
			Lbound("[").
			Filler("#").
			Tip("#").
			Padding("-").
			Rbound("]"),
		mpb.PrependDecorators(decor.Name(name, decor.WC{W: labelWidth})),
		mpb.AppendDecorators(progressDecorators(selectedLayout, unit, name)...),
		mpb.BarRemoveOnComplete(),
	)
}

// progressDecorators formats each value with visible separators and unit-aware counters.
func progressDecorators(selectedLayout layout, unit, name string) []decor.Decorator {
	appendDecorators := []decor.Decorator{decor.NewPercentage(" %.1f")}
	if selectedLayout == layoutFull {
		counts := decor.Any(func(stats decor.Statistics) string {
			if unit == "bytes" {
				return formatBytes(decoratorCounter(stats.Current)) + " / " +
					formatBytes(decoratorCounter(stats.Total))
			}

			return formatItems(decoratorCounter(stats.Current)) + " / " +
				formatItems(decoratorCounter(stats.Total))
		})

		appendDecorators = append([]decor.Decorator{counts}, appendDecorators...)
	}

	if selectedLayout != layoutNarrow && (unit == "bytes" || !strings.HasPrefix(name, "overall")) {
		speedFormat := " %.1f IDs/s"
		var speed decor.Decorator

		switch {
		case unit != "bytes" && strings.Contains(name, "write"):
			width := decor.WC{}
			speed = &bucketSpeed{WC: width.Init()}

		case unit == "bytes":
			speed = decor.EwmaSpeed(decor.SizeB1024(0), " %.1f ", 2.0)

		default:
			speed = decor.EwmaSpeed(float64(0), speedFormat, 2.0)
		}

		appendDecorators = append(appendDecorators, speed)
		if selectedLayout == layoutFull {
			appendDecorators = append(
				appendDecorators,
				decor.Any(func(decor.Statistics) string { return "  " }),
				decor.EwmaETA(decor.ET_STYLE_HHMMSS, 2.0),
			)
		}
	}

	return appendDecorators
}

// formatBucketRate scales bucket throughput with decimal SI prefixes.
func formatBucketRate(rate float64) string {
	units := [...]string{"", "K", "M", "G", "T"}
	unit := 0
	for rate >= 1000 && unit < len(units)-1 {
		rate /= 1000
		unit++
	}

	return fmt.Sprintf("%.1f %sbuckets/s", rate, units[unit])
}

// decoratorCounter converts MPB's nonnegative signed counters for the shared formatters.
func decoratorCounter(value int64) uint64 {
	if value < 0 {
		return 0
	}

	// #nosec G115 -- progress values are nonnegative and capped by int64 bar totals.
	return uint64(value)
}

// terminalLayout selects stable decorator widths for the three supported terminal sizes.
func terminalLayout(width int) layout {
	if width >= 80 {
		return layoutFull
	}

	if width >= 55 {
		return layoutMedium
	}

	return layoutNarrow
}

// truncate limits a task label so the selected terminal layout remains on one row.
func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}

	if width < 2 {
		return string(runes[:width])
	}

	return string(runes[:width-1]) + "~"
}

// formatBytes formats binary transfer sizes with compact IEC units.
func formatBytes(value uint64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}

	units := [...]string{"KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	for _, unit := range units {
		amount /= 1024
		if amount < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", amount, unit)
		}
	}

	return fmt.Sprintf("%d B", value)
}

// formatRate formats average byte throughput with compact IEC units.
func formatRate(bytes uint64, elapsed time.Duration) string {
	if elapsed <= 0 {
		return "-"
	}

	perSecond := float64(bytes) / elapsed.Seconds()
	if perSecond < 1 {
		return "-"
	}

	return formatBytes(uint64(perSecond)) + "/s"
}

// formatItems formats identifier counts and rates with compact decimal suffixes.
func formatItems(value uint64) string {
	const base = 1000
	if value < base {
		return strconv.FormatUint(value, 10)
	}

	units := [...]string{"K", "M", "B", "T"}
	amount := float64(value)
	for _, unit := range units {
		amount /= base
		if amount < base || unit == units[len(units)-1] {
			if amount >= 100 {
				return fmt.Sprintf("%.0f%s", amount, unit)
			}

			return fmt.Sprintf("%.1f%s", amount, unit)
		}
	}

	return strconv.FormatUint(value, 10)
}

// formatItemRate formats the current-phase AccountID rate when the sample is stable.
func formatItemRate(items uint64, elapsed time.Duration) string {
	if elapsed < 2*time.Second {
		return "-"
	}

	return formatItems(uint64(float64(items)/elapsed.Seconds())) + " IDs/s"
}

// Close removes active rows and waits for the renderer to stop.
func (s *Session) Close(err error) {
	s.close(err, true)
}

// CloseWithoutSummary removes active rows without printing a completion line.
func (s *Session) CloseWithoutSummary(err error) {
	s.close(err, false)
}

// close drains the selected progress backend and optionally prints its generic summary.
func (s *Session) close(err error, printSummary bool) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}

	s.closed = true
	if !s.started {
		s.mu.Unlock()
		return
	}

	if s.heartbeatDone != nil {
		close(s.heartbeatDone)
	}

	s.operationErr = err
	if s.overallTask != nil {
		if err != nil {
			s.overallTask.Fail()
		} else {
			s.overallTask.Complete()
		}
	}
	if s.renderStop != nil {
		close(s.renderStop)
	}

	s.mu.Unlock()
	s.heartbeatWG.Wait()

	if s.renderDone != nil {
		<-s.renderDone
	}
	if s.plainLines != nil {
		close(s.plainLines)
		<-s.outputDone
	}
	if err != nil || !printSummary {
		return
	}

	if s.unit == "bytes" {
		elapsed := time.Since(s.startedAt)
		_, _ = fmt.Fprintf(s.writer, "%s: completed, %s, %s, %s\n",
			s.operation, formatBytes(s.total), elapsed.Round(time.Millisecond), formatRate(s.total, elapsed))
		return
	}

	_, _ = fmt.Fprintf(s.writer, "%s: completed, %d/%d segments, %s\n",
		s.operation, s.completed, s.total, time.Since(s.startedAt).Round(time.Millisecond))
}

// toInt64 caps counters at the type accepted by mpb.
func toInt64(value uint64) int64 {
	const maxInt64 = 1<<63 - 1
	if value > maxInt64 {
		return maxInt64
	}

	return int64(value)
}
