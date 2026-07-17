// SPDX-License-Identifier: Apache-2.0

// Package v1alpha1 defines the QuantumJob custom resource. The spec document
// in the Tangle specification is already shaped like a Kubernetes resource
// (apiVersion tangle.dev/v1alpha1, kind QuantumJob, metadata/spec/status),
// so the CRD mirrors it directly: the CR's spec is the document's spec
// verbatim (validated server-side by rabi's admission against the JSON
// Schema), and the tenant maps to the namespace unless overridden by the
// tangle.dev/tenant annotation (docs/decisions.md D-030).
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the CRD group/version — identical to the spec document's
// apiVersion, which is what makes the mirroring exact.
var GroupVersion = schema.GroupVersion{Group: "tangle.dev", Version: "v1alpha1"}

// TenantAnnotation overrides the namespace-as-tenant default.
const TenantAnnotation = "tangle.dev/tenant"

// Finalizer guards deletion so in-flight jobs get a best-effort cancel.
const Finalizer = "tangle.dev/job-finalizer"

// QuantumJobStatus mirrors the interesting parts of the control plane's
// status document onto the CR.
type QuantumJobStatus struct {
	// JobID is the control-plane job id once submitted.
	JobID string `json:"jobId,omitempty"`
	// Phase is the lifecycle phase (PENDING … SUCCEEDED/FAILED/CANCELLED).
	Phase string `json:"phase,omitempty"`
	// BoundTarget is the fleet target the job was placed on.
	BoundTarget string `json:"boundTarget,omitempty"`
	// PlacementReason is the human-readable audit reason.
	PlacementReason string `json:"placementReason,omitempty"`
	// Message carries the latest condition message (errors, warnings).
	Message string `json:"message,omitempty"`
	// SyncedAt is when the operator last reconciled control-plane state.
	SyncedAt metav1.Time `json:"syncedAt,omitempty"`
}

// QuantumJob is the Schema for the quantumjobs API.
type QuantumJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the QuantumJob document's spec, passed through verbatim and
	// validated by rabi's admission (schema + semantic rules).
	Spec runtime.RawExtension `json:"spec"`

	Status QuantumJobStatus `json:"status,omitempty"`
}

// QuantumJobList contains a list of QuantumJob.
type QuantumJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QuantumJob `json:"items"`
}

// -- deepcopy (hand-written; the types are small and controller-gen would be
// the only reason to carry that toolchain, D-030) ---------------------------

func (in *QuantumJob) DeepCopyInto(out *QuantumJob) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
	in.Status.SyncedAt.DeepCopyInto(&out.Status.SyncedAt)
}

func (in *QuantumJob) DeepCopy() *QuantumJob {
	if in == nil {
		return nil
	}
	out := new(QuantumJob)
	in.DeepCopyInto(out)
	return out
}

func (in *QuantumJob) DeepCopyObject() runtime.Object { return in.DeepCopy() }

func (in *QuantumJobList) DeepCopyInto(out *QuantumJobList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]QuantumJob, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *QuantumJobList) DeepCopy() *QuantumJobList {
	if in == nil {
		return nil
	}
	out := new(QuantumJobList)
	in.DeepCopyInto(out)
	return out
}

func (in *QuantumJobList) DeepCopyObject() runtime.Object { return in.DeepCopy() }

// AddToScheme registers the types.
func AddToScheme(s *runtime.Scheme) error {
	builder := runtime.NewSchemeBuilder(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(GroupVersion, &QuantumJob{}, &QuantumJobList{})
		metav1.AddToGroupVersion(scheme, GroupVersion)
		return nil
	})
	return builder.AddToScheme(s)
}
