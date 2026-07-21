// SPDX-License-Identifier: Apache-2.0

package loadtest

import (
	"context"
	"testing"
	"time"
)

func TestStorm(t *testing.T) {
	// Smoke defaults keep the normal coverage pass fast; the weekly job sets
	// RABI_STORM_JOBS=10000 RABI_STORM_TARGETS=100 for the full test-plan size.
	jobs := envInt("RABI_STORM_JOBS", 400)
	targets := envInt("RABI_STORM_TARGETS", 8)

	ctx := context.Background()
	s, err := NewStack(ctx, testStore, targets, "")
	if err != nil {
		t.Fatalf("boot stack: %v", err)
	}
	defer s.Close()

	res, err := RunStorm(ctx, s, StormConfig{
		Jobs: jobs, Targets: targets, DrainTimeout: 3 * time.Minute,
	})
	if err != nil {
		t.Fatalf("storm: %v", err)
	}
	t.Log(res.String())

	if vs := res.Violations(); len(vs) > 0 {
		for _, v := range vs {
			t.Errorf("THRESHOLD BREACH: %s", v)
		}
	}
	if res.CycleCount == 0 {
		t.Error("scheduler never cycled")
	}
	if res.APIReadP99 == 0 || res.APIWriteP99 == 0 {
		t.Error("API probes recorded no latency")
	}
}
