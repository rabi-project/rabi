// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/durationpb"
	"strings"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/scheduler"
	"github.com/rabi-project/rabi/internal/store"
)

// Sessions (M6): a session binds successive tasks to one target.
//   - An opener job (session.maxDuration set, no join) opens an adapter
//     session on its bound target after placement; the control-plane
//     session id lands in status.sessionId for later jobs to join.
//   - A joiner (session.join set) is pinned to the session's target
//     (scheduler affinity via requireTargets) and its task carries the
//     adapter session id.
//   - A joiner whose session is closed or expired gets SESSION_LOST —
//     explicitly failed, never silently rescheduled (spec §overview).

// sessionAffinity resolves a joiner's session before scheduling. It returns
// (session, true) when the job may proceed — with the job pinned to the
// session's target — or (nil, false) after failing/parking the job itself.
func (d *Dispatcher) sessionAffinity(ctx context.Context, rec *store.JobRecord, j *scheduler.JobView) (*store.SessionRecord, bool) {
	if j.SessionJoin == "" {
		return nil, true
	}
	sess, err := d.store.GetSession(ctx, j.SessionJoin)
	if err != nil {
		d.failSessionLost(ctx, rec, fmt.Sprintf("session %q not found", j.SessionJoin))
		return nil, false
	}
	// Session accounting attributes usage to the session's project: a job
	// from another tenant may not ride it.
	if sess.Tenant != rec.Tenant {
		d.failSessionLost(ctx, rec, fmt.Sprintf("session %q belongs to project %q", sess.SessionID, sess.Tenant))
		return nil, false
	}
	if !sess.Live(d.now()) {
		d.failSessionLost(ctx, rec, fmt.Sprintf("session %q is closed or expired", sess.SessionID))
		return nil, false
	}
	// Affinity: the session target is the only feasible placement.
	j.RequireTargets = []string{sess.Target}
	return sess, true
}

// failSessionLost is the RFC-mandated explicit failure: SESSION_LOST, never
// a silent reschedule.
func (d *Dispatcher) failSessionLost(ctx context.Context, rec *store.JobRecord, msg string) {
	_, err := d.store.TransitionJob(ctx, rec.JobID, job.Failed, func(st map[string]any) map[string]any {
		conditions, _ := st["conditions"].([]any)
		st["conditions"] = append(conditions, map[string]any{
			"type": "SessionLost", "status": "True", "reason": "SessionUnavailable", "message": msg,
		})
		st["error"] = map[string]any{"category": "SESSION_LOST", "retriable": true, "message": msg}
		return st
	})
	if err != nil {
		d.logger.Error("session-lost transition", "job", rec.JobID, "error", err)
	} else {
		d.logger.Info("job failed with SESSION_LOST", "job", rec.JobID, "cause", msg)
	}
}

