// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/probe"
)

// metricsHandler emits Prometheus text format by hand (M12): aggregates
// only — no tenant-identifying data — so the endpoint can stay open to the
// scraper like /healthz. Hand-rolled to keep the air-gap surface at zero
// new dependencies.
func (s *Server) metricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		_, _ = fmt.Fprintln(w, "# HELP rabi_jobs_total Jobs by phase.")
		_, _ = fmt.Fprintln(w, "# TYPE rabi_jobs_total gauge")
		for _, phase := range job.Phases {
			var n int64
			if err := s.cfg.Store.Pool.QueryRow(ctx,
				`SELECT count(*) FROM jobs WHERE phase = $1`, string(phase)).Scan(&n); err == nil {
				_, _ = fmt.Fprintf(w, "rabi_jobs_total{phase=%q} %d\n", phase, n)
			}
		}

		health, err := probe.LatestHealth(ctx, s.cfg.Store)
		if err == nil {
			sort.Slice(health, func(i, j int) bool { return health[i].Target < health[j].Target })
			_, _ = fmt.Fprintln(w, "# HELP rabi_probe_fidelity Latest probe fidelity (1-TVD vs ideal) per target.")
			_, _ = fmt.Fprintln(w, "# TYPE rabi_probe_fidelity gauge")
			for _, h := range health {
				_, _ = fmt.Fprintf(w, "rabi_probe_fidelity{target=%q} %g\n", h.Target, h.Fidelity)
			}
			_, _ = fmt.Fprintln(w, "# HELP rabi_probe_estimator_abs_error |predicted ESP - measured| per target (pilot SLO: median <= 0.10).")
			_, _ = fmt.Fprintln(w, "# TYPE rabi_probe_estimator_abs_error gauge")
			for _, h := range health {
				if h.AbsError != nil {
					_, _ = fmt.Fprintf(w, "rabi_probe_estimator_abs_error{target=%q} %g\n", h.Target, *h.AbsError)
				}
			}
			_, _ = fmt.Fprintln(w, "# HELP rabi_probe_age_seconds Seconds since each target's latest probe.")
			_, _ = fmt.Fprintln(w, "# TYPE rabi_probe_age_seconds gauge")
			now := s.now()
			for _, h := range health {
				_, _ = fmt.Fprintf(w, "rabi_probe_age_seconds{target=%q} %g\n", h.Target, now.Sub(h.At).Seconds())
			}
		}
	}
}
