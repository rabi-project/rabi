// SPDX-License-Identifier: Apache-2.0

// Package loadtest is the Phase 2 load & soak harness (P2.M2). It boots the
// real control-plane stack (store, registry, dispatcher, API server) against a
// throwaway Postgres, drives it with a storm of jobs across many synthetic
// targets, and asserts the test-plan §4 thresholds: scheduler-cycle p99 < 2s,
// API read p99 < 300ms, write p99 < 1s, and bounded queue growth. The soak
// variant runs an accelerated replay and asserts RSS-growth, goroutine, and
// stuck-job bounds.
package loadtest

import (
	"sort"
	"sync"
	"time"
)

// Latencies is a concurrency-safe recorder of durations that computes
// percentiles on demand. Recording is cheap (a locked append); the sort only
// happens when a percentile is read, at the end of a run.
type Latencies struct {
	mu      sync.Mutex
	samples []time.Duration
}

// Record appends one observed duration.
func (l *Latencies) Record(d time.Duration) {
	l.mu.Lock()
	l.samples = append(l.samples, d)
	l.mu.Unlock()
}

// Time records the duration of fn.
func (l *Latencies) Time(fn func()) {
	start := time.Now()
	fn()
	l.Record(time.Since(start))
}

// Count returns how many samples were recorded.
func (l *Latencies) Count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.samples)
}

// Percentile returns the q-quantile (0..1) using nearest-rank on a sorted copy.
// Returns 0 when there are no samples.
func (l *Latencies) Percentile(q float64) time.Duration {
	l.mu.Lock()
	sorted := make([]time.Duration, len(l.samples))
	copy(sorted, l.samples)
	l.mu.Unlock()
	if len(sorted) == 0 {
		return 0
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	// nearest-rank: rank = ceil(q*N), 1-indexed.
	rank := int(q*float64(len(sorted)) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// P50, P99, and Max are the percentiles the harness reports.
func (l *Latencies) P50() time.Duration { return l.Percentile(0.50) }
func (l *Latencies) P99() time.Duration { return l.Percentile(0.99) }
func (l *Latencies) Max() time.Duration { return l.Percentile(1.0) }

// Mean returns the arithmetic mean of all samples (0 when empty).
func (l *Latencies) Mean() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, d := range l.samples {
		total += d
	}
	return total / time.Duration(len(l.samples))
}
