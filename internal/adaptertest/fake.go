// SPDX-License-Identifier: Apache-2.0

// Package adaptertest provides an in-process fake adapter for control-plane
// tests: instant simulated execution, injectable failures, spec-shaped
// snapshots. The real-physics reference adapter lives in adapters/aer; this
// fake exists so Go component tests need no Python.
package adaptertest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
)

// TargetSpec configures one fake target.
type TargetSpec struct {
	ID         string
	Qubits     uint32
	Formats    []string
	MaxShots   uint64
	SnapshotID string
	Metrics    []*adapterv1alpha1.Metric
	// FailWith, when set, makes every task fail with this detail.
	FailWith *adapterv1alpha1.ErrorDetail
	// Broken-adapter knobs for the harness self-test (M7): each one makes
	// exactly one conformance category fail.
	IgnoreMaxShots bool // declare MaxShots but accept above it (cat 1)
	BrokenSessions bool // declare sessions but refuse OpenSession (cat 8)
	// StepDelay is the pause between task states (default 10ms).
	StepDelay time.Duration
}

type task struct {
	status   *adapterv1alpha1.TaskStatus
	cancel   bool
	failWith *adapterv1alpha1.ErrorDetail
	key      string // idempotency key, for eviction from byKey
}

// fakeTaskCap bounds how many task records the in-process fake retains. A real
// adapter is a separate process, so its backend history never counts against
// the control plane's memory; without a cap the fake would accumulate one task
// per job forever and confound the load & soak harness's heap measurement
// (P2.M2). Far above any functional test's task count, so those are unaffected.
const fakeTaskCap = 8192

// Fake implements AdapterService in memory.
type Fake struct {
	adapterv1alpha1.UnimplementedAdapterServiceServer
	mu       sync.Mutex
	targets  map[string]*TargetSpec
	tasks    map[string]*task
	byKey    map[string]string
	order    []string // task ids in submission order, for bounded eviction
	nextID   int
	sessions map[string]bool
	garbage  atomic.Bool // when set, results come back semantically corrupt
}

// SetGarbage toggles corrupt-result mode: SUCCEEDED tasks return an
// unparseable result body, so the control plane must degrade gracefully
// rather than crash (chaos scenario "garbage from an adapter").
func (f *Fake) SetGarbage(on bool) { f.garbage.Store(on) }

// ServeControllable serves on addr ("127.0.0.1:0" for an ephemeral port) and
// returns the bound address plus a stop func the caller controls — chaos uses
// it to kill and restart the adapter on a fixed port. Unlike Serve, it does
// not register test cleanup; the caller owns the lifecycle.
func (f *Fake) ServeControllable(addr string) (string, func(), error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	srv := grpc.NewServer()
	adapterv1alpha1.RegisterAdapterServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), srv.Stop, nil
}

func New(specs ...*TargetSpec) *Fake {
	f := &Fake{targets: map[string]*TargetSpec{}, tasks: map[string]*task{}, byKey: map[string]string{}}
	for _, s := range specs {
		if s.StepDelay == 0 {
			s.StepDelay = 10 * time.Millisecond
		}
		if s.MaxShots == 0 {
			s.MaxShots = 100000
		}
		if s.SnapshotID == "" {
			s.SnapshotID = "fake-snap-1"
		}
		if len(s.Metrics) == 0 {
			s.Metrics = []*adapterv1alpha1.Metric{
				{Name: "gate.2q.cx.error", Value: 0.005, Qubits: []uint32{0, 1},
					Methodology: "synthetic-fixture"},
				{Name: "readout.error", Value: 0.01, Qubits: []uint32{0},
					Methodology: "synthetic-fixture"},
			}
		}
		f.targets[s.ID] = s
	}
	return f
}

// Serve starts a gRPC server on a loopback port and returns its address.
func (f *Fake) Serve(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	adapterv1alpha1.RegisterAdapterServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func (f *Fake) spec(id string) (*TargetSpec, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.targets[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "target %q not served here", id)
	}
	return s, nil
}

