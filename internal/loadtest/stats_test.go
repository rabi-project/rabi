// SPDX-License-Identifier: Apache-2.0

package loadtest

import (
	"testing"
	"time"
)

func TestLatenciesPercentiles(t *testing.T) {
	var l Latencies
	if l.P99() != 0 || l.Count() != 0 || l.Mean() != 0 {
		t.Fatal("empty recorder should report zero")
	}
	// 1..100 ms.
	for i := 1; i <= 100; i++ {
		l.Record(time.Duration(i) * time.Millisecond)
	}
	if l.Count() != 100 {
		t.Fatalf("count = %d, want 100", l.Count())
	}
	// nearest-rank: p50 -> rank ceil(0.5*100)=50 -> 50ms; p99 -> 99ms; max 100ms.
	if got := l.P50(); got != 50*time.Millisecond {
		t.Errorf("p50 = %v, want 50ms", got)
	}
	if got := l.P99(); got != 99*time.Millisecond {
		t.Errorf("p99 = %v, want 99ms", got)
	}
	if got := l.Max(); got != 100*time.Millisecond {
		t.Errorf("max = %v, want 100ms", got)
	}
	// mean of 1..100 = 50.5ms.
	if got := l.Mean(); got != 50500*time.Microsecond {
		t.Errorf("mean = %v, want 50.5ms", got)
	}
}

func TestLatenciesTimeAndEdges(t *testing.T) {
	var l Latencies
	l.Time(func() { time.Sleep(time.Millisecond) })
	if l.Count() != 1 {
		t.Fatalf("Time did not record a sample: count=%d", l.Count())
	}
	// single sample: every percentile is that sample.
	if l.Percentile(0) != l.Percentile(1) {
		t.Error("single-sample percentiles should coincide")
	}
	if l.Percentile(-5) != l.Percentile(0) || l.Percentile(5) != l.Percentile(1) {
		t.Error("out-of-range quantiles should clamp")
	}
}
