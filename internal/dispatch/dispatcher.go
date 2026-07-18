// SPDX-License-Identifier: Apache-2.0

// Package dispatch moves accepted jobs through the fleet: it claims PENDING
// jobs from the Postgres work queue (FOR UPDATE SKIP LOCKED + LISTEN/NOTIFY
// wakeups), binds them to a feasible target, drives the adapter task to a
// terminal state, and accounts native-unit usage.
//
// At M2 target selection is "the first feasible target" — M3 replaces that
// one function with the policy pipeline (filter → score → bind) without
// touching the execution machinery here.
package dispatch

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/scheduler"
	"github.com/rabi-project/rabi/internal/store"
)

const (
	cycleEvery   = 5 * time.Second
	claimBatch   = 32
	watchBackoff = time.Second
)

// Dispatcher owns the job execution loop. One instance per rabi process.
type Dispatcher struct {
	store  *store.Store
	reg    *registry.Registry
	policy scheduler.SchedulingPolicy
	now    func() time.Time
	logger *slog.Logger

	mu       sync.Mutex
	inFlight map[string]bool // job ids with an active executor goroutine
	wg       sync.WaitGroup
}

// New wires the dispatcher with the named scheduling policy (RABI_POLICY;
// empty = fifo/v0).
func New(st *store.Store, reg *registry.Registry, policyName string, logger *slog.Logger) (*Dispatcher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if policyName == "" {
		policyName = "fifo/v0"
	}
	policy, err := scheduler.Lookup(policyName)
	if err != nil {
		return nil, err
	}
	return &Dispatcher{
		store: st, reg: reg, policy: policy, now: time.Now,
		logger: logger, inFlight: map[string]bool{},
	}, nil
}

// Run blocks until ctx is done: resume in-flight work, then cycle on job
// arrival (LISTEN/NOTIFY) or the 5s tick.
func (d *Dispatcher) Run(ctx context.Context) {
	d.resume(ctx)
	for ctx.Err() == nil {
		d.cycle(ctx)
		if err := d.store.WaitForJobNotify(ctx, cycleEvery); err != nil && ctx.Err() == nil {
			d.logger.Warn("job notify wait failed; falling back to tick", "error", err)
			select {
			case <-time.After(cycleEvery):
			case <-ctx.Done():
			}
		}
	}
	d.wg.Wait()
}

// resume re-attaches executors to tasks that were in flight when the previous
// process stopped. task_id is the idempotency key, so resubmission is safe.
func (d *Dispatcher) resume(ctx context.Context) {
	tasks, err := d.store.ActiveTasks(ctx)
	if err != nil {
		d.logger.Error("resume: listing active tasks", "error", err)
		return
	}
	for _, t := range tasks {
		rec, err := d.store.GetJob(ctx, t.JobID)
		if err != nil {
			d.logger.Error("resume: loading job", "job", t.JobID, "error", err)
			continue
		}
		d.logger.Info("resuming in-flight job", "job", t.JobID, "task", t.TaskID, "target", t.Target)
		d.startExecutor(ctx, rec, t.TaskID, t.Target)
	}
}

func (d *Dispatcher) cycle(ctx context.Context) {
	pending, err := d.store.PendingJobs(ctx, claimBatch)
	if err != nil {
		d.logger.Error("listing pending jobs", "error", err)
		return
	}
	for _, rec := range d.orderPending(ctx, pending) {
		d.dispatchOne(ctx, rec)
	}
}