func (f *Fake) ListTargets(context.Context, *adapterv1alpha1.ListTargetsRequest) (*adapterv1alpha1.ListTargetsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &adapterv1alpha1.ListTargetsResponse{}
	for id := range f.targets {
		resp.Targets = append(resp.Targets, targetInfo(id))
	}
	return resp, nil
}

func (f *Fake) GetCapabilities(_ context.Context, ref *adapterv1alpha1.TargetRef) (*adapterv1alpha1.Capabilities, error) {
	s, err := f.spec(ref.GetTargetId())
	if err != nil {
		return nil, err
	}
	return &adapterv1alpha1.Capabilities{
		Target:         targetInfo(s.ID),
		NumQubits:      s.Qubits,
		ProgramFormats: s.Formats,
		MaxShots:       s.MaxShots,
		Cancellation:   true,
		Sessions:       true,
		BillingUnits:   []string{"shots", "tasks"},
		CouplingClass:  "loose",
	}, nil
}

// AddSession pre-registers a live session id (dispatcher-test fixtures
// that fabricate control-plane session rows need the adapter to honor the
// adapter-side id they invented).
func (f *Fake) AddSession(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessions == nil {
		f.sessions = map[string]bool{}
	}
	f.sessions[id] = true
}

// Sessions: minimal spec-shaped session support for dispatcher tests.
func (f *Fake) OpenSession(_ context.Context, req *adapterv1alpha1.OpenSessionRequest) (*adapterv1alpha1.SessionHandle, error) {
	if s, err := f.spec(req.GetTarget().GetTargetId()); err == nil && s.BrokenSessions {
		return nil, status.Error(codes.Unimplemented, "sessions declared but not implemented (self-test fixture)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessions == nil {
		f.sessions = map[string]bool{}
	}
	f.nextID++
	id := fmt.Sprintf("fake-sess-%d", f.nextID)
	f.sessions[id] = true
	exp := time.Now().Add(req.GetMaxDuration().AsDuration())
	if req.GetMaxDuration().AsDuration() <= 0 {
		exp = time.Now().Add(time.Hour)
	}
	return &adapterv1alpha1.SessionHandle{
		Target:    req.GetTarget(),
		SessionId: id,
		ExpiresAt: timestamppb.New(exp),
	}, nil
}

func (f *Fake) CloseSession(_ context.Context, handle *adapterv1alpha1.SessionHandle) (*adapterv1alpha1.CloseSessionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, handle.GetSessionId())
	return &adapterv1alpha1.CloseSessionResponse{}, nil
}

func (f *Fake) GetDeviceState(_ context.Context, ref *adapterv1alpha1.TargetRef) (*adapterv1alpha1.DeviceState, error) {
	s, err := f.spec(ref.GetTargetId())
	if err != nil {
		return nil, err
	}
	return &adapterv1alpha1.DeviceState{
		Target:        &adapterv1alpha1.TargetRef{TargetId: s.ID},
		Status:        adapterv1alpha1.DeviceState_ONLINE,
		EstimatedWait: durationpb.New(2 * time.Second),
		Calibration: &adapterv1alpha1.CalibrationSnapshot{
			SnapshotId: s.SnapshotID,
			MeasuredAt: timestamppb.Now(),
			Source:     "adaptertest",
			Metrics:    s.Metrics,
		},
		ObservedAt: timestamppb.Now(),
	}, nil
}

