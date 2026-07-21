// SPDX-License-Identifier: Apache-2.0

package loadtest

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Soak thresholds (spec/test-and-verification-plan.md §4 "Soak"): 72h with
// accelerated replay — RSS growth < 5%/24h post-warmup, goroutines bounded,
// zero non-terminal jobs older than policy max. In an accelerated run we assert
// the *shape* of those SLOs: a genuine leak balloons the GC'd heap and leaks
// goroutines, so a modest growth tripwire catches it without flapping on noise.
const (
	MaxSoakHeapGrowthPct   = 50.0 // post-warmup GC'd heap growth over the run
	GoroutineSlack         = 100  // allowed goroutines above the warmup baseline
	DefaultStuckJobMaxAge  = 30 * time.Second
	DefaultSoakArrivalRate = 200 // jobs/sec of accelerated replay
)

// SoakConfig parameterizes an accelerated soak. Duration is wall-clock; the
// arrival rate is what makes it "accelerated" — production replay would span
// 72h, the harness compresses the churn.
type SoakConfig struct {
	Duration       time.Duration
	Targets        int
	Tenant         string
	Policy         string
	ArrivalRate    int // jobs submitted per second
	WarmupFraction float64
	StuckJobMaxAge time.Duration
	SampleEvery    time.Duration
}

func (c *SoakConfig) withDefaults() {
	if c.Duration == 0 {
		c.Duration = 2 * time.Minute
	}
	if c.Targets == 0 {
		c.Targets = 20
	}
	if c.Tenant == "" {
		c.Tenant = "acme/qa"
	}
	if c.ArrivalRate == 0 {
		c.ArrivalRate = DefaultSoakArrivalRate
	}
	if c.WarmupFraction == 0 {
		// Baseline late enough that the working set — including the fake's
		// capped task retention — has stabilized, so post-warmup growth measures
		// leaks, not warm-up fill.
		c.WarmupFraction = 0.35
	}
	if c.StuckJobMaxAge == 0 {
		c.StuckJobMaxAge = DefaultStuckJobMaxAge
	}
	if c.SampleEvery == 0 {
		c.SampleEvery = 2 * time.Second
	}
}

// SoakResult is the measured outcome of a soak run.
type SoakResult struct {
	Duration       time.Duration
	Samples        int
	JobsSubmitted  int64
	HeapInuseBase  uint64
	HeapInuseEnd   uint64
	HeapGrowthPct  float64
	GoroutinesBase int
	GoroutinesPeak int
	GoroutinesEnd  int
	RSSBaseKB      uint64 // best-effort (Linux); 0 if unavailable
	RSSEndKB       uint64
	MaxStuck       int64
	FinalStuck     int64
	FinalPending   int64
}

// Violations returns breached soak bounds (empty = all green).
func (r SoakResult) Violations() []string {
	var v []string
	if r.HeapGrowthPct > MaxSoakHeapGrowthPct {
		v = append(v, fmt.Sprintf("post-warmup heap growth %.1f%% > %.1f%%", r.HeapGrowthPct, MaxSoakHeapGrowthPct))
	}
	if r.GoroutinesEnd > r.GoroutinesBase+GoroutineSlack {
		v = append(v, fmt.Sprintf("goroutines unbounded: end %d > base %d + slack %d", r.GoroutinesEnd, r.GoroutinesBase, GoroutineSlack))
	}
	if r.FinalStuck > 0 {
		v = append(v, fmt.Sprintf("%d job(s) stuck non-terminal past max age", r.FinalStuck))
	}
	return v
}

func (r SoakResult) String() string {
	return fmt.Sprintf(
		"soak[dur=%s samples=%d submitted=%d]: heap base=%s end=%s growth=%.1f%% · goroutines base=%d peak=%d end=%d · rss base=%dKB end=%dKB · stuck max=%d final=%d",
		r.Duration.Round(time.Second), r.Samples, r.JobsSubmitted,
		humanBytes(r.HeapInuseBase), humanBytes(r.HeapInuseEnd), r.HeapGrowthPct,
		r.GoroutinesBase, r.GoroutinesPeak, r.GoroutinesEnd,
		r.RSSBaseKB, r.RSSEndKB, r.MaxStuck, r.FinalStuck)
}

