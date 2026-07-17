// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

// T3.golden — placement decisions are regression-locked: given fleet state X
// and job Y, the decision (target, snapshot, score, full reason text) must
// match the committed golden byte-for-byte. Changing a golden requires the
// `golden-change` PR label (CI-enforced) and a per-scenario justification.
var updateGoldens = flag.Bool("update-goldens", false, "rewrite golden decision files")

type goldenScenario struct {
	Policy string `json:"policy"`
	Now    string `json:"now"`
	Job    struct {
		ID     string         `json:"id"`
		Tenant string         `json:"tenant"`
		Doc    map[string]any `json:"doc"`
	} `json:"job"`
	Fleet []*TargetView `json:"fleet"`
}

func TestGoldenPlacements(t *testing.T) {
	files, err := filepath.Glob("testdata/golden/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("T3.golden requires >= 20 scenarios, found %d", len(files))
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".yaml")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var sc goldenScenario
			if err := yaml.Unmarshal(raw, &sc); err != nil {
				t.Fatalf("parsing %s: %v", file, err)
			}
			now, err := time.Parse(time.RFC3339, sc.Now)
			if err != nil {
				t.Fatalf("bad now: %v", err)
			}
			policy, err := Lookup(sc.Policy)
			if err != nil {
				t.Fatal(err)
			}
			job, err := ParseJob(sc.Job.ID, sc.Job.Tenant, sc.Job.Doc)
			if err != nil {
				t.Fatal(err)
			}

			decision := Schedule(policy, job, sc.Fleet, now)
			got, err := json.MarshalIndent(decision, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')

			goldenPath := strings.TrimSuffix(file, ".yaml") + ".golden.json"
			if *updateGoldens {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run with -update-goldens): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("decision differs from golden %s\n--- got ---\n%s\n--- want ---\n%s",
					goldenPath, got, want)
			}
		})
	}
}