func (f *Fake) SubmitTask(_ context.Context, req *adapterv1alpha1.SubmitTaskRequest) (*adapterv1alpha1.TaskHandle, error) {
	s, err := f.spec(req.GetTarget().GetTargetId())
	if err != nil {
		return nil, err
	}
	if req.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key is required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := s.ID + "/" + req.GetIdempotencyKey()
	if id, ok := f.byKey[key]; ok {
		return &adapterv1alpha1.TaskHandle{
			Target: &adapterv1alpha1.TargetRef{TargetId: s.ID}, TaskId: id}, nil
	}
	f.nextID++
	id := fmt.Sprintf("fake-task-%d", f.nextID)
	f.byKey[key] = id
	var failWith *adapterv1alpha1.ErrorDetail
	if !slices.Contains(s.Formats, req.GetPayload().GetFormat()) {
		failWith = &adapterv1alpha1.ErrorDetail{
			Category:      adapterv1alpha1.ErrorDetail_CAPABILITY_MISMATCH,
			VendorMessage: "undeclared program format",
		}
	} else if req.GetSessionId() != "" && !f.sessions[req.GetSessionId()] {
		failWith = &adapterv1alpha1.ErrorDetail{
			Category:      adapterv1alpha1.ErrorDetail_SESSION_LOST,
			Retriable:     true,
			VendorMessage: "unknown or closed session",
		}
	} else if bytes.Contains(req.GetPayload().GetInline(), []byte("not a program")) {
		failWith = &adapterv1alpha1.ErrorDetail{
			Category:      adapterv1alpha1.ErrorDetail_INVALID_PROGRAM,
			VendorMessage: "unparseable program",
		}
	} else if s.MaxShots > 0 && req.GetShots() > s.MaxShots && !s.IgnoreMaxShots {
		failWith = &adapterv1alpha1.ErrorDetail{
			Category:      adapterv1alpha1.ErrorDetail_CAPABILITY_MISMATCH,
			VendorMessage: "shots above declared max_shots",
		}
	}
	tk := &task{failWith: failWith, key: key, status: &adapterv1alpha1.TaskStatus{
		Task: &adapterv1alpha1.TaskRef{
			Target: &adapterv1alpha1.TargetRef{TargetId: s.ID}, TaskId: id},
		State:     adapterv1alpha1.TaskStatus_QUEUED,
		UpdatedAt: timestamppb.Now(),
	}}
	f.tasks[id] = tk
	f.order = append(f.order, id)
	f.evictTerminal()
	go f.advance(s, id, req.GetShots())
	return &adapterv1alpha1.TaskHandle{
		Target: &adapterv1alpha1.TargetRef{TargetId: s.ID}, TaskId: id}, nil
}

// evictTerminal drops the oldest terminal tasks once retention exceeds the cap,
// bounding the fake's memory during a long soak. Non-terminal tasks are kept
// (an executor may still be watching), so eviction never strands live work.
// Caller holds f.mu.
func (f *Fake) evictTerminal() {
	if len(f.order) <= fakeTaskCap {
		return
	}
	target := len(f.order) - fakeTaskCap
	kept := f.order[:0]
	removed := 0
	for _, id := range f.order {
		t, ok := f.tasks[id]
		if ok && removed < target && isTerminalState(t.status.GetState()) {
			delete(f.tasks, id)
			delete(f.byKey, t.key)
			removed++
			continue
		}
		kept = append(kept, id)
	}
	f.order = kept
}

func isTerminalState(s adapterv1alpha1.TaskStatus_State) bool {
	switch s {
	case adapterv1alpha1.TaskStatus_SUCCEEDED, adapterv1alpha1.TaskStatus_FAILED,
		adapterv1alpha1.TaskStatus_CANCELLED:
		return true
	}
	return false
}

