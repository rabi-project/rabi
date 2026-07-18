// SPDX-License-Identifier: Apache-2.0

// Package conformance implements the adapter certification suite from the
// spec (spec/conformance/README.md). The same categories that will certify
// third-party drivers run against our own adapters in CI first — we are
// never our own exception.
//
// Usage: point Run at a live adapter endpoint. Category 8 (sessions) is
// exercised only when the adapter declares the capability; declaring nothing
// is always legal.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
)

// T is the subset of *testing.T the suite needs (keeps the package usable
// from non-test harnesses later).
type T interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
	Run(name string, f func(t T)) bool
}

const (
	validQASM = `
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
`
	invalidQASM = "OPENQASM 3.0; definitely not a program;"
	delayParam  = "rabi.sim/delay-ms"
)

// Suite drives the categories against one target of one adapter.
type Suite struct {
	Client   adapterv1alpha1.AdapterServiceClient
	TargetID string
	Caps     *adapterv1alpha1.Capabilities
	// KeyPrefix isolates idempotency keys between runs.
	KeyPrefix string
}

// Dial connects and fetches capabilities for the named target.
func Dial(ctx context.Context, addr, targetID string) (*Suite, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	client := adapterv1alpha1.NewAdapterServiceClient(conn)
	caps, err := client.GetCapabilities(ctx, &adapterv1alpha1.TargetRef{TargetId: targetID})
	if err != nil {
		return nil, fmt.Errorf("GetCapabilities(%s): %w", targetID, err)
	}
	return &Suite{
		Client: client, TargetID: targetID, Caps: caps,
		KeyPrefix: fmt.Sprintf("conf-%d-", time.Now().UnixNano()),
	}, nil
}

// Run executes every applicable category.
func (s *Suite) Run(ctx context.Context, t T) {
	t.Run("cat1-capability-honesty", func(t T) { s.CapabilityHonesty(ctx, t) })
	t.Run("cat2-state-machine", func(t T) { s.StateMachine(ctx, t) })
	t.Run("cat3-idempotency", func(t T) { s.Idempotency(ctx, t) })
	if s.Caps.GetCancellation() {
		t.Run("cat4-cancellation", func(t T) { s.Cancellation(ctx, t) })
	}
	t.Run("cat5-provenance", func(t T) { s.Provenance(ctx, t) })
	t.Run("cat6-usage", func(t T) { s.Usage(ctx, t) })
	t.Run("cat7-error-taxonomy", func(t T) { s.ErrorTaxonomy(ctx, t) })
	if !s.Caps.GetSessions() {
		t.Run("cat8-sessions-undeclared", func(t T) { s.SessionsUndeclared(ctx, t) })
	}
}

func (s *Suite) submit(ctx context.Context, key, format string, program []byte, shots uint64,
	params map[string]string) (*adapterv1alpha1.TaskHandle, error) {
	return s.Client.SubmitTask(ctx, &adapterv1alpha1.SubmitTaskRequest{
		Target:         &adapterv1alpha1.TargetRef{TargetId: s.TargetID},
		IdempotencyKey: s.KeyPrefix + key,
		Payload: &adapterv1alpha1.Payload{
			Format: format,
			Body:   &adapterv1alpha1.Payload_Inline{Inline: program},
		},
		Shots:      shots,
		Parameters: params,
	})
}

