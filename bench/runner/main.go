// SPDX-License-Identifier: Apache-2.0

// The benchmark runner: a deterministic discrete-event simulation that
// executes the identical seeded workload under each scheduling policy over
// the identical replayed-calibration timeline, using the REAL policy code
// from internal/scheduler (mvp-build-plan.md §M6 — like with like inside the
// same machinery).
//
// Time is purely simulated (no wall clock, no goroutines), so runs are
// byte-identical for a given seed. Physics is not executed here: the runner
// emits every (circuit, target, snapshot, shots, seed) execution for the
// Python batch executor, which shares the same series file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tangle.dev/tangle/internal/scheduler"
)

// Workload shape (documented in the report's methodology):
// bursty Poisson arrivals over ~2 sim-days so the run spans calibration
// drift; execution time grows with shots; ~30% deadlines, ~40% quality
// floors per the build plan.
const (
	interArrivalBurstS = 20.0
	interArrivalCalmS  = 400.0
	burstProb          = 0.35
	execBaseS          = 20.0
	execPerShotS       = 1.0 / 150.0
	deadlineProb       = 0.30
	floorProb          = 0.40
	drainCapHours      = 12.0
	pendingRetryS      = 60.0 // re-试 unplaced jobs at least this often
)

var shotChoices = []uint64{500, 1000, 2000, 4000}

// ---- series file ----

type seriesFile struct {
	HorizonHours float64         `json:"horizon_hours"`
	Targets      []*seriesTarget `json:"targets"`
}

type seriesTarget struct {
	Name        string       `json:"name"`
	Qubits      uint32       `json:"qubits"`
	Formats     []string     `json:"formats"`
	Billing     []string     `json:"billing"`
	MaxShots    uint64       `json:"max_shots"`
	Technology  string       `json:"technology"`
	TwoQubit    string       `json:"two_qubit_gate"`
	Nominal2Q   float64      `json:"nominal_2q_error_median"`
	StepSeconds float64      `json:"step_seconds"`
	Snapshots   []*seriesSnap `json:"snapshots"`
}

type seriesSnap struct {
	SnapshotID string  `json:"snapshot_id"`
	SimOffsetS float64 `json:"sim_offset_s"`
	Metrics    []struct {
		Name   string   `json:"name"`
		Value  float64  `json:"value"`
		Qubits []uint32 `json:"qubits"`
	} `json:"metrics"`
}

// snapAt returns the snapshot in effect at sim offset t (last step <= t).
func (st *seriesTarget) snapAt(t float64) *seriesSnap {
	idx := sort.Search(len(st.Snapshots), func(i int) bool {
		return st.Snapshots[i].SimOffsetS > t
	}) - 1
	if idx < 0 {
		idx = 0
	}
	return st.Snapshots[idx]
}

func (st *seriesTarget) viewAt(t float64, waitS float64, depth uint32) *scheduler.TargetView {
	snap := st.snapAt(t)
	v := &scheduler.TargetView{
		Name:           st.Name,
		Modality:       "gate-model",
		Technology:     st.Technology,
		Qubits:         st.Qubits,
		Formats:        st.Formats,
		MaxShots:       st.MaxShots,
		Billing:        st.Billing,
		Online:         true,
		SnapshotID:     snap.SnapshotID,
		MeasuredAt:     simBase.Add(time.Duration(snap.SimOffsetS) * time.Second),
		WaitSeconds:    waitS,
		QueueDepth:     depth,
		Nominal2QError: st.Nominal2Q,
	}
	for _, m := range snap.Metrics {
		v.Metrics = append(v.Metrics, scheduler.Metric{Name: m.Name, Value: m.Value, Qubits: m.Qubits})
	}
	return v
}

// simBase anchors sim offsets to a fixed instant so calibrationMaxAge
// arithmetic works; its absolute value is irrelevant.
var simBase = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

// ---- workload ----

type circuitInfo struct {
	Name    string
	Width   int
	Profile scheduler.CircuitProfile
	Source  string
}

