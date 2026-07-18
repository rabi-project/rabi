// SPDX-License-Identifier: Apache-2.0

// Package adaptertest provides an in-process fake adapter for control-plane
// tests: instant simulated execution, injectable failures, spec-shaped
// snapshots. The real-physics reference adapter lives in adapters/aer; this
// fake exists so Go component tests need no Python.
package adaptertest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
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
	// StepDelay is the pause between task states (default 10ms).
	StepDelay time.Duration
}

type task struct {
	status *adapterv1alpha1.TaskStatus
	cancel bool
}

// Fake implements AdapterService in memory.
type Fake struct {
	adapterv1alpha1.UnimplementedAdapterServiceServer
	mu      sync.Mutex
	targets map[string]*TargetSpec
	tasks   map[string]*task
	byKey   map[string]string
	nextID  int
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
		BillingUnits:   []string{"shots", "tasks"},
		CouplingClass:  "loose",
	}, nil
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
	tk := &task{status: &adapterv1alpha1.TaskStatus{
		Task: &adapterv1alpha1.TaskRef{
			Target: &adapterv1alpha1.TargetRef{TargetId: s.ID}, TaskId: id},
		State:     adapterv1alpha1.TaskStatus_QUEUED,
		UpdatedAt: timestamppb.Now(),
	}}
	f.tasks[id] = tk
	go f.advance(s, id, req.GetShots())
	return &adapterv1alpha1.TaskHandle{
		Target: &adapterv1alpha1.TargetRef{TargetId: s.ID}, TaskId: id}, nil
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
		if s.FailWith != nil {
			tk.status.State = adapterv1alpha1.TaskStatus_FAILED
			tk.status.Error = s.FailWith
			return
		}
		counts, _ := json.Marshal(map[string]any{
			"counts": map[string]int{"00": int(shots) / 2, "11": int(shots) - int(shots)/2},
		})
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
		Simulator:   true,
	}
}
