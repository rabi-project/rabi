// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rabi-project/rabi/internal/job"
)

// jobsChannel is the LISTEN/NOTIFY channel that wakes the dispatcher when a
// job lands (mvp-build-plan.md §2: Postgres work queue + NOTIFY wakeups).
const jobsChannel = "rabi_jobs"

// TaskRecord is one adapter submission owned by a job.
type TaskRecord struct {
	TaskID        string
	JobID         string
	Target        string
	AdapterTaskID string
	State         string
	Error         map[string]any
	Result        map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BindJob atomically claims a PENDING job (FOR UPDATE SKIP LOCKED), moves it
// to SCHEDULED with the placement audit record, and creates its task row.
// Returns ErrNotFound when the job is gone and errJobNotPending when another
// worker (or a cancel) got there first.
func (s *Store) BindJob(ctx context.Context, jobID, taskID, target string,
	placement map[string]any) (*JobRecord, error) {

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin bind: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rec, err := scanJob(tx.QueryRow(ctx, `
		SELECT job_id, tenant, name, phase, doc, status, created_at, updated_at
		FROM jobs WHERE job_id = $1 FOR UPDATE SKIP LOCKED`, jobID))
	if err != nil {
		return nil, err
	}
	if rec.Phase != job.Pending {
		return nil, fmt.Errorf("store: job %s is %s, not PENDING", jobID, rec.Phase)
	}
	if err := job.Transition(rec.Phase, job.Scheduled); err != nil {
		return nil, err
	}

	status := rec.Status
	if status == nil {
		status = map[string]any{}
	}
	status["phase"] = string(job.Scheduled)
	status["boundTarget"] = target
	status["placement"] = placement
	status["tasks"] = []any{map[string]any{
		"id": taskID, "target": target, "state": "QUEUED",
	}}
	rawStatus, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("store: marshal bound status: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE jobs SET phase = $2, status = $3, updated_at = now() WHERE job_id = $1`,
		jobID, string(job.Scheduled), rawStatus); err != nil {
		return nil, fmt.Errorf("store: bind update: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO job_events (job_id, phase, status) VALUES ($1, $2, $3)`,
		jobID, string(job.Scheduled), rawStatus); err != nil {
		return nil, fmt.Errorf("store: bind event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tasks (task_id, job_id, target, state) VALUES ($1, $2, $3, 'QUEUED')`,
		taskID, jobID, target); err != nil {
		return nil, fmt.Errorf("store: insert task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: commit bind: %w", err)
	}
	rec.Phase = job.Scheduled
	rec.Status = status
	return rec, nil
}

// PendingJobs lists PENDING jobs oldest-first for a scheduling cycle.
func (s *Store) PendingJobs(ctx context.Context, limit int) ([]*JobRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT job_id, tenant, name, phase, doc, status, created_at, updated_at
		FROM jobs WHERE phase = 'PENDING' ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: pending jobs: %w", err)
	}
	defer rows.Close()
	var out []*JobRecord
	for rows.Next() {
		rec, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ActiveTasks returns tasks whose jobs are in-flight (SCHEDULED/SUBMITTED/
// RUNNING) — used to re-attach watchers after a control-plane restart.
func (s *Store) ActiveTasks(ctx context.Context) ([]*TaskRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT t.task_id, t.job_id, t.target, COALESCE(t.adapter_task_id, ''), t.state,
		       t.error, t.result, t.created_at, t.updated_at
		FROM tasks t JOIN jobs j ON j.job_id = t.job_id
		WHERE j.phase IN ('SCHEDULED', 'SUBMITTED', 'RUNNING')`)
	if err != nil {
		return nil, fmt.Errorf("store: active tasks: %w", err)
	}
	defer rows.Close()
	var out []*TaskRecord
	for rows.Next() {
		rec, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// TaskForJob returns the newest task for a job, or ErrNotFound.
func (s *Store) TaskForJob(ctx context.Context, jobID string) (*TaskRecord, error) {
	rec, err := scanTask(s.Pool.QueryRow(ctx, `
		SELECT task_id, job_id, target, COALESCE(adapter_task_id, ''), state,
		       error, result, created_at, updated_at
		FROM tasks WHERE job_id = $1 ORDER BY created_at DESC LIMIT 1`, jobID))
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// UpdateTask persists adapter-side progress on a task.
func (s *Store) UpdateTask(ctx context.Context, taskID string, adapterTaskID, state string,
	errDetail, result map[string]any) error {

	rawErr, err := marshalNullable(errDetail)
	if err != nil {
		return fmt.Errorf("store: marshal task error: %w", err)
	}
	rawResult, err := marshalNullable(result)
	if err != nil {
		return fmt.Errorf("store: marshal task result: %w", err)
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE tasks SET adapter_task_id = COALESCE(NULLIF($2, ''), adapter_task_id),
		       state = $3, error = COALESCE($4, error), result = COALESCE($5, result),
		       updated_at = now()
		WHERE task_id = $1`, taskID, adapterTaskID, state, rawErr, rawResult)
	if err != nil {
		return fmt.Errorf("store: update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordUsage appends usage records idempotently (task+unit unique).
func (s *Store) RecordUsage(ctx context.Context, jobID, taskID, tenant, target string,
	usage map[string]float64) error {

	for unit, amount := range usage {
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO usage_ledger (job_id, task_id, tenant, target, unit, amount)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (task_id, unit) DO NOTHING`,
			jobID, taskID, tenant, target, unit, amount); err != nil {
			return fmt.Errorf("store: record usage: %w", err)
		}
	}
	return nil
}

// UsageTotal is one aggregated ledger row.
type UsageTotal struct {
	Target string
	Unit   string
	Amount float64
}

// TenantUsage aggregates native-unit usage per target for a tenant.
func (s *Store) TenantUsage(ctx context.Context, tenant string, from, to time.Time) ([]UsageTotal, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT target, unit, SUM(amount) FROM usage_ledger
		WHERE tenant = $1
		  AND ($2::timestamptz IS NULL OR recorded_at >= $2)
		  AND ($3::timestamptz IS NULL OR recorded_at < $3)
		GROUP BY target, unit ORDER BY target, unit`,
		tenant, nullableTime(from), nullableTime(to))
	if err != nil {
		return nil, fmt.Errorf("store: tenant usage: %w", err)
	}
	defer rows.Close()
	var out []UsageTotal
	for rows.Next() {
		var u UsageTotal
		if err := rows.Scan(&u.Target, &u.Unit, &u.Amount); err != nil {
			return nil, fmt.Errorf("store: scan usage: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UsageCountForTask reports ledger rows for one task (test surface for T2).
func (s *Store) UsageCountForTask(ctx context.Context, taskID string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_ledger WHERE task_id = $1`, taskID).Scan(&n)
	return n, err
}

// NotifyJobs pings the dispatcher wake-up channel.
func (s *Store) NotifyJobs(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, "SELECT pg_notify($1, '')", jobsChannel)
	return err
}

// WaitForJobNotify blocks on a dedicated connection until a notification
// arrives or the timeout elapses. Both outcomes return nil; real errors
// (context cancelled, connection lost) propagate.
func (s *Store) WaitForJobNotify(ctx context.Context, timeout time.Duration) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+jobsChannel); err != nil {
		return err
	}
	defer func() {
		// Best effort: the connection returns to the pool without the LISTEN.
		_, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN "+jobsChannel)
	}()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err = conn.Conn().WaitForNotification(waitCtx)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
		return nil
	}
	return err
}

func scanTask(row rowScanner) (*TaskRecord, error) {
	rec := &TaskRecord{}
	var rawErr, rawResult []byte
	err := row.Scan(&rec.TaskID, &rec.JobID, &rec.Target, &rec.AdapterTaskID, &rec.State,
		&rawErr, &rawResult, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan task: %w", err)
	}
	if len(rawErr) > 0 {
		if err := json.Unmarshal(rawErr, &rec.Error); err != nil {
			return nil, fmt.Errorf("store: decode task error: %w", err)
		}
	}
	if len(rawResult) > 0 {
		if err := json.Unmarshal(rawResult, &rec.Result); err != nil {
			return nil, fmt.Errorf("store: decode task result: %w", err)
		}
	}
	return rec, nil
}

func marshalNullable(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