type benchJob struct {
	ID        string
	Circuit   *circuitInfo
	Shots     uint64
	ArrivalS  float64
	DeadlineS float64 // 0 = none
	Floor2Q   float64 // 0 = none
	FloorRO   float64 // 0 = none
}

func loadCircuits(dir string) ([]*circuitInfo, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.qasm"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []*circuitInfo
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		profile, err := scheduler.ProfileQASM(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".qasm")
		out = append(out, &circuitInfo{
			Name: name, Width: profile.Qubits, Profile: profile, Source: string(raw),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no circuits in %s", dir)
	}
	return out, nil
}

// generateWorkload is seeded and policy-independent: every policy sees the
// same jobs at the same times.
func generateWorkload(r *rand.Rand, circuits []*circuitInfo, n int) []*benchJob {
	jobs := make([]*benchJob, 0, n)
	now := 0.0
	for i := 0; i < n; i++ {
		if r.Float64() < burstProb {
			now += r.ExpFloat64() * interArrivalBurstS
		} else {
			now += r.ExpFloat64() * interArrivalCalmS
		}
		j := &benchJob{
			ID:       fmt.Sprintf("bj-%04d", i),
			Circuit:  circuits[r.Intn(len(circuits))],
			Shots:    shotChoices[r.Intn(len(shotChoices))],
			ArrivalS: now,
		}
		if r.Float64() < deadlineProb {
			// Slack: generous but violable under queueing.
			j.DeadlineS = now + 300 + r.Float64()*1800
		}
		if r.Float64() < floorProb {
			// Floor ranges sit against the replay devices' real error scales
			// (best 2q error 0.003-0.005 fresh, +30% under drift), so floors
			// are satisfiable on fresh calibration and bind as devices drift.
			if r.Float64() < 0.7 {
				j.Floor2Q = 0.0035 + r.Float64()*0.004 // [0.0035, 0.0075]
			}
			if r.Float64() < 0.5 {
				j.FloorRO = 0.007 + r.Float64()*0.013 // [0.007, 0.020]
			}
			if j.Floor2Q == 0 && j.FloorRO == 0 {
				j.Floor2Q = 0.0035 + r.Float64()*0.004
			}
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func (j *benchJob) view() *scheduler.JobView {
	profile := j.Circuit.Profile
	view := &scheduler.JobView{
		ID: j.ID, Tenant: "bench", Kind: "gate-model", Format: "openqasm3",
		Shots: j.Shots, Qubits: uint32(j.Circuit.Width),
		TwoQubitErrorMax: j.Floor2Q, ReadoutErrorMax: j.FloorRO,
		Profile: &profile,
	}
	if j.DeadlineS > 0 {
		view.Deadline = simBase.Add(time.Duration(j.DeadlineS) * time.Second)
	}
	return view
}

// ---- discrete-event simulation ----

type execution struct {
	JobID      string  `json:"job_id"`
	Circuit    string  `json:"circuit"`
	Width      int     `json:"width"`
	Shots      uint64  `json:"shots"`
	ArrivalS   float64 `json:"arrival_s"`
	StartS     float64 `json:"start_s"`
	EndS       float64 `json:"end_s"`
	WaitS      float64 `json:"wait_s"`
	Target     string  `json:"target"`
	SnapshotID string  `json:"snapshot_id"` // snapshot at execution start
	ESP        float64 `json:"esp_predicted"`
	Floor2Q    float64 `json:"floor_2q"`
	FloorRO    float64 `json:"floor_ro"`
	SLOViolate bool    `json:"slo_violated"`
	DeadlineS  float64 `json:"deadline_s"`
	DeadlineOK bool    `json:"deadline_met"`
	Unplaced   bool    `json:"unplaced"`
	PhysSeed   int64   `json:"phys_seed"`
}

func execDuration(shots uint64) float64 { return execBaseS + float64(shots)*execPerShotS }

// simulate runs one (policy, seed) DES over the shared workload.
func simulate(policyName string, seed int64, jobs []*benchJob, targets []*seriesTarget) ([]*execution, error) {
	policy, err := scheduler.NewPolicy(policyName)
	if err != nil {
		return nil, err
	}

	freeAt := make(map[string]float64, len(targets))
	depth := make(map[string]uint32, len(targets))
	for _, t := range targets {
		freeAt[t.Name] = 0
	}

	horizonS := jobs[len(jobs)-1].ArrivalS + drainCapHours*3600

	type pendingJob struct {
		job     *benchJob
		nextTry float64
	}
	var pending []*pendingJob
	results := make(map[string]*execution, len(jobs))

	// Event times: arrivals plus retry ticks. Process chronologically.
	// A simple time-stepped loop over sorted event times is sufficient and
	// deterministic: collect candidate times, always process the earliest.
	arrivalIdx := 0
	now := 0.0
	for {
		// Next event: earliest of (next arrival, earliest pending retry).
		nextT := math.Inf(1)
		if arrivalIdx < len(jobs) {
			nextT = jobs[arrivalIdx].ArrivalS
		}
		for _, p := range pending {
			if p.nextTry < nextT {
				nextT = p.nextTry
			}
		}
		if math.IsInf(nextT, 1) || nextT > horizonS {
			break
		}
		now = nextT

		if arrivalIdx < len(jobs) && jobs[arrivalIdx].ArrivalS <= now {
			pending = append(pending, &pendingJob{job: jobs[arrivalIdx], nextTry: now})
			arrivalIdx++
		}

		// Try to place every due pending job, in arrival order.
		var still []*pendingJob
		for _, p := range pending {
			if p.nextTry > now {
				still = append(still, p)
				continue
			}
			placed := tryPlace(policy, p.job, seed, now, targets, freeAt, depth, results)
			if !placed {
				p.nextTry = now + pendingRetryS
				still = append(still, p)
			}
		}
		pending = still
	}

	// Jobs never placed within the horizon.
	for _, j := range jobs {
		if _, ok := results[j.ID]; !ok {
			results[j.ID] = &execution{
				JobID: j.ID, Circuit: j.Circuit.Name, Width: j.Circuit.Width,
				Shots: j.Shots, ArrivalS: j.ArrivalS,
				WaitS: horizonS - j.ArrivalS, Unplaced: true,
				Floor2Q: j.Floor2Q, FloorRO: j.FloorRO, DeadlineS: j.DeadlineS,
				SLOViolate: false, // never ran under a violated floor
			}
		}
	}

	out := make([]*execution, 0, len(results))
	for _, j := range jobs {
		out = append(out, results[j.ID])
	}
	return out, nil
}

func tryPlace(policy scheduler.SchedulingPolicy, j *benchJob, seed int64, now float64,
	targets []*seriesTarget, freeAt map[string]float64, depth map[string]uint32,
	results map[string]*execution) bool {

	fleet := make([]*scheduler.TargetView, 0, len(targets))
	byName := make(map[string]*seriesTarget, len(targets))
	for _, t := range targets {
		wait := math.Max(0, freeAt[t.Name]-now)
		fleet = append(fleet, t.viewAt(now, wait, depth[t.Name]))
		byName[t.Name] = t
	}
	decision := scheduler.Schedule(policy, j.view(), fleet, simBase.Add(time.Duration(now*float64(time.Second))))
	if decision.Target == "" {
		return false
	}

	target := byName[decision.Target]
	start := math.Max(now, freeAt[target.Name])
	dur := execDuration(j.Shots)
	end := start + dur
	freeAt[target.Name] = end
	depth[target.Name]++

	// Snapshot the physics will use: the one in effect at execution start.
	execSnap := target.snapAt(start)

	violated := false
	if j.Floor2Q > 0 {
		if v, ok := minMetric(execSnap, "gate.2q."); !ok || v > j.Floor2Q {
			violated = true
		}
	}
	if j.FloorRO > 0 {
		if v, ok := minMetricExact(execSnap, "readout.error"); !ok || v > j.FloorRO {
			violated = true
		}
	}

	results[j.ID] = &execution{
		JobID: j.ID, Circuit: j.Circuit.Name, Width: j.Circuit.Width, Shots: j.Shots,
		ArrivalS: j.ArrivalS, StartS: start, EndS: end, WaitS: start - j.ArrivalS,
		Target: target.Name, SnapshotID: execSnap.SnapshotID, ESP: decision.PredictedESP,
		Floor2Q: j.Floor2Q, FloorRO: j.FloorRO, SLOViolate: violated,
		DeadlineS: j.DeadlineS, DeadlineOK: j.DeadlineS == 0 || end <= j.DeadlineS,
		// One physics measurement per unique (circuit, target, snapshot):
		// identical physical experiments share one seeded run (see report
		// methodology on measurement sharing).
		PhysSeed: physSeed(j.Circuit.Name + "|" + target.Name + "|" + execSnap.SnapshotID),
	}
	return true
}

func minMetric(s *seriesSnap, prefix string) (float64, bool) {
	best, found := 0.0, false
	for _, m := range s.Metrics {
		if !strings.HasPrefix(m.Name, prefix) || !strings.HasSuffix(m.Name, ".error") {
			continue
		}
		if !found || m.Value < best {
			best, found = m.Value, true
		}
	}
	return best, found
}

func minMetricExact(s *seriesSnap, name string) (float64, bool) {
	best, found := 0.0, false
	for _, m := range s.Metrics {
		if m.Name != name {
			continue
		}
		if !found || m.Value < best {
			best, found = m.Value, true
		}
	}
	return best, found
}

func physSeed(key string) int64 {
	var h int64 = 1469598103934665603
	for _, c := range key {
		h ^= int64(c)
		h *= 1099511628211
	}
	return h & 0x7fffffff
}

// ---- main ----

func main() {
	circuitsDir := flag.String("circuits", "bench/circuits", "vendored circuit dir")
	seriesPath := flag.String("series", "bench/out/series.json", "replay series file")
	policiesArg := flag.String("policies", "calib-aware/v0,static-best/v0,round-robin/v0", "comma-separated")
	seeds := flag.Int("seeds", 5, "number of workload seeds")
	baseSeed := flag.Int64("base-seed", 1000, "first seed value")
	jobsN := flag.Int("jobs", 500, "jobs per seed")
	outDir := flag.String("out", "bench/out", "output directory")
	flag.Parse()

	circuits, err := loadCircuits(*circuitsDir)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(*seriesPath)
	if err != nil {
		log.Fatal(err)
	}
	var series seriesFile
	if err := json.Unmarshal(raw, &series); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	policies := strings.Split(*policiesArg, ",")
	for s := 0; s < *seeds; s++ {
		seed := *baseSeed + int64(s)
		workload := generateWorkload(rand.New(rand.NewSource(seed)), circuits, *jobsN)
		for _, policyName := range policies {
			execs, err := simulate(policyName, seed, workload, series.Targets)
			if err != nil {
				log.Fatal(err)
			}
			name := fmt.Sprintf("schedule_%s_seed%d.json",
				strings.ReplaceAll(strings.ReplaceAll(policyName, "/", "-"), ".", "-"), seed)
			f, err := os.Create(filepath.Join(*outDir, name))
			if err != nil {
				log.Fatal(err)
			}
			enc := json.NewEncoder(f)
			enc.SetIndent("", " ")
			if err := enc.Encode(map[string]any{
				"policy": policyName, "seed": seed, "executions": execs,
			}); err != nil {
				log.Fatal(err)
			}
			_ = f.Close()
			placed := 0
			for _, e := range execs {
				if !e.Unplaced {
					placed++
				}
			}
			fmt.Printf("%-18s seed %d: %d jobs, %d placed\n", policyName, seed, len(execs), placed)
		}
	}
}
