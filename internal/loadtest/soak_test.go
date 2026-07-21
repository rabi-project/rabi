// SPDX-License-Identifier: Apache-2.0

package loadtest

import (
	"context"
	"testing"
	"time"
)

func TestSoak(t *testing.T) {
	// Smoke default: a short accelerated soak. The monthly job sets
	// RABI_SOAK_SECONDS (e.g. 3600) for a long run.
	seconds := envInt("RABI_SOAK_SECONDS", 12)
	targets := envInt("RABI_SOAK_TARGETS", 6)
	rate := envInt("RABI_SOAK_RATE", 100)

	ctx := context.Background()
	s, err := NewStack(ctx, testStore, targets, "")
	if err != nil {
		t.Fatalf("boot stack: %v", err)
	}
	defer s.Close()

	res, err := RunSoak(ctx, s, SoakConfig{
		Duration:    time.Duration(seconds) * time.Second,
		Targets:     targets,
		ArrivalRate: rate,
		SampleEvery: time.Second,
	})
	if err != nil {
		t.Fatalf("soak: %v", err)
	}
	t.Log(res.String())

	if vs := res.Violations(); len(vs) > 0 {
		for _, v := range vs {
			t.Errorf("SOAK BOUND BREACH: %s", v)
		}
	}
	if res.JobsSubmitted == 0 {
		t.Error("soak submitted no jobs")
	}
}
