// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"sort"
	"sync"
	"time"
)

// cycleRing records the most recent scheduler-cycle durations in a fixed-size
// ring buffer. A ring, not an unbounded slice, so p99 stays computable in a
// long-running process (a 72h soak) without the sample set growing without
// bound — the load & soak harness (P2.M2) reads it in-process, and it is cheap
// enough to record on every cycle.
type cycleRing struct {
	mu    sync.Mutex
	buf   []time.Duration
	next  int   // write index
	count int   // live samples (<= len(buf))
	total int64 // lifetime cycles recorded
}

func newCycleRing(size int) *cycleRing {
	if size <= 0 {
		size = 8192
	}
	return &cycleRing{buf: make([]time.Duration, size)}
}

func (c *cycleRing) record(d time.Duration) {
	c.mu.Lock()
	c.buf[c.next] = d
	c.next = (c.next + 1) % len(c.buf)
	if c.count < len(c.buf) {
		c.count++
	}
	c.total++
	c.mu.Unlock()
}

// percentile returns the q-quantile (0..1) of the retained samples, 0 if empty.
func (c *cycleRing) percentile(q float64) time.Duration {
	c.mu.Lock()
	live := make([]time.Duration, c.count)
	copy(live, c.buf[:c.count])
	c.mu.Unlock()
	if len(live) == 0 {
		return 0
	}
	sort.Slice(live, func(i, j int) bool { return live[i] < live[j] })
	if q <= 0 {
		return live[0]
	}
	if q >= 1 {
		return live[len(live)-1]
	}
	rank := int(q*float64(len(live)) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(live) {
		rank = len(live)
	}
	return live[rank-1]
}

func (c *cycleRing) lifetime() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// CycleP50, CycleP99, CycleMax report scheduler-cycle latency percentiles over
// the retained window. The load harness asserts CycleP99 < 2s (test plan §4).
func (d *Dispatcher) CycleP50() time.Duration { return d.cycles.percentile(0.50) }
func (d *Dispatcher) CycleP99() time.Duration { return d.cycles.percentile(0.99) }
func (d *Dispatcher) CycleMax() time.Duration { return d.cycles.percentile(1.0) }

// CycleCount is the lifetime number of scheduler cycles run.
func (d *Dispatcher) CycleCount() int64 { return d.cycles.lifetime() }
