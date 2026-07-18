// SPDX-License-Identifier: Apache-2.0

// Package accounting turns the immutable native-unit ledger into cost
// records under a site-configurable, versioned normalization policy
// (phase1-build-plan.md M3). Normalization is a pure function of
// (ledger, policy): cost records are never stored, so replaying the same
// ledger under the same policy version is byte-equal by construction
// (docs/decisions.md D-037).
package accounting

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"

	"sigs.k8s.io/yaml"

	"github.com/rabi-project/rabi/internal/store"
)

// Policy is the site's normalization document.
type Policy struct {
	// Version stamps every cost record; sites bump it on any rate change.
	Version  string `json:"version"`
	Currency string `json:"currency"`
	// Rates are matched in order; first match wins.
	Rates []Rate `json:"rates"`
}

// Rate prices one native unit, optionally scoped to targets by glob.
type Rate struct {
	Unit   string  `json:"unit"`
	Target string  `json:"target,omitempty"` // path.Match glob; empty = any
	Rate   float64 `json:"rate"`
}

// ParsePolicy loads and validates a policy document.
func ParsePolicy(raw []byte) (*Policy, error) {
	var p Policy
	if err := yaml.UnmarshalStrict(raw, &p); err != nil {
		return nil, fmt.Errorf("normalization policy: %w", err)
	}
	if p.Version == "" || p.Currency == "" {
		return nil, fmt.Errorf("normalization policy: version and currency are required")
	}
	for _, r := range p.Rates {
		if r.Unit == "" || r.Rate < 0 {
			return nil, fmt.Errorf("normalization policy: rates need a unit and a non-negative rate")
		}
		if r.Target != "" {
			if _, err := path.Match(r.Target, "probe"); err != nil {
				return nil, fmt.Errorf("normalization policy: bad target glob %q: %w", r.Target, err)
			}
		}
	}
	return &p, nil
}

// CostRecord is one priced ledger row.
type CostRecord struct {
	LedgerID      int64
	JobID         string
	Tenant        string
	Target        string
	Unit          string
	Amount        float64
	Rate          float64
	Cost          float64
	Currency      string
	PolicyVersion string
}

// rateFor returns the first matching rate; unpriced units cost 0 with rate 0
// (visible in the export rather than silently dropped).
func (p *Policy) rateFor(unit, target string) float64 {
	for _, r := range p.Rates {
		if r.Unit != unit {
			continue
		}
		if r.Target == "" {
			return r.Rate
		}
		if ok, _ := path.Match(r.Target, target); ok {
			return r.Rate
		}
	}
	return 0
}

// Normalize prices ledger entries in append order.
func Normalize(entries []store.LedgerEntry, p *Policy) []CostRecord {
	out := make([]CostRecord, 0, len(entries))
	for _, e := range entries {
		rate := p.rateFor(e.Unit, e.Target)
		out = append(out, CostRecord{
			LedgerID: e.ID, JobID: e.JobID, Tenant: e.Tenant, Target: e.Target,
			Unit: e.Unit, Amount: e.Amount, Rate: rate, Cost: e.Amount * rate,
			Currency: p.Currency, PolicyVersion: p.Version,
		})
	}
	// Ledger order is already deterministic; sort defensively by ledger id
	// so the CSV never depends on caller ordering.
	sort.Slice(out, func(i, j int) bool { return out[i].LedgerID < out[j].LedgerID })
	return out
}

// WriteCSV emits the canonical export: fixed header, ledger order,
// shortest-round-trip floats. Byte-equal across replays.
func WriteCSV(w io.Writer, records []CostRecord) error {
	if _, err := io.WriteString(w,
		"ledger_id,job_id,tenant,target,unit,amount,rate,cost,currency,policy_version\n"); err != nil {
		return err
	}
	for _, r := range records {
		line := fmt.Sprintf("%d,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
			r.LedgerID, r.JobID, r.Tenant, r.Target, r.Unit,
			f(r.Amount), f(r.Rate), f(r.Cost), r.Currency, r.PolicyVersion)
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	return nil
}

func f(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