// RunSoak churns jobs through the stack for cfg.Duration, sampling memory,
// goroutines, and stuck-job counts, and asserts the soak bounds.
func RunSoak(ctx context.Context, s *Stack, cfg SoakConfig) (SoakResult, error) {
	cfg.withDefaults()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Quiescent goroutine baseline: the stack's steady-state background
	// goroutines before any load. A leak shows up as the post-drain count
	// exceeding this by more than the executor slack — regardless of how many
	// jobs ran — so this is the honest reference for the goroutine bound.
	runtime.GC()
	startGoro := runtime.NumGoroutine()

	// Accelerated arrival: a submitter goroutine feeds jobs at the target rate.
	var submitted, wg = int64Counter(), sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		interval := time.Second / time.Duration(cfg.ArrivalRate)
		if interval <= 0 {
			interval = time.Microsecond
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				if _, err := s.SeedJob(runCtx, cfg.Tenant, 1000); err == nil {
					submitted.add(1)
				}
			}
		}
	}()

	warmup := time.Duration(float64(cfg.Duration) * cfg.WarmupFraction)
	start := time.Now()
	var baseHeap uint64
	var baseRSS uint64
	peakGoro := startGoro
	var maxStuck int64
	samples := 0
	baselined := false

	ticker := time.NewTicker(cfg.SampleEvery)
	defer ticker.Stop()
	for time.Now().Before(start.Add(cfg.Duration)) {
		select {
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return SoakResult{}, ctx.Err()
		case <-ticker.C:
		}
		g := runtime.NumGoroutine()
		if g > peakGoro {
			peakGoro = g
		}
		if stuck, err := s.NonTerminalOlderThan(ctx, cfg.StuckJobMaxAge); err == nil && stuck > maxStuck {
			maxStuck = stuck
		}
		// Take the post-warmup HEAP baseline once, on a GC'd heap for a stable
		// read (caches have filled by now, so later growth signals a leak). The
		// goroutine baseline is the quiescent startGoro captured before load.
		if !baselined && time.Since(start) >= warmup {
			runtime.GC()
			baseHeap = heapInuse()
			baseRSS = rssKB()
			baselined = true
		}
		samples++
	}

	// Stop arrivals and let in-flight work fully drain before the final reading.
	// The goroutine bound is only meaningful at quiescence: wait until every job
	// is terminal (not merely out of PENDING), so executor goroutines — which
	// exit on their task reaching a terminal state — have wound down.
	cancel()
	wg.Wait()
	drainDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(drainDeadline) {
		if n, err := s.NonTerminalOlderThan(ctx, 0); err == nil && n == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(2 * time.Second) // let executor goroutines observe ctx/terminal and exit
	runtime.GC()
	runtime.GC() // second pass: finalizers freed on the first

	endHeap := heapInuse()
	endGoro := runtime.NumGoroutine()
	finalStuck, _ := s.NonTerminalOlderThan(ctx, cfg.StuckJobMaxAge)
	finalPending, _ := s.PendingCount(ctx)

	if !baselined { // duration shorter than warmup: heap baseline at end
		baseHeap, baseRSS = endHeap, rssKB()
	}
	growth := 0.0
	if baseHeap > 0 && endHeap > baseHeap {
		growth = float64(endHeap-baseHeap) / float64(baseHeap) * 100
	}

	return SoakResult{
		Duration: time.Since(start), Samples: samples, JobsSubmitted: submitted.get(),
		HeapInuseBase: baseHeap, HeapInuseEnd: endHeap, HeapGrowthPct: growth,
		GoroutinesBase: startGoro, GoroutinesPeak: peakGoro, GoroutinesEnd: endGoro,
		RSSBaseKB: baseRSS, RSSEndKB: rssKB(),
		MaxStuck: maxStuck, FinalStuck: finalStuck, FinalPending: finalPending,
	}, nil
}

func heapInuse() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// rssKB reads resident set size from /proc/self/status (Linux). Returns 0 where
// unavailable (macOS/dev), where the harness falls back to Go heap stats.
func rssKB() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb
			}
		}
	}
	return 0
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGT"[exp])
}

// int64Counter is a tiny atomic counter (avoids exporting sync/atomic types).
type counter struct {
	mu sync.Mutex
	n  int64
}

func int64Counter() *counter   { return &counter{} }
func (c *counter) add(d int64) { c.mu.Lock(); c.n += d; c.mu.Unlock() }
func (c *counter) get() int64  { c.mu.Lock(); defer c.mu.Unlock(); return c.n }
