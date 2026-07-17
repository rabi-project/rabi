// SPDX-License-Identifier: Apache-2.0

// Package registry maintains the fleet view: it dials adapters, caches their
// capabilities, and polls device state. Adapters are separate processes
// speaking tangle.adapter.v1alpha1; the registry is always the client.
package registry

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	adapterv1alpha1 "tangle.dev/tangle/gen/go/tangle/adapter/v1alpha1"
	apiv1alpha1 "tangle.dev/tangle/gen/go/tangle/api/v1alpha1"
	"tangle.dev/tangle/internal/job"
)

const (
	discoverEvery = 30 * time.Second
	pollEvery     = 5 * time.Second
)

// Entry is the cached view of one fleet target.
type Entry struct {
	Name     string // fleet-scoped: "<site>/<target_id>"
	Site     string
	TargetID string
	Info     *adapterv1alpha1.TargetInfo
	Caps     *adapterv1alpha1.Capabilities
	State    *adapterv1alpha1.DeviceState
	StateAt  time.Time
}

type site struct {
	name   string
	addr   string
	conn   *grpc.ClientConn
	client adapterv1alpha1.AdapterServiceClient
}

// Registry is the control plane's authoritative view of registered targets.
type Registry struct {
	mu      sync.RWMutex
	sites   map[string]*site
	entries map[string]*Entry // by fleet-scoped name
	logger  *slog.Logger
}

// New returns a registry with no configured adapters (empty fleet).
func New() *Registry {
	return &Registry{
		sites:   map[string]*site{},
		entries: map[string]*Entry{},
		logger:  slog.Default(),
	}
}

// NewFromSpec parses TANGLE_ADAPTERS ("site=host:port,site2=host:port") and
// dials each adapter lazily. An empty spec yields an empty fleet.
func NewFromSpec(spec string) (*Registry, error) {
	r := New()
	if strings.TrimSpace(spec) == "" {
		return r, nil
	}
	for _, part := range strings.Split(spec, ",") {
		name, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" || addr == "" {
			return nil, fmt.Errorf("registry: malformed adapter spec %q (want site=host:port)", part)
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("registry: dial %s (%s): %w", name, addr, err)
		}
		r.sites[name] = &site{
			name: name, addr: addr, conn: conn,
			client: adapterv1alpha1.NewAdapterServiceClient(conn),
		}
	}
	return r, nil
}

// Start runs discovery and state polling until ctx is done. It performs one
// synchronous discovery pass first so the fleet is visible at startup.
func (r *Registry) Start(ctx context.Context) {
	r.discover(ctx)
	r.poll(ctx)
	go func() {
		discovery := time.NewTicker(discoverEvery)
		poll := time.NewTicker(pollEvery)
		defer discovery.Stop()
		defer poll.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-discovery.C:
				r.discover(ctx)
			case <-poll.C:
				r.poll(ctx)
			}
		}
	}()
}

func (r *Registry) discover(ctx context.Context) {
	r.mu.RLock()
	sites := make([]*site, 0, len(r.sites))
	for _, s := range r.sites {
		sites = append(sites, s)
	}
	r.mu.RUnlock()

	for _, s := range sites {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		resp, err := s.client.ListTargets(cctx, &adapterv1alpha1.ListTargetsRequest{})
		if err != nil {
			cancel()
			r.logger.Warn("adapter discovery failed", "site", s.name, "addr", s.addr, "error", err)
			continue
		}
		for _, info := range resp.GetTargets() {
			caps, err := s.client.GetCapabilities(cctx, &adapterv1alpha1.TargetRef{TargetId: info.GetTargetId()})
			if err != nil {
				r.logger.Warn("get capabilities failed", "site", s.name, "target", info.GetTargetId(), "error", err)
				continue
			}
			name := s.name + "/" + info.GetTargetId()
			r.mu.Lock()
			existing := r.entries[name]
			if existing == nil {
				r.entries[name] = &Entry{
					Name: name, Site: s.name, TargetID: info.GetTargetId(),
					Info: info, Caps: caps,
				}
				r.logger.Info("target registered", "target", name,
					"qubits", caps.GetNumQubits(), "formats", caps.GetProgramFormats())
			} else {
				existing.Info, existing.Caps = info, caps
			}
			r.mu.Unlock()
		}
		cancel()
	}
}

func (r *Registry) poll(ctx context.Context) {
	for _, e := range r.Entries() {
		s := r.siteFor(e.Site)
		if s == nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		state, err := s.client.GetDeviceState(cctx, &adapterv1alpha1.TargetRef{TargetId: e.TargetID})
		cancel()
		if err != nil {
			r.logger.Warn("device state poll failed", "target", e.Name, "error", err)
			continue
		}
		r.mu.Lock()
		if entry := r.entries[e.Name]; entry != nil {
			entry.State = state
			entry.StateAt = time.Now()
		}
		r.mu.Unlock()
	}
}

func (r *Registry) siteFor(name string) *site {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sites[name]
}

// AdapterClient returns the gRPC client for a site ("" when unknown).
func (r *Registry) AdapterClient(siteName string) adapterv1alpha1.AdapterServiceClient {
	s := r.siteFor(siteName)
	if s == nil {
		return nil
	}
	return s.client
}

// Entries returns a stable-ordered snapshot of all known targets.
func (r *Registry) Entries() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Entry, 0, len(r.entries))
	for _, e := range r.entries {
		copied := *e
		out = append(out, &copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entry returns one target by fleet-scoped name, or nil.
func (r *Registry) Entry(name string) *Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		copied := *e
		return &copied
	}
	return nil
}

// ListTargets implements the API's fleet view.
func (r *Registry) ListTargets(_ context.Context, modalityFilter string) ([]*apiv1alpha1.Target, error) {
	var out []*apiv1alpha1.Target
	for _, e := range r.Entries() {
		if modalityFilter != "" && e.Info.GetModality() != modalityFilter {
			continue
		}
		t, err := entryToAPI(e)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// GetTarget returns the named target ("<site>/<target_id>") or nil when unknown.
func (r *Registry) GetTarget(_ context.Context, name string) (*apiv1alpha1.Target, error) {
	e := r.Entry(name)
	if e == nil {
		return nil, nil
	}
	return entryToAPI(e)
}

// FleetView reports the program formats and billing units currently offered
// by ≥1 registered target, for admission validation.
func (r *Registry) FleetView(_ context.Context) job.FleetView {
	view := job.FleetView{
		ProgramFormats: map[string]bool{},
		BillingUnits:   map[string]bool{},
	}
	for _, e := range r.Entries() {
		for _, f := range e.Caps.GetProgramFormats() {
			view.ProgramFormats[f] = true
		}
		for _, u := range e.Caps.GetBillingUnits() {
			view.BillingUnits[u] = true
		}
	}
	return view
}

func entryToAPI(e *Entry) (*apiv1alpha1.Target, error) {
	caps, err := messageToStruct(e.Caps)
	if err != nil {
		return nil, fmt.Errorf("registry: encode capabilities for %s: %w", e.Name, err)
	}
	state, err := messageToStruct(e.State)
	if err != nil {
		return nil, fmt.Errorf("registry: encode state for %s: %w", e.Name, err)
	}
	return &apiv1alpha1.Target{Name: e.Name, Capabilities: caps, State: state}, nil
}

func messageToStruct(m proto.Message) (*structpb.Struct, error) {
	if m == nil || !m.ProtoReflect().IsValid() {
		return nil, nil
	}
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	out := &structpb.Struct{}
	if err := protojson.Unmarshal(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}