func (d *Dispatcher) dispatchOne(ctx context.Context, rec *store.JobRecord) {
	jobView, err := scheduler.ParseJob(rec.JobID, rec.Tenant, rec.Doc)
	if err != nil {
		// Admission should make this impossible; surface it, don't loop hot.
		d.logger.Error("unparseable job document", "job", rec.JobID, "error", err)
		_, _ = d.store.SetJobCondition(ctx, rec.JobID, map[string]any{
			"type": "Schedulable", "status": "False", "reason": "UnparseableDocument",
			"message": err.Error(),
		})
		return
	}

	decision := scheduler.Schedule(d.policy, jobView, d.fleetViews(), d.now())
	if decision.Target == "" {
		// Infeasible now: the job stays PENDING with a condition explaining
		// which constraint failed for how many targets (spec §quantumjob).
		changed, err := d.store.SetJobCondition(ctx, rec.JobID, map[string]any{
			"type": "Schedulable", "status": "False", "reason": "NoFeasibleTarget",
			"message": decision.Reason,
		})
		if err != nil {
			d.logger.Error("recording infeasibility", "job", rec.JobID, "error", err)
		} else if changed {
			d.logger.Info("job not placeable", "job", rec.JobID, "reason", decision.Reason)
		}
		return
	}

	taskID := uuid.NewString()
	bound, err := d.store.BindJob(ctx, rec.JobID, taskID, decision.Target, decision.PlacementRecord())
	if err != nil {
		// Lost the race (another cycle, or a concurrent cancel): not an error.
		d.logger.Debug("bind skipped", "job", rec.JobID, "cause", err)
		return
	}
	d.logger.Info("job bound", "job", rec.JobID, "target", decision.Target,
		"policy", decision.Policy, "task", taskID)
	d.startExecutor(ctx, bound, taskID, decision.Target)
}

// fleetViews converts the registry cache into the scheduler's plain values.
func (d *Dispatcher) fleetViews() []*scheduler.TargetView {
	entries := d.reg.Entries()
	views := make([]*scheduler.TargetView, 0, len(entries))
	for _, e := range entries {
		views = append(views, entryToView(e))
	}
	return views
}

func entryToView(e *registry.Entry) *scheduler.TargetView {
	ext := e.Caps.GetVendorExtensions()
	// RFC-0001: the first-class fields are authoritative when present; the
	// vendor_extensions fallback is legal only until spec v0.3, then the
	// extension keys become reserved and the fallback must be deleted.
	technology := e.Info.GetTechnology()
	if technology == "" {
		technology = ext["technology"]
	}
	cloud := e.Caps.GetCloudQueue() || ext["cloud"] == "true"
	v := &scheduler.TargetView{
		Name:       e.Name,
		Modality:   e.Info.GetModality(),
		Technology: technology,
		Qubits:     e.Caps.GetNumQubits(),
		Formats:    e.Caps.GetProgramFormats(),
		MaxShots:   e.Caps.GetMaxShots(),
		Billing:    e.Caps.GetBillingUnits(),
		Cloud:      cloud,
	}
	if nominal, err := strconv.ParseFloat(ext["nominal-2q-error-median"], 64); err == nil {
		v.Nominal2QError = nominal
	}
	if state := e.State; state != nil {
		v.Online = state.GetStatus() == adapterv1alpha1.DeviceState_ONLINE
		v.QueueDepth = state.GetQueueDepth()
		v.WaitSeconds = state.GetEstimatedWait().AsDuration().Seconds()
		if cal := state.GetCalibration(); cal != nil {
			v.SnapshotID = cal.GetSnapshotId()
			if cal.GetMeasuredAt() != nil {
				v.MeasuredAt = cal.GetMeasuredAt().AsTime()
			}
			for _, m := range cal.GetMetrics() {
				v.Metrics = append(v.Metrics, scheduler.Metric{
					Name: m.GetName(), Value: m.GetValue(), Qubits: m.GetQubits(),
				})
			}
		}
		for _, w := range state.GetMaintenance() {
			v.Maintenance = append(v.Maintenance, scheduler.Window{
				Start: w.GetStart().AsTime(), End: w.GetEnd().AsTime(),
			})
		}
	}
	return v
}

