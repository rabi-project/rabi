// SPDX-License-Identifier: Apache-2.0

// M6 acceptance: session objects, scheduler affinity, SESSION_LOST on
// expiry, and session accounting staying inside the session's project.
package dispatch_test

import (
	"testing"
	"time"

	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

func sessionFleet(t *testing.T) {
	t.Helper()
	newFleet(t,
		&adaptertest.TargetSpec{ID: "aaa", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 100000},
		&adaptertest.TargetSpec{ID: "zzz", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 100000},
	)
}

func sessionSpec(join string, maxDuration string) map[string]any {
	spec := gateModelSpec(2, 100)
	sess := map[string]any{}
	if join != "" {
		sess["join"] = join
	}
	if maxDuration != "" {
		sess["maxDuration"] = maxDuration
	}
	spec["session"] = sess
	return spec
}

func boundTarget(rec *store.JobRecord) string {
	s, _ := rec.Status["boundTarget"].(string)
	return s
}

// An opener gets a session (status.sessionId + sessions row on its bound
// target); a seeded iterative loop of joiners lands on the session target
// 100% — even though the fifo policy would otherwise prefer the
// lexicographically first target.
func TestSessionOpenerAndAffinity(t *testing.T) {
	sessionFleet(t)
	ctx := t.Context()

	opener := insertJob(t, "00000000-0000-4000-9000-00000000d001", "sess/loop", sessionSpec("", "30m"))
	got := awaitPhase(t, opener.JobID, job.Succeeded, 30*time.Second)
	sessionID, _ := got.Status["sessionId"].(string)
	if sessionID == "" {
		t.Fatal("opener did not record status.sessionId")
	}
	sess, err := testStore.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Target != boundTarget(got) || sess.Tenant != "sess/loop" {
		t.Fatalf("session record wrong: %+v vs bound %q", sess, boundTarget(got))
	}
	if sess.ExpiresAt == nil || !sess.Live(time.Now()) {
		t.Fatalf("session must be live with an expiry: %+v", sess)
	}

	// Pin the affinity proof to the non-default target: a session on
	// sim/zzz must pull every joiner there although fifo prefers sim/aaa.
	forced := &store.SessionRecord{
		SessionID: "forced-affinity", Tenant: "sess/loop", Target: "sim/zzz",
		AdapterSessionID: "fake-sess-forced", OpenedByJob: opener.JobID,
	}
	if err := testStore.InsertSession(ctx, forced); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"00000000-0000-4000-9000-00000000d002",
		"00000000-0000-4000-9000-00000000d003",
		"00000000-0000-4000-9000-00000000d004",
	}
	for _, id := range ids {
		insertJob(t, id, "sess/loop", sessionSpec("forced-affinity", ""))
	}
	for _, id := range ids {
		rec := awaitPhase(t, id, job.Succeeded, 30*time.Second)
		if bt := boundTarget(rec); bt != "sim/zzz" {
			t.Fatalf("joiner %s bound %q, want the session target sim/zzz (100%% affinity)", id, bt)
		}
	}
}

// A joiner whose session is gone gets SESSION_LOST — explicitly failed,
// never silently rescheduled onto another target.
func TestSessionExpiryProducesSessionLost(t *testing.T) {
	sessionFleet(t)
	ctx := t.Context()

	closed := time.Now().Add(-time.Minute)
	dead := &store.SessionRecord{
		SessionID: "dead-session", Tenant: "sess/exp", Target: "sim/aaa",
		AdapterSessionID: "fake-sess-dead", OpenedByJob: "00000000-0000-4000-9000-00000000d001",
		ClosedAt: &closed,
	}
	if err := testStore.InsertSession(ctx, dead); err != nil {
		t.Fatal(err)
	}
	rec := insertJob(t, "00000000-0000-4000-9000-00000000d005", "sess/exp", sessionSpec("dead-session", ""))
	got := awaitPhase(t, rec.JobID, job.Failed, 30*time.Second)
	errDetail, _ := got.Status["error"].(map[string]any)
	if errDetail["category"] != "SESSION_LOST" {
		t.Fatalf("want SESSION_LOST, got %+v", errDetail)
	}
	if bt := boundTarget(got); bt != "" {
		t.Fatalf("lost-session job was silently placed on %q", bt)
	}

	// Unknown session id: same explicit loss.
	rec2 := insertJob(t, "00000000-0000-4000-9000-00000000d006", "sess/exp", sessionSpec("never-existed", ""))
	got2 := awaitPhase(t, rec2.JobID, job.Failed, 30*time.Second)
	if errDetail, _ := got2.Status["error"].(map[string]any); errDetail["category"] != "SESSION_LOST" {
		t.Fatalf("unknown session: want SESSION_LOST, got %+v", errDetail)
	}
}

// Session accounting: a job from another project may not ride the session,
// so usage can only ever attribute to the session's own project.
func TestSessionCrossTenantRejected(t *testing.T) {
	sessionFleet(t)
	ctx := t.Context()
	sess := &store.SessionRecord{
		SessionID: "team-a-session", Tenant: "team/a", Target: "sim/aaa",
		AdapterSessionID: "fake-sess-a", OpenedByJob: "00000000-0000-4000-9000-00000000d001",
	}
	if err := testStore.InsertSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	rec := insertJob(t, "00000000-0000-4000-9000-00000000d007", "team/b", sessionSpec("team-a-session", ""))
	got := awaitPhase(t, rec.JobID, job.Failed, 30*time.Second)
	if errDetail, _ := got.Status["error"].(map[string]any); errDetail["category"] != "SESSION_LOST" {
		t.Fatalf("cross-tenant join: want SESSION_LOST, got %+v", errDetail)
	}
	// The team/a ledger saw nothing from team/b.
	entries, err := testStore.LedgerEntries(ctx, "team/a")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.JobID == rec.JobID {
			t.Fatal("cross-tenant job billed to the session's project")
		}
	}
}
