// SPDX-License-Identifier: Apache-2.0

// Package registry maintains the fleet view: which targets exist, their
// capabilities, and their latest device state. Adapters are dialed and cached
// from M2 onward; at M0 the registry is an empty fleet.
package registry

import (
	"context"

	apiv1alpha1 "tangle.dev/tangle/gen/go/tangle/api/v1alpha1"
	"tangle.dev/tangle/internal/job"
)

// Registry is the control plane's authoritative view of registered targets.
type Registry struct{}

// New returns a registry with no configured adapters (empty fleet).
func New() *Registry {
	return &Registry{}
}

// ListTargets returns all known targets, optionally filtered by modality.
func (r *Registry) ListTargets(_ context.Context, _ string) ([]*apiv1alpha1.Target, error) {
	return nil, nil
}

// GetTarget returns the named target ("<site>/<target_id>") or nil when unknown.
func (r *Registry) GetTarget(_ context.Context, _ string) (*apiv1alpha1.Target, error) {
	return nil, nil
}

// FleetView reports the program formats and billing units currently offered
// by ≥1 registered target, for admission validation. Empty fleet at M0/M1.
func (r *Registry) FleetView(_ context.Context) job.FleetView {
	return job.FleetView{}
}
