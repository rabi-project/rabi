// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tangle.dev/tangle/internal/job"
)

// ErrNotFound is returned when a job id does not exist.
var ErrNotFound = errors.New("job not found")

// JobRecord is a stored QuantumJob with control-plane bookkeeping.
type JobRecord struct {
	JobID     string
	Tenant    string
	Name      string
	Phase     job.Phase
	Doc       map[string]any
	Status    map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobEvent is one entry of a job's transition history.
type JobEvent struct {
	Seq       int64
	JobID     string
	Phase     job.Phase
	Status    map[string]any
	CreatedAt time.Time
}

// InsertJob persists a newly admitted job (phase PENDING) and its first event
// atomically.
func (s *Store) InsertJob(ctx context.Context, rec *JobRecord) error {
	doc, err := json.Marshal(rec.Doc)
	if err != nil {
		return fmt.Errorf("store: marshal job doc: %w", err)
	}
	status, err := json.Marshal(rec.Status)
	if err != nil {
		return fmt.Errorf("store: marshal job status: %w", err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin insert job: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		INSERT INTO jobs (job_id, tenant, name, phase, doc, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		rec.JobID, rec.Tenant, rec.Name, string(rec.Phase), doc, status)
	if err := row.Scan(&rec.CreatedAt, &rec.UpdatedAt); err != nil {
		return fmt.Errorf("store: insert job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO job_events (job_id, phase, status) VALUES ($1, $2, $3)`,
		rec.JobID, string(rec.Phase), status); err != nil {
		return fmt.Errorf("store: insert job event: %w", err)
	}
	// Delivered on commit: wakes the dispatcher without polling latency.
	if _, err := tx.Exec(ctx, "SELECT pg_notify($1, $2)", jobsChannel, rec.JobID); err != nil {
		return fmt.Errorf("store: notify: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit insert job: %w", err)
	}
	return nil
}

// GetJob fetches one job by id.
func (s *Store) GetJob(ctx context.Context, jobID string) (*JobRecord, error) {
	return scanJob(s.Pool.QueryRow(ctx, `
		SELECT job_id, tenant, name, phase, doc, status, created_at, updated_at
		FROM jobs WHERE job_id = $1`, jobID))
}

// ListJobs returns jobs newest-first, filtered by tenant and/or phase, with
// offset pagination.
func (s *Store) ListJobs(ctx context.Context, tenant, phase string, limit, offset int) ([]*JobRecord, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT job_id, tenant, name, phase, doc, status, created_at, updated_at
		FROM jobs
		WHERE ($1 = '' OR tenant = $1) AND ($2 = '' OR phase = $2)
		ORDER BY created_at DESC, job_id
		LIMIT $3 OFFSET $4`, tenant, phase, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
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

// TransitionJob moves a job to phase `to` and rewrites its status document,
// enforcing the lifecycle state machine inside a row lock. mutate receives
// the current status and returns the new one; the phase field is set by this
// function. Every phase change in the control plane goes through here.
func (s *Store) TransitionJob(ctx context.Context, jobID string, to job.Phase,
	mutate func(status map[string]any) map[string]any) (*JobRecord, error) {

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rec, err := scanJob(tx.QueryRow(ctx, `
		SELECT job_id, tenant, name, phase, doc, status, created_at, updated_at
		FROM jobs WHERE job_id = $1 FOR UPDATE`, jobID))
	if err != nil {
		return nil, err
	}
	if err := job.Transition(rec.Phase, to); err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	status := rec.Status
	if mutate != nil {
		status = mutate(status)
	}
	if status == nil {
		status = map[string]any{}
	}
	status["phase"] = string(to)
	rawStatus, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("store: marshal status: %w", err)
	}

	row := tx.QueryRow(ctx, `
		UPDATE jobs SET phase = $2, status = $3, updated_at = now()
		WHERE job_id = $1 RETURNING updated_at`, jobID, string(to), rawStatus)
	if err := row.Scan(&rec.UpdatedAt); err != nil {
		return nil, fmt.Errorf("store: update job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO job_events (job_id, phase, status) VALUES ($1, $2, $3)`,
		jobID, string(to), rawStatus); err != nil {
		return nil, fmt.Errorf("store: insert transition event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: commit transition: %w", err)
	}
	rec.Phase = to
	rec.Status = status
	return rec, nil
}

// JobEventsSince returns a job's events with seq > after, in seq order.
func (s *Store) JobEventsSince(ctx context.Context, jobID string, after int64) ([]*JobEvent, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT seq, job_id, phase, status, created_at
		FROM job_events WHERE job_id = $1 AND seq > $2 ORDER BY seq`, jobID, after)
	if err != nil {
		return nil, fmt.Errorf("store: job events: %w", err)
	}
	defer rows.Close()

	var out []*JobEvent
	for rows.Next() {
		ev := &JobEvent{}
		var phase string
		var rawStatus []byte
		if err := rows.Scan(&ev.Seq, &ev.JobID, &phase, &rawStatus, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan job event: %w", err)
		}
		ev.Phase = job.Phase(phase)
		if err := json.Unmarshal(rawStatus, &ev.Status); err != nil {
			return nil, fmt.Errorf("store: decode event status: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// CountRows is a test helper surface: it reports table row counts so suites
// can assert e.g. that dry_run writes nothing.
func (s *Store) CountRows(ctx context.Context, table string) (int64, error) {
	var allowed = map[string]bool{"jobs": true, "job_events": true}
	if !allowed[table] {
		return 0, fmt.Errorf("store: refusing to count unknown table %q", table)
	}
	var n int64
	err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n)
	return n, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (*JobRecord, error) {
	rec := &JobRecord{}
	var phase string
	var rawDoc, rawStatus []byte
	err := row.Scan(&rec.JobID, &rec.Tenant, &rec.Name, &phase, &rawDoc, &rawStatus,
		&rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan job: %w", err)
	}
	rec.Phase = job.Phase(phase)
	if err := json.Unmarshal(rawDoc, &rec.Doc); err != nil {
		return nil, fmt.Errorf("store: decode job doc: %w", err)
	}
	if err := json.Unmarshal(rawStatus, &rec.Status); err != nil {
		return nil, fmt.Errorf("store: decode job status: %w", err)
	}
	return rec, nil
}