func (d *Dispatcher) startExecutor(ctx context.Context, rec *store.JobRecord, taskID, targetName string) {
	d.mu.Lock()
	if d.inFlight[rec.JobID] {
		d.mu.Unlock()
		return
	}
	d.inFlight[rec.JobID] = true
	d.mu.Unlock()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer func() {
			d.mu.Lock()
			delete(d.inFlight, rec.JobID)
			d.mu.Unlock()
		}()
		d.execute(ctx, rec, taskID, targetName)
	}()
}

// execute submits the task (idempotently) and follows it to a terminal state.
func (d *Dispatcher) execute(ctx context.Context, rec *store.JobRecord, taskID, targetName string) {
	siteName, targetID, _ := strings.Cut(targetName, "/")
	client := d.reg.AdapterClient(siteName)
	if client == nil {
		d.failJob(ctx, rec, taskID, targetName, map[string]any{
			"category": "DEVICE_OFFLINE", "retriable": true,
			"vendorMessage": "adapter for site " + siteName + " is not configured",
		})
		return
	}

	payload, shots, errDetail := payloadFor(rec.Doc)
	if errDetail != nil {
		d.failJob(ctx, rec, taskID, targetName, errDetail)
		return
	}

	handle, err := client.SubmitTask(ctx, &adapterv1alpha1.SubmitTaskRequest{
		Target:         &adapterv1alpha1.TargetRef{TargetId: targetID},
		IdempotencyKey: taskID,
		Payload:        payload,
		Shots:          shots,
		TenantHint:     rec.Tenant,
	})
	if err != nil {
		if ctx.Err() != nil {
			return // shutdown; resume() picks this up next start
		}
		d.failJob(ctx, rec, taskID, targetName, map[string]any{
			"category": "VENDOR_ERROR", "retriable": true,
			"vendorMessage": "SubmitTask: " + err.Error(),
		})
		return
	}
	_ = d.store.UpdateTask(ctx, taskID, handle.GetTaskId(), "QUEUED", nil, nil)
	if _, err := d.transition(ctx, rec.JobID, job.Submitted, nil); err != nil {
		// Job likely cancelled concurrently: stop the adapter task.
		_, _ = client.CancelTask(ctx, taskRef(targetID, handle.GetTaskId()))
		return
	}

	d.follow(ctx, rec, taskID, targetName, targetID, handle.GetTaskId(), client)
}

// follow watches the adapter task until terminal, mirroring states onto the
// job. Stream failures fall back to reconnecting (adapters may restart).
func (d *Dispatcher) follow(ctx context.Context, rec *store.JobRecord, taskID, targetName,
	targetID, adapterTaskID string, client adapterv1alpha1.AdapterServiceClient) {

	running := false
	for ctx.Err() == nil {
		stream, err := client.WatchTask(ctx, taskRef(targetID, adapterTaskID))
		if err != nil {
			d.logger.Warn("watch task failed; retrying", "task", taskID, "error", err)
			select {
			case <-time.After(watchBackoff):
			case <-ctx.Done():
			}
			continue
		}
		for {
			status, err := stream.Recv()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				d.logger.Warn("watch stream broke; reconnecting", "task", taskID, "error", err)
				select {
				case <-time.After(watchBackoff):
				case <-ctx.Done():
				}
				break
			}
			done := d.applyTaskStatus(ctx, rec, taskID, targetName, targetID, adapterTaskID,
				client, status, &running)
			if done {
				return
			}
		}
	}
}

