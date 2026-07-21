// SPDX-License-Identifier: Apache-2.0

package api

import (
	"net/http"

	"github.com/rabi-project/rabi/internal/status"
)

// statusHandler serves the public status page (P2.M7): a static HTML document
// the control plane renders from its own database on each request. Aggregate
// only — no tenant data — so it is safe to expose unauthenticated.
func (s *Server) statusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := status.Gather(r.Context(), s.cfg.Store, s.started, s.now())
		if err != nil {
			http.Error(w, "status unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_ = status.Render(w, data)
	}
}
