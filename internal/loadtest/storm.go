// SPDX-License-Identifier: Apache-2.0

package loadtest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
)

// Storm thresholds (spec/test-and-verification-plan.md §4 "Load"):
// 10,000 pending jobs across 100 targets, with these p99 ceilings.
const (
	MaxSchedulerCycleP99 = 2 * time.Second
	MaxAPIReadP99        = 300 * time.Millisecond
	MaxAPIWriteP99       = 1 * time.Second
)

// StormConfig parameterizes a storm run. Zero fields take CI defaults
// (10,000 jobs / 100 targets); the fleet-0 variant passes 1,000 / fewer.
type StormConfig struct {
	Jobs         int
	Targets      int
	Tenant       string
	Policy       string
	SeedWorkers  int
	APIReaders   int
	APIWriters   int
	APIReads     int // total read probes
	APIWrites    int // total write probes
	DrainTimeout time.Duration
}

func (c *StormConfig) withDefaults() {
	if c.Jobs == 0 {
		c.Jobs = 10_000
	}
	if c.Targets == 0 {
		c.Targets = 100
	}
	if c.Tenant == "" {
		c.Tenant = "acme/qa"
	}
	if c.SeedWorkers == 0 {
		c.SeedWorkers = 64
	}
	if c.APIReaders == 0 {
		c.APIReaders = 16
	}
	if c.APIWriters == 0 {
		c.APIWriters = 8
	}
	if c.APIReads == 0 {
		c.APIReads = 2_000
	}
	if c.APIWrites == 0 {
		c.APIWrites = 500
	}
	if c.DrainTimeout == 0 {
		c.DrainTimeout = 5 * time.Minute
	}
}

// StormResult is the measured outcome of a storm run.
type StormResult struct {
	Jobs, Targets     int
	Duration          time.Duration
	CycleCount        int64
	SchedulerCycleP50 time.Duration
	SchedulerCycleP99 time.Duration
	SchedulerCycleMax time.Duration
	APIReadP50        time.Duration
	APIReadP99        time.Duration
	APIWriteP50       time.Duration
	APIWriteP99       time.Duration
	PeakPending       int64
	FinalPending      int64
	Drained           bool
}

// Violations returns the list of breached thresholds (empty = all green). A
// storm passes only if every p99 is within budget, the queue drained, and it
// never grew unbounded (peak pending stays within a small multiple of the
// seeded backlog).
func (r StormResult) Violations() []string {
	var v []string
	if r.SchedulerCycleP99 > MaxSchedulerCycleP99 {
		v = append(v, fmt.Sprintf("scheduler-cycle p99 %v > %v", r.SchedulerCycleP99, MaxSchedulerCycleP99))
	}
	if r.APIReadP99 > MaxAPIReadP99 {
		v = append(v, fmt.Sprintf("api-read p99 %v > %v", r.APIReadP99, MaxAPIReadP99))
	}
	if r.APIWriteP99 > MaxAPIWriteP99 {
		v = append(v, fmt.Sprintf("api-write p99 %v > %v", r.APIWriteP99, MaxAPIWriteP99))
	}
	if !r.Drained {
		v = append(v, fmt.Sprintf("queue did not drain: %d jobs still pending", r.FinalPending))
	}
	// Unbounded-growth guard: the backlog should never exceed what we seeded
	// plus the API-write probes plus slack. If it did, arrivals outran the
	// scheduler and the queue was growing without bound.
	ceiling := int64(r.Jobs) + int64(r.Targets) + 2000
	if r.PeakPending > ceiling {
		v = append(v, fmt.Sprintf("queue grew unbounded: peak pending %d > ceiling %d", r.PeakPending, ceiling))
	}
	return v
}

func (r StormResult) String() string {
	return fmt.Sprintf(
		"storm[jobs=%d targets=%d dur=%s]: cycle p50=%v p99=%v max=%v (%d cycles) · read p50=%v p99=%v · write p50=%v p99=%v · pending peak=%d final=%d drained=%v",
		r.Jobs, r.Targets, r.Duration.Round(time.Millisecond),
		r.SchedulerCycleP50, r.SchedulerCycleP99, r.SchedulerCycleMax, r.CycleCount,
		r.APIReadP50, r.APIReadP99, r.APIWriteP50, r.APIWriteP99,
		r.PeakPending, r.FinalPending, r.Drained)
}