// applyTaskStatus mirrors one adapter TaskStatus onto the job; returns true
// when the task reached a terminal state.
func (d *Dispatcher) applyTaskStatus(ctx context.Context, rec *store.JobRecord, taskID,
	targetName, targetID, adapterTaskID string, client adapterv1alpha1.AdapterServiceClient,
	status *adapterv1alpha1.TaskStatus, running *bool) bool {

	switch status.GetState() {
	case adapterv1alpha1.TaskStatus_RUNNING:
		if !*running {
			*running = true
			_ = d.store.UpdateTask(ctx, taskID, adapterTaskID, "RUNNING", nil, nil)
			if _, err := d.transition(ctx, rec.JobID, job.Running, nil); err != nil {
				_, _ = client.CancelTask(ctx, taskRef(targetID, adapterTaskID))
				return true
			}
		}
		return false

	case adapterv1alpha1.TaskStatus_SUCCEEDED:
		result := resultToMap(status.GetResult())
		_ = d.store.UpdateTask(ctx, taskID, adapterTaskID, "SUCCEEDED", nil, result)
		usage := usageToMap(status.GetUsage())
		if err := d.store.RecordUsage(ctx, rec.JobID, taskID, rec.Tenant, targetName, usage); err != nil {
			d.logger.Error("recording usage", "job", rec.JobID, "error", err)
		}
		_, err := d.transition(ctx, rec.JobID, job.Succeeded, func(st map[string]any) map[string]any {
			st["tasks"] = []any{map[string]any{
				"id": taskID, "target": targetName, "state": "SUCCEEDED", "result": result,
			}}
			st["usage"] = usageToStatus(usage)
			return st
		})
		if err != nil {
			d.logger.Error("finishing job", "job", rec.JobID, "error", err)
		}
		d.logger.Info("job succeeded", "job", rec.JobID, "target", targetName)
		return true

	case adapterv1alpha1.TaskStatus_FAILED:
		errDetail := errorToMap(status.GetError())
		_ = d.store.UpdateTask(ctx, taskID, adapterTaskID, "FAILED", errDetail, nil)
		d.failJob(ctx, rec, taskID, targetName, errDetail)
		return true

	case adapterv1alpha1.TaskStatus_CANCELLED:
		_ = d.store.UpdateTask(ctx, taskID, adapterTaskID, "CANCELLED", nil, nil)
		_, err := d.transition(ctx, rec.JobID, job.Cancelled, func(st map[string]any) map[string]any {
			return appendCondition(st, "Cancelled", "True", "AdapterCancelled",
				"task cancelled at the adapter")
		})
		if err != nil {
			d.logger.Debug("job already terminal on adapter cancel", "job", rec.JobID)
		}
		return true

	default: // QUEUED / unspecified
		return false
	}
}

// CancelJob is called by the API layer: best-effort adapter cancel, then the
// authoritative job transition.
func (d *Dispatcher) CancelJob(ctx context.Context, jobID string) error {
	task, err := d.store.TaskForJob(ctx, jobID)
	if err == nil && task.AdapterTaskID != "" {
		siteName, targetID, _ := strings.Cut(task.Target, "/")
		if client := d.reg.AdapterClient(siteName); client != nil {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, cerr := client.CancelTask(cctx, taskRef(targetID, task.AdapterTaskID))
			cancel()
			if cerr != nil {
				d.logger.Warn("adapter cancel failed", "job", jobID, "error", cerr)
			}
		}
	}
	return nil
}

func (d *Dispatcher) failJob(ctx context.Context, rec *store.JobRecord, taskID, targetName string,
	errDetail map[string]any) {

	_ = d.store.UpdateTask(ctx, taskID, "", "FAILED", errDetail, nil)
	_, err := d.transition(ctx, rec.JobID, job.Failed, func(st map[string]any) map[string]any {
		st["tasks"] = []any{map[string]any{
			"id": taskID, "target": targetName, "state": "FAILED", "error": errDetail,
		}}
		category, _ := errDetail["category"].(string)
		message, _ := errDetail["vendorMessage"].(string)
		return appendCondition(st, "Failed", "True", category, message)
	})
	if err != nil {
		d.logger.Error("failing job", "job", rec.JobID, "error", err)
	}
	d.logger.Info("job failed", "job", rec.JobID, "category", errDetail["category"])
}

func (d *Dispatcher) transition(ctx context.Context, jobID string, to job.Phase,
	mutate func(map[string]any) map[string]any) (*store.JobRecord, error) {
	return d.store.TransitionJob(ctx, jobID, to, mutate)
}

