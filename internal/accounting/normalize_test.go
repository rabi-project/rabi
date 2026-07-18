// SPDX-License-Identifier: Apache-2.0

package accounting

import (
	"bytes"
	"testing"

	"github.com/rabi-project/rabi/internal/store"
)

const policyYAML = `
version: 2026-07-a
currency: EUR
rates:
  - unit: shots
    target: "ibm/*"
    rate: 0.002
  - unit: shots
    rate: 0.001
  - unit: seconds
    rate: 0.5
`

func ledgerFixture() []store.LedgerEntry {
	return []store.LedgerEntry{
		{ID: 1, JobID: "j1", Tenant: "acme/qa", Target: "ibm/torino", Unit: "shots", Amount: 1000},
		{ID: 2, JobID: "j1", Tenant: "acme/qa", Target: "ibm/torino", Unit: "seconds", Amount: 3.25},
		{ID: 3, JobID: "j2", Tenant: "acme/qa", Target: "sim/aer-1", Unit: "shots", Amount: 4096},
		{ID: 4, JobID: "j3", Tenant: "acme/qa", Target: "sim/aer-1", Unit: "qpu-hours", Amount: 0.5},
	}
}

func TestNormalizeRatesAndReplay(t *testing.T) {
	p, err := ParsePolicy([]byte(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	recs := Normalize(ledgerFixture(), p)
	if len(recs) != 4 {
		t.Fatalf("records = %d", len(recs))
	}
	// Target-scoped rate beats the generic one; unpriced units surface at 0.
	if recs[0].Cost != 2.0 || recs[0].Rate != 0.002 {
		t.Fatalf("ibm shots: %+v", recs[0])
	}
	if recs[2].Cost != 4.096 || recs[2].Rate != 0.001 {
		t.Fatalf("sim shots: %+v", recs[2])
	}
	if recs[1].Cost != 1.625 {
		t.Fatalf("seconds: %+v", recs[1])
	}
	if recs[3].Rate != 0 || recs[3].Cost != 0 {
		t.Fatalf("unpriced unit must be visible at rate 0: %+v", recs[3])
	}
	for _, r := range recs {
		if r.PolicyVersion != "2026-07-a" || r.Currency != "EUR" {
			t.Fatalf("stamping missing: %+v", r)
		}
	}

	// §3: same ledger + same policy version → byte-equal CSV.
	var a, b bytes.Buffer
	if err := WriteCSV(&a, recs); err != nil {
		t.Fatal(err)
	}
	if err := WriteCSV(&b, Normalize(ledgerFixture(), p)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("replay is not byte-equal")
	}
	want := "ledger_id,job_id,tenant,target,unit,amount,rate,cost,currency,policy_version\n" +
		"1,j1,acme/qa,ibm/torino,shots,1000,0.002,2,EUR,2026-07-a\n"
	if got := a.String()[:len(want)]; got != want {
		t.Fatalf("canonical CSV drifted:\n%s", got)
	}
}

func TestParsePolicyRejectsBadDocs(t *testing.T) {
	for _, bad := range []string{
		"currency: EUR", // no version
		"version: v1",   // no currency
		"version: v1\ncurrency: EUR\nrates: [{rate: 1}]",                              // no unit
		"version: v1\ncurrency: EUR\nrates: [{unit: shots, rate: -1}]",                // negative
		"version: v1\ncurrency: EUR\nrates: [{unit: shots, rate: 1, target: '[bad'}]", // bad glob
		"version: v1\ncurrency: EUR\nbogus: field",                                    // unknown field
	} {
		if _, err := ParsePolicy([]byte(bad)); err == nil {
			t.Errorf("accepted bad policy: %q", bad)
		}
	}
}
