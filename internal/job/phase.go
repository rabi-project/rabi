// SPDX-License-Identifier: Apache-2.0

// Package job holds pure QuantumJob logic: the lifecycle state machine and
// admission validation. No I/O lives here.
package job

import "fmt"

// Phase is the job lifecycle phase (spec/spec/overview.md §3).
type Phase string

const (
	Pending   Phase = "PENDING"
	Scheduled Phase = "SCHEDULED"
	Submitted Phase = "SUBMITTED"
	Running   Phase = "RUNNING"
	Succeeded Phase = "SUCCEEDED"
	Failed    Phase = "FAILED"
	Cancelled Phase = "CANCELLED"
)

// Phases lists every phase, in lifecycle order.
var Phases = []Phase{Pending, Scheduled, Submitted, Running, Succeeded, Failed, Cancelled}

// transitions is the single source of truth for legal phase changes
// (spec/spec/overview.md §3). SCHEDULED/SUBMITTED may return to PENDING
// (policy-controlled reschedule before RUNNING); FAILED is reachable from
// SCHEDULED (bind-time payload/adapter failures), SUBMITTED, and RUNNING
// (see docs/decisions.md D-010), and from PENDING via RFC-0003
// onConflict=reject at the decision horizon (D-039); terminal states are
// immutable.
var transitions = map[Phase]map[Phase]bool{
	Pending:   {Scheduled: true, Failed: true, Cancelled: true},
	Scheduled: {Submitted: true, Pending: true, Failed: true, Cancelled: true},
	Submitted: {Running: true, Pending: true, Failed: true, Cancelled: true},
	Running:   {Succeeded: true, Failed: true, Cancelled: true},
	Succeeded: {},
	Failed:    {},
	Cancelled: {},
}

// Valid reports whether p is a known phase.
func (p Phase) Valid() bool {
	_, ok := transitions[p]
	return ok
}

// Terminal reports whether p is terminal (immutable).
func (p Phase) Terminal() bool {
	next, ok := transitions[p]
	return ok && len(next) == 0
}

// CanTransition reports whether from → to is a legal lifecycle transition.
func CanTransition(from, to Phase) bool {
	return transitions[from][to]
}

// Transition validates from → to, returning a precise error when illegal.
// Every phase change in the system goes through this function.
func Transition(from, to Phase) error {
	if !from.Valid() {
		return fmt.Errorf("unknown phase %q", from)
	}
	if !to.Valid() {
		return fmt.Errorf("unknown phase %q", to)
	}
	if from.Terminal() {
		return fmt.Errorf("job is %s, a terminal state: no further transitions", from)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("illegal transition %s → %s", from, to)
	}
	return nil
}