// -- helpers ----------------------------------------------------------------

func taskRef(targetID, adapterTaskID string) *adapterv1alpha1.TaskRef {
	return &adapterv1alpha1.TaskRef{
		Target: &adapterv1alpha1.TargetRef{TargetId: targetID},
		TaskId: adapterTaskID,
	}
}

func docPath(doc map[string]any, path ...string) map[string]any {
	cur := doc
	for _, p := range path {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		cur = next
	}
	return cur
}

// payloadFor extracts the adapter payload from the job document.
func payloadFor(doc map[string]any) (*adapterv1alpha1.Payload, uint64, map[string]any) {
	workload := docPath(doc, "spec", "workload")
	kind, _ := workload["kind"].(string)
	payloadField := map[string]string{
		"gate-model": "gateModel", "analog-hamiltonian": "analogHamiltonian",
		"annealing": "annealing", "pulse": "pulse", "logical": "logical",
	}[kind]
	modality := docPath(workload, payloadField)
	program := docPath(modality, "program")
	format, _ := program["format"].(string)

	var shots uint64
	if s, ok := modality["shots"].(float64); ok {
		shots = uint64(s)
	}

	if uri, ok := program["source"].(string); ok && uri != "" {
		return nil, 0, map[string]any{
			"category": "INVALID_PROGRAM", "retriable": false,
			"vendorMessage": fmt.Sprintf(
				"program.source URIs are not resolvable in this deployment (got %q); inline the program", uri),
		}
	}
	inline, _ := program["inline"].(string)
	raw, err := base64.StdEncoding.DecodeString(inline)
	if err != nil {
		return nil, 0, map[string]any{
			"category": "INVALID_PROGRAM", "retriable": false,
			"vendorMessage": "program.inline is not valid base64: " + err.Error(),
		}
	}
	return &adapterv1alpha1.Payload{
		Format: format,
		Body:   &adapterv1alpha1.Payload_Inline{Inline: raw},
	}, shots, nil
}

func errorToMap(detail *adapterv1alpha1.ErrorDetail) map[string]any {
	if detail == nil {
		return map[string]any{"category": "VENDOR_ERROR", "retriable": false,
			"vendorMessage": "adapter reported failure without ErrorDetail"}
	}
	return map[string]any{
		"category":      detail.GetCategory().String(),
		"retriable":     detail.GetRetriable(),
		"vendorCode":    detail.GetVendorCode(),
		"vendorMessage": detail.GetVendorMessage(),
	}
}

func resultToMap(result *adapterv1alpha1.Result) map[string]any {
	if result == nil {
		return nil
	}
	out := map[string]any{"format": result.GetFormat()}
	if inline := result.GetInline(); len(inline) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(inline, &decoded); err == nil {
			out["data"] = decoded
		} else {
			out["inlineBase64"] = base64.StdEncoding.EncodeToString(inline)
		}
	}
	if uri := result.GetUri(); uri != "" {
		out["uri"] = uri
	}
	return out
}

func usageToMap(records []*adapterv1alpha1.UsageRecord) map[string]float64 {
	out := map[string]float64{}
	for _, u := range records {
		out[u.GetUnit()] += u.GetAmount()
	}
	return out
}

func usageToStatus(usage map[string]float64) []any {
	units := make([]string, 0, len(usage))
	for unit := range usage {
		units = append(units, unit)
	}
	sort.Strings(units) // stable order keeps job status documents deterministic
	out := make([]any, 0, len(usage))
	for _, unit := range units {
		out = append(out, map[string]any{"unit": unit, "amount": usage[unit]})
	}
	return out
}

func appendCondition(st map[string]any, condType, status, reason, message string) map[string]any {
	if st == nil {
		st = map[string]any{}
	}
	conditions, _ := st["conditions"].([]any)
	st["conditions"] = append(conditions, map[string]any{
		"type": condType, "status": status, "reason": reason, "message": message,
	})
	return st
}

