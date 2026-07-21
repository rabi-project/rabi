// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"math/rand"
	"testing"
)

// TestPolicyConformance holds EVERY registered policy — including newly-absorbed
// ones (P2.M6) — to the properties a placement policy must satisfy: it only ever
// selects a feasible target, it is deterministic for a fresh instance, and
// removing its choice reroutes rather than re-selecting the removed target.
// Iterating RegisteredPolicies means a new policy is conformance-tested the
// moment it is registered.
func TestPolicyConformance(t *testing.T) {
	for _, name := range RegisteredPolicies() {
		name := name
		t.Run(name, func(t *testing.T) {
			r := rand.New(rand.NewSource(71))
			for i := 0; i < propCases/4; i++ {
				j := genJob(r)
				fleet := genFleet(r)

				p1, err := NewPolicy(name)
				if err != nil {
					t.Fatal(err)
				}
				d := Schedule(p1, j, fleet, now)

				// 1. A chosen target must be feasible under the policy's OWN
				//    filter (the calibration-blind baselines legitimately use a
				//    narrower filter than the full spec one, D-024).
				if d.Target != "" {
					tv := findByName(fleet, d.Target)
					if tv == nil {
						t.Fatalf("case %d: chose target %q not in fleet", i, d.Target)
					}
					if reason := p1.Filter(j, tv, now); reason != "" {
						t.Fatalf("case %d: chose target %q its own filter rejects: %s", i, d.Target, reason)
					}
				}

				// 2. Determinism: a fresh instance on identical inputs decides
				//    identically (stateful policies reset per fresh instance).
				p2, _ := NewPolicy(name)
				if d2 := Schedule(p2, j, fleet, now); d2.Target != d.Target {
					t.Fatalf("case %d: nondeterministic decision %q vs %q", i, d.Target, d2.Target)
				}

				// 3. Reroute: removing the choice never re-selects it.
				if d.Target != "" {
					without := removeTarget(fleet, d.Target)
					p3, _ := NewPolicy(name)
					if d3 := Schedule(p3, j, without, now); d3.Target == d.Target {
						t.Fatalf("case %d: removed target %q still selected", i, d.Target)
					}
				}
			}
		})
	}
}

// TestAbsorbedPoliciesRegistered pins the P2.M6 additions so a rename or a
// missing Register is caught.
func TestAbsorbedPoliciesRegistered(t *testing.T) {
	for _, name := range []string{"pareto/v0", "adaptive-deferral/v0"} {
		if _, err := NewPolicy(name); err != nil {
			t.Errorf("absorbed policy %q not registered: %v", name, err)
		}
	}
}

func findByName(fleet []*TargetView, name string) *TargetView {
	for _, t := range fleet {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func removeTarget(fleet []*TargetView, name string) []*TargetView {
	var out []*TargetView
	for _, t := range fleet {
		if t.Name != name {
			out = append(out, t)
		}
	}
	return out
}