// openSessionIfRequested opens an adapter session for an opener job right
// after bind, records it, and stamps status.sessionId. Failure to open
// fails the job (the user asked for a session; silently running without
// one would break the affinity contract for followers).
func (d *Dispatcher) openSessionIfRequested(ctx context.Context, rec *store.JobRecord, taskID, targetName string) (adapterSessionID string, ok bool) {
	j, err := scheduler.ParseJob(rec.JobID, rec.Tenant, rec.Doc)
	if err != nil || j.SessionMaxDuration <= 0 || j.SessionJoin != "" {
		return "", true // not an opener
	}
	siteName, targetID, _ := strings.Cut(targetName, "/")
	client := d.reg.AdapterClient(siteName)
	if client == nil {
		d.failJob(ctx, rec, taskID, targetName, map[string]any{
			"category": "DEVICE_OFFLINE", "retriable": true,
			"vendorMessage": "adapter for site " + siteName + " is not configured",
		})
		return "", false
	}
	handle, err := client.OpenSession(ctx, &adapterv1alpha1.OpenSessionRequest{
		Target:      &adapterv1alpha1.TargetRef{TargetId: targetID},
		MaxDuration: durationpb.New(j.SessionMaxDuration),
		TenantHint:  rec.Tenant,
	})
	if err != nil {
		d.failJob(ctx, rec, taskID, targetName, map[string]any{
			"category": "VENDOR_ERROR", "retriable": true,
			"vendorMessage": "OpenSession: " + err.Error(),
		})
		return "", false
	}
	sess := &store.SessionRecord{
		SessionID:        uuid.NewString(),
		Tenant:           rec.Tenant,
		Target:           targetName,
		AdapterSessionID: handle.GetSessionId(),
		OpenedByJob:      rec.JobID,
	}
	if exp := handle.GetExpiresAt(); exp != nil {
		t := exp.AsTime()
		sess.ExpiresAt = &t
	}
	if err := d.store.InsertSession(ctx, sess); err != nil {
		d.logger.Error("recording session", "job", rec.JobID, "error", err)
		_, _ = client.CloseSession(ctx, handle)
		d.failJob(ctx, rec, taskID, targetName, map[string]any{
			"category": "VENDOR_ERROR", "retriable": true,
			"vendorMessage": "recording session: " + err.Error(),
		})
		return "", false
	}
	if _, err := d.store.SetJobCondition(ctx, rec.JobID, map[string]any{
		"type": "SessionOpened", "status": "True", "reason": "SessionOpened",
		"message": "session " + sess.SessionID + " on " + targetName,
	}); err != nil {
		d.logger.Error("recording session condition", "job", rec.JobID, "error", err)
	}
	d.stampSessionID(ctx, rec, sess.SessionID)
	d.logger.Info("session opened", "job", rec.JobID, "session", sess.SessionID,
		"adapterSession", handle.GetSessionId(), "target", targetName)
	return handle.GetSessionId(), true
}

// stampSessionID writes status.sessionId (additive, control-plane-written).
func (d *Dispatcher) stampSessionID(ctx context.Context, rec *store.JobRecord, sessionID string) {
	if rec.Status == nil {
		rec.Status = map[string]any{}
	}
	rec.Status["sessionId"] = sessionID
	if _, err := d.store.Pool.Exec(ctx, `
		UPDATE jobs SET status = jsonb_set(status, '{sessionId}', to_jsonb($2::text), true),
		               updated_at = now()
		WHERE job_id = $1`, rec.JobID, sessionID); err != nil {
		d.logger.Error("stamping sessionId", "job", rec.JobID, "error", err)
	}
}

// sessionForExecution returns the adapter session id a task must carry:
// the joined session's, or one freshly opened for an opener.
func (d *Dispatcher) sessionForExecution(ctx context.Context, rec *store.JobRecord, taskID, targetName string) (string, bool) {
	j, err := scheduler.ParseJob(rec.JobID, rec.Tenant, rec.Doc)
	if err != nil {
		return "", true
	}
	if j.SessionJoin != "" {
		sess, err := d.store.GetSession(ctx, j.SessionJoin)
		if err != nil || !sess.Live(d.now()) {
			// The session vanished between bind and submit: explicit loss.
			cause := "closed or expired"
			if err != nil {
				cause = "not found"
			}
			d.failSessionLostTask(ctx, rec, taskID, targetName,
				fmt.Sprintf("session %q %s before task submission", j.SessionJoin, cause))
			return "", false
		}
		return sess.AdapterSessionID, true
	}
	return d.openSessionIfRequested(ctx, rec, taskID, targetName)
}

// failSessionLostTask marks the bound task failed with SESSION_LOST.
func (d *Dispatcher) failSessionLostTask(ctx context.Context, rec *store.JobRecord, taskID, targetName, msg string) {
	d.failJob(ctx, rec, taskID, targetName, map[string]any{
		"category": "SESSION_LOST", "retriable": true, "message": msg,
	})
}

// sweepExpiredSessions closes control-plane session records whose expiry
// passed (best-effort bookkeeping; adapters own the hard expiry).
func (d *Dispatcher) sweepExpiredSessions(ctx context.Context) {
	_, err := d.store.Pool.Exec(ctx, `
		UPDATE sessions SET closed_at = now()
		WHERE closed_at IS NULL AND expires_at IS NOT NULL AND expires_at < now()`)
	if err != nil && ctx.Err() == nil {
		d.logger.Error("sweeping expired sessions", "error", err)
	}
}
