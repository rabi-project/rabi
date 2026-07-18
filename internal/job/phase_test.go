// SPDX-License-Identifier: Apache-2.0

package job

import "testing"

// legal is the expected transition table, written out pairwise so the test is
// an independent statement of spec/spec/overview.md §3 rather than a mirror
// of the implementation map.
var legal = map[[2]Phase]bool{
	{Pending, Scheduled}:   true,
	{Pending, Cancelled}:   true,
	{Pending, Failed}:      true, // RFC-0003 onConflict=reject at the horizon
	{Scheduled, Submitted}: true,
	{Scheduled, Pending}:   true,
	{Scheduled, Failed}:    true,
	{Scheduled, Cancelled}: true,
	{Submitted, Running}:   true,
	{Submitted, Pending}:   true,
	{Submitted, Failed}:    true,
	{Submitted, Cancelled}: true,
	{Running, Succeeded}:   true,
	{Running, Failed}:      true,
	{Running, Cancelled}:   true,
}

// TestTransitionExhaustive enumerates every ordered phase pair (T1.fsm).
func TestTransitionExhaustive(t *testing.T) {
	for _, from := range Phases {
		for _, to := range Phases {
			err := Transition(from, to)
			if legal[[2]Phase{from, to}] {
				if err != nil {
					t.Errorf("expected %s → %s legal, got error: %v", from, to, err)
				}
			} else if err == nil {
				t.Errorf("expected %s → %s illegal, but it was allowed", from, to)
			}
		}
	}
}

// TestTerminalImmutable is the property: no event sequence moves a terminal job.
func TestTerminalImmutable(t *testing.T) {
	for _, from := range []Phase{Succeeded, Failed, Cancelled} {
		if !from.Terminal() {
			t.Errorf("%s must be terminal", from)
		}
		for _, to := range Phases {
			if err := Transition(from, to); err == nil {
				t.Errorf("terminal %s must reject transition to %s", from, to)
			}
		}
	}
	for _, p := range []Phase{Pending, Scheduled, Submitted, Running} {
		if p.Terminal() {
			t.Errorf("%s must not be terminal", p)
		}
	}
}

// TestUnknownPhases covers the remaining branches of Transition.
func TestUnknownPhases(t *testing.T) {
	if err := Transition(Phase("BOGUS"), Pending); err == nil {
		t.Error("unknown from-phase must be rejected")
	}
	if err := Transition(Pending, Phase("BOGUS")); err == nil {
		t.Error("unknown to-phase must be rejected")
	}
	if Phase("BOGUS").Valid() {
		t.Error("BOGUS must not be a valid phase")
	}
	if Phase("BOGUS").Terminal() {
		t.Error("unknown phase must not report terminal")
	}
}

// TestReachability sanity-checks the lifecycle shape: every phase is
// reachable from PENDING and every non-terminal phase can reach a terminal.
func TestReachability(t *testing.T) {
	reached := map[Phase]bool{Pending: true}
	for range Phases {
		for from := range reached {
			for _, to := range Phases {
				if CanTransition(from, to) {
					reached[to] = true
				}
			}
		}
	}
	for _, p := range Phases {
		if !reached[p] {
			t.Errorf("phase %s unreachable from PENDING", p)
		}
	}
}