func (s *Suite) awaitTerminal(ctx context.Context, t T, handle *adapterv1alpha1.TaskHandle) *adapterv1alpha1.TaskStatus {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Client.GetTask(ctx, &adapterv1alpha1.TaskRef{
			Target: handle.GetTarget(), TaskId: handle.GetTaskId()})
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		switch st.GetState() {
		case adapterv1alpha1.TaskStatus_SUCCEEDED, adapterv1alpha1.TaskStatus_FAILED,
			adapterv1alpha1.TaskStatus_CANCELLED:
			return st
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s never reached a terminal state", handle.GetTaskId())
	return nil
}

// CapabilityHonesty — category 1.
func (s *Suite) CapabilityHonesty(ctx context.Context, t T) {
	formats := s.Caps.GetProgramFormats()
	if len(formats) == 0 {
		t.Fatalf("adapter declares no program formats")
	}
	if len(s.Caps.GetBillingUnits()) == 0 {
		t.Fatalf("adapter declares no billing units")
	}

	// Declared format accepts a valid program.
	h, err := s.submit(ctx, "cap-valid", formats[0], []byte(validQASM), 100, nil)
	if err != nil {
		t.Fatalf("valid program rejected at submit: %v", err)
	}
	if st := s.awaitTerminal(ctx, t, h); st.GetState() != adapterv1alpha1.TaskStatus_SUCCEEDED {
		t.Fatalf("valid program did not succeed: %v (%v)", st.GetState(), st.GetError())
	}

	// Declared format rejects an invalid program with INVALID_PROGRAM.
	h, err = s.submit(ctx, "cap-invalid", formats[0], []byte(invalidQASM), 100, nil)
	if err == nil {
		st := s.awaitTerminal(ctx, t, h)
		if st.GetState() != adapterv1alpha1.TaskStatus_FAILED ||
			st.GetError().GetCategory() != adapterv1alpha1.ErrorDetail_INVALID_PROGRAM {
			t.Errorf("invalid program: want FAILED/INVALID_PROGRAM, got %v/%v",
				st.GetState(), st.GetError().GetCategory())
		}
	}

	// Declared max_shots is enforced.
	if maxShots := s.Caps.GetMaxShots(); maxShots > 0 {
		h, err = s.submit(ctx, "cap-shots", formats[0], []byte(validQASM), maxShots+1, nil)
		if err == nil {
			st := s.awaitTerminal(ctx, t, h)
			if st.GetState() != adapterv1alpha1.TaskStatus_FAILED {
				t.Errorf("shots above max_shots must fail, got %v", st.GetState())
			}
		}
	}
}

// StateMachine — category 2.
func (s *Suite) StateMachine(ctx context.Context, t T) {
	h, err := s.submit(ctx, "fsm", s.Caps.GetProgramFormats()[0], []byte(validQASM), 200,
		map[string]string{delayParam: "300"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	stream, err := s.Client.WatchTask(ctx, &adapterv1alpha1.TaskRef{
		Target: h.GetTarget(), TaskId: h.GetTaskId()})
	if err != nil {
		t.Fatalf("WatchTask: %v", err)
	}
	rank := map[adapterv1alpha1.TaskStatus_State]int{
		adapterv1alpha1.TaskStatus_QUEUED: 0, adapterv1alpha1.TaskStatus_RUNNING: 1,
		adapterv1alpha1.TaskStatus_SUCCEEDED: 2, adapterv1alpha1.TaskStatus_FAILED: 2,
		adapterv1alpha1.TaskStatus_CANCELLED: 2,
	}
	last := -1
	var lastTS time.Time
	var final *adapterv1alpha1.TaskStatus
	for {
		st, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("watch recv: %v", err)
		}
		r, known := rank[st.GetState()]
		if !known {
			t.Fatalf("unknown state %v", st.GetState())
		}
		if r < last {
			t.Errorf("state moved backwards: %v after rank %d", st.GetState(), last)
		}
		ts := st.GetUpdatedAt().AsTime()
		if ts.Before(lastTS) {
			t.Errorf("updated_at moved backwards: %v < %v", ts, lastTS)
		}
		last, lastTS, final = r, ts, st
		if r == 2 {
			break
		}
	}
	if final == nil {
		t.Fatalf("watch delivered nothing")
	}

	// Polled GetTask agrees with the watched terminal state, and terminal
	// states are immutable.
	for range 3 {
		polled, err := s.Client.GetTask(ctx, &adapterv1alpha1.TaskRef{
			Target: h.GetTarget(), TaskId: h.GetTaskId()})
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if polled.GetState() != final.GetState() {
			t.Errorf("GetTask %v != watched terminal %v", polled.GetState(), final.GetState())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Idempotency — category 3, at the T2.idem bar: 1,000 concurrent submissions
// of one key → exactly one task and one execution.
func (s *Suite) Idempotency(ctx context.Context, t T) {
	const n = 1000
	format := s.Caps.GetProgramFormats()[0]
	ids := make(chan string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h, err := s.submit(ctx, "idem", format, []byte(validQASM), 100, nil)
			if err != nil {
				t.Errorf("submit: %v", err)
				return
			}
			ids <- h.GetTaskId()
		}()
	}
	close(start)
	wg.Wait()
	close(ids)

	distinct := map[string]bool{}
	var one string
	for id := range ids {
		distinct[id] = true
		one = id
	}
	if len(distinct) != 1 {
		t.Fatalf("%d concurrent submissions produced %d distinct tasks, want 1", n, len(distinct))
	}

	st := s.awaitTerminal(ctx, t, &adapterv1alpha1.TaskHandle{
		Target: &adapterv1alpha1.TargetRef{TargetId: s.TargetID}, TaskId: one})
	if st.GetState() != adapterv1alpha1.TaskStatus_SUCCEEDED {
		t.Fatalf("idempotent task failed: %v", st.GetError())
	}
	// Exactly one usage record per unit — never n.
	perUnit := map[string]int{}
	for _, u := range st.GetUsage() {
		perUnit[u.GetUnit()]++
	}
	for unit, count := range perUnit {
		if count != 1 {
			t.Errorf("unit %s recorded %d times, want 1", unit, count)
		}
	}
}

// Cancellation — category 4 (declared).
func (s *Suite) Cancellation(ctx context.Context, t T) {
	format := s.Caps.GetProgramFormats()[0]
	// Occupy the queue so the second task stays QUEUED.
	slow, err := s.submit(ctx, "cancel-slow", format, []byte(validQASM), 100,
		map[string]string{delayParam: "1500"})
	if err != nil {
		t.Fatalf("submit slow: %v", err)
	}
	queued, err := s.submit(ctx, "cancel-queued", format, []byte(validQASM), 100, nil)
	if err != nil {
		t.Fatalf("submit queued: %v", err)
	}
	resp, err := s.Client.CancelTask(ctx, &adapterv1alpha1.TaskRef{
		Target: queued.GetTarget(), TaskId: queued.GetTaskId()})
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if !resp.GetAccepted() {
		t.Fatalf("cancel of QUEUED task not accepted")
	}
	st := s.awaitTerminal(ctx, t, queued)
	if st.GetState() != adapterv1alpha1.TaskStatus_CANCELLED {
		t.Errorf("queued+cancelled task must be CANCELLED, got %v", st.GetState())
	}
	if len(st.GetUsage()) != 0 {
		t.Errorf("never-run cancel must report zero usage, got %v", st.GetUsage())
	}
	// The slow task still converges to a terminal state.
	if st := s.awaitTerminal(ctx, t, slow); st.GetState() == adapterv1alpha1.TaskStatus_QUEUED {
		t.Errorf("slow task never converged")
	}
}

// Provenance — category 5.
func (s *Suite) Provenance(ctx context.Context, t T) {
	state1, err := s.Client.GetDeviceState(ctx, &adapterv1alpha1.TargetRef{TargetId: s.TargetID})
	if err != nil {
		t.Fatalf("GetDeviceState: %v", err)
	}
	cal := state1.GetCalibration()
	if cal.GetSnapshotId() == "" {
		t.Fatalf("snapshot_id empty")
	}
	if cal.GetMeasuredAt() == nil {
		t.Errorf("snapshot lacks measured_at")
	}
	if cal.GetSource() == "" {
		t.Errorf("snapshot lacks source")
	}
	if len(cal.GetMetrics()) == 0 {
		t.Fatalf("snapshot has no metrics")
	}
	for _, m := range cal.GetMetrics() {
		if m.GetMethodology() == "" {
			t.Errorf("metric %s lacks methodology", m.GetName())
		}
		if m.GetName() == "" {
			t.Errorf("metric with empty name")
		}
	}
	// snapshot_id stable for identical data.
	state2, err := s.Client.GetDeviceState(ctx, &adapterv1alpha1.TargetRef{TargetId: s.TargetID})
	if err != nil {
		t.Fatalf("GetDeviceState: %v", err)
	}
	if state2.GetCalibration().GetSnapshotId() != cal.GetSnapshotId() {
		t.Errorf("snapshot_id changed with identical data: %s → %s",
			cal.GetSnapshotId(), state2.GetCalibration().GetSnapshotId())
	}
}

// Usage — category 6.
func (s *Suite) Usage(ctx context.Context, t T) {
	declared := map[string]bool{}
	for _, u := range s.Caps.GetBillingUnits() {
		declared[u] = true
	}
	h, err := s.submit(ctx, "usage", s.Caps.GetProgramFormats()[0], []byte(validQASM), 321, nil)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	st := s.awaitTerminal(ctx, t, h)
	if st.GetState() != adapterv1alpha1.TaskStatus_SUCCEEDED {
		t.Fatalf("usage probe failed: %v", st.GetError())
	}
	if len(st.GetUsage()) == 0 {
		t.Fatalf("terminal SUCCEEDED task reports no usage")
	}
	for _, u := range st.GetUsage() {
		if !declared[u.GetUnit()] {
			t.Errorf("usage unit %q not in declared billing_units", u.GetUnit())
		}
		if u.GetAmount() <= 0 {
			t.Errorf("executed work must report nonzero usage, got %v %s", u.GetAmount(), u.GetUnit())
		}
		if u.GetRecordedAt() == nil {
			t.Errorf("usage record lacks recorded_at")
		}
	}
}

// ErrorTaxonomy — category 7.
func (s *Suite) ErrorTaxonomy(ctx context.Context, t T) {
	format := s.Caps.GetProgramFormats()[0]

	h, err := s.submit(ctx, "tax-bad", format, []byte(invalidQASM), 100, nil)
	if err == nil {
		st := s.awaitTerminal(ctx, t, h)
		e := st.GetError()
		if e.GetCategory() != adapterv1alpha1.ErrorDetail_INVALID_PROGRAM {
			t.Errorf("bad program: want INVALID_PROGRAM, got %v (%s)", e.GetCategory(), e.GetVendorMessage())
		}
	}

	h, err = s.submit(ctx, "tax-format", "no-such-format", []byte(validQASM), 100, nil)
	if err == nil {
		st := s.awaitTerminal(ctx, t, h)
		e := st.GetError()
		if e.GetCategory() != adapterv1alpha1.ErrorDetail_CAPABILITY_MISMATCH &&
			e.GetCategory() != adapterv1alpha1.ErrorDetail_INVALID_PROGRAM {
			t.Errorf("undeclared format: want CAPABILITY_MISMATCH, got %v", e.GetCategory())
		}
		if e.GetCategory() == adapterv1alpha1.ErrorDetail_CATEGORY_UNSPECIFIED {
			t.Errorf("bare/unspecified category on induced failure")
		}
	}

	// Unknown target must be a clean NOT_FOUND, not a crash or empty answer.
	_, err = s.Client.GetDeviceState(ctx, &adapterv1alpha1.TargetRef{TargetId: "no-such-target"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown target: want NotFound, got %v", status.Code(err))
	}
}

// SessionsUndeclared — an adapter that does not declare sessions must refuse
// session RPCs rather than half-implement them.
func (s *Suite) SessionsUndeclared(ctx context.Context, t T) {
	_, err := s.Client.OpenSession(ctx, &adapterv1alpha1.OpenSessionRequest{
		Target: &adapterv1alpha1.TargetRef{TargetId: s.TargetID}})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("OpenSession on sessionless adapter: want Unimplemented, got %v", status.Code(err))
	}
}