// RunStorm seeds a backlog, drives concurrent API read/write probes while the
// scheduler drains it, and measures the load thresholds.
func RunStorm(ctx context.Context, s *Stack, cfg StormConfig) (StormResult, error) {
	cfg.withDefaults()
	start := time.Now()

	// Seed the backlog concurrently; collect ids for read probes.
	ids := make([]string, cfg.Jobs)
	var seedErr atomic.Value
	seedJobs(ctx, s, cfg, ids, &seedErr)
	if e := seedErr.Load(); e != nil {
		return StormResult{}, fmt.Errorf("seeding backlog: %w", e.(error))
	}

	// Sample queue depth in the background to find the peak and confirm drain.
	var peak atomic.Int64
	sampleDone := make(chan struct{})
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-sampleDone:
				return
			case <-t.C:
				if n, err := s.PendingCount(ctx); err == nil {
					for {
						old := peak.Load()
						if n <= old || peak.CompareAndSwap(old, n) {
							break
						}
					}
				}
			}
		}
	}()

	// Drive API read + write probes concurrently with draining.
	readLat, writeLat := &Latencies{}, &Latencies{}
	runProbes(ctx, s, cfg, ids, readLat, writeLat)

	// Wait for the backlog to drain.
	drained, final := waitForDrain(ctx, s, cfg.DrainTimeout)
	close(sampleDone)

	return StormResult{
		Jobs: cfg.Jobs, Targets: cfg.Targets, Duration: time.Since(start),
		CycleCount:        s.Dispatcher.CycleCount(),
		SchedulerCycleP50: s.Dispatcher.CycleP50(),
		SchedulerCycleP99: s.Dispatcher.CycleP99(),
		SchedulerCycleMax: s.Dispatcher.CycleMax(),
		APIReadP50:        readLat.P50(), APIReadP99: readLat.P99(),
		APIWriteP50: writeLat.P50(), APIWriteP99: writeLat.P99(),
		PeakPending: peak.Load(), FinalPending: final, Drained: drained,
	}, nil
}

func seedJobs(ctx context.Context, s *Stack, cfg StormConfig, ids []string, seedErr *atomic.Value) {
	var wg sync.WaitGroup
	next := make(chan int, cfg.Jobs)
	for i := 0; i < cfg.Jobs; i++ {
		next <- i
	}
	close(next)
	for w := 0; w < cfg.SeedWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				id, err := s.SeedJob(ctx, cfg.Tenant, 1000)
				if err != nil {
					seedErr.Store(err)
					return
				}
				ids[i] = id
			}
		}()
	}
	wg.Wait()
}

func runProbes(ctx context.Context, s *Stack, cfg StormConfig, ids []string, readLat, writeLat *Latencies) {
	client, cctx, closeConn, err := s.Client()
	if err != nil {
		return
	}
	defer func() { _ = closeConn() }()

	var wg sync.WaitGroup
	var reads, writes atomic.Int64

	for r := 0; r < cfg.APIReaders; r++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := seed
			for reads.Add(1) <= int64(cfg.APIReads) {
				i = (i + 1) % len(ids)
				if i%3 == 0 {
					readLat.Time(func() {
						_, _ = client.ListJobs(cctx, &apiv1alpha1.ListJobsRequest{PageSize: 50})
					})
				} else {
					id := ids[i]
					readLat.Time(func() {
						_, _ = client.GetJob(cctx, &apiv1alpha1.JobRef{JobId: id})
					})
				}
			}
		}(r * 997)
	}

	doc, _ := structpb.NewStruct(minimalDoc("load-api", cfg.Tenant, 1000))
	for w := 0; w < cfg.APIWriters; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for writes.Add(1) <= int64(cfg.APIWrites) {
				writeLat.Time(func() {
					_, _ = client.SubmitJob(cctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: doc})
				})
			}
		}()
	}
	wg.Wait()
}

func waitForDrain(ctx context.Context, s *Stack, timeout time.Duration) (bool, int64) {
	deadline := time.Now().Add(timeout)
	var last int64
	for time.Now().Before(deadline) {
		n, err := s.PendingCount(ctx)
		if err == nil {
			last = n
			if n == 0 {
				return true, 0
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false, last
}