func (f *Fake) advance(s *TargetSpec, id string, shots uint64) {
	step := func(mut func(*task)) bool {
		time.Sleep(s.StepDelay)
		f.mu.Lock()
		defer f.mu.Unlock()
		tk := f.tasks[id]
		switch tk.status.GetState() {
		case adapterv1alpha1.TaskStatus_SUCCEEDED, adapterv1alpha1.TaskStatus_FAILED,
			adapterv1alpha1.TaskStatus_CANCELLED:
			return false
		}
		if tk.cancel {
			tk.status.State = adapterv1alpha1.TaskStatus_CANCELLED
			tk.status.UpdatedAt = timestamppb.Now()
			return false
		}
		mut(tk)
		tk.status.UpdatedAt = timestamppb.Now()
		return true
	}

	if !step(func(tk *task) { tk.status.State = adapterv1alpha1.TaskStatus_RUNNING }) {
		return
	}
	step(func(tk *task) {
		if tk.failWith != nil {
			tk.status.State = adapterv1alpha1.TaskStatus_FAILED
			tk.status.Error = tk.failWith
			return
		}
		if s.FailWith != nil {
			tk.status.State = adapterv1alpha1.TaskStatus_FAILED
			tk.status.Error = s.FailWith
			return
		}
		counts, _ := json.Marshal(map[string]any{
			"counts": map[string]int{"00": int(shots) / 2, "11": int(shots) - int(shots)/2},
		})
		if f.garbage.Load() {
			// A result the control plane cannot parse: it must reach a
			// queryable terminal state, never panic.
			counts = []byte("\x00\xff not json at all \xfe")
		}
		tk.status.State = adapterv1alpha1.TaskStatus_SUCCEEDED
		tk.status.Result = &adapterv1alpha1.Result{
			Format: "counts-json",
			Body:   &adapterv1alpha1.Result_Inline{Inline: counts},
		}
		tk.status.Usage = []*adapterv1alpha1.UsageRecord{
			{Unit: "shots", Amount: float64(shots), RecordedAt: timestamppb.Now()},
			{Unit: "tasks", Amount: 1, RecordedAt: timestamppb.Now()},
		}
	})
}

func (f *Fake) getTask(ref *adapterv1alpha1.TaskRef) (*task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tk, ok := f.tasks[ref.GetTaskId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "task %q not found", ref.GetTaskId())
	}
	return tk, nil
}

func (f *Fake) GetTask(_ context.Context, ref *adapterv1alpha1.TaskRef) (*adapterv1alpha1.TaskStatus, error) {
	tk, err := f.getTask(ref)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return tk.status, nil
}

func (f *Fake) WatchTask(ref *adapterv1alpha1.TaskRef, stream adapterv1alpha1.AdapterService_WatchTaskServer) error {
	if _, err := f.getTask(ref); err != nil {
		return err
	}
	var last adapterv1alpha1.TaskStatus_State = -1
	for {
		f.mu.Lock()
		st := f.tasks[ref.GetTaskId()].status
		f.mu.Unlock()
		if st.GetState() != last {
			if err := stream.Send(st); err != nil {
				return err
			}
			last = st.GetState()
			switch last {
			case adapterv1alpha1.TaskStatus_SUCCEEDED, adapterv1alpha1.TaskStatus_FAILED,
				adapterv1alpha1.TaskStatus_CANCELLED:
				return nil
			}
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (f *Fake) CancelTask(_ context.Context, ref *adapterv1alpha1.TaskRef) (*adapterv1alpha1.CancelTaskResponse, error) {
	tk, err := f.getTask(ref)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch tk.status.GetState() {
	case adapterv1alpha1.TaskStatus_SUCCEEDED, adapterv1alpha1.TaskStatus_FAILED,
		adapterv1alpha1.TaskStatus_CANCELLED:
		return &adapterv1alpha1.CancelTaskResponse{Accepted: false}, nil
	}
	tk.cancel = true
	return &adapterv1alpha1.CancelTaskResponse{Accepted: true}, nil
}

func targetInfo(id string) *adapterv1alpha1.TargetInfo {
	return &adapterv1alpha1.TargetInfo{
		TargetId:    id,
		DisplayName: "Fake " + id,
		Vendor:      "adaptertest",
		Modality:    "gate-model",
		Technology:  "simulator",
		Simulator:   true,
	}
}
