// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	tanglev1alpha1 "github.com/rabi-project/rabi/operator/api/v1alpha1"
)

// tenantFor maps a CR to its control-plane tenant: the tangle.dev/tenant
// annotation when present, otherwise the namespace (D-030).
func tenantFor(cr *tanglev1alpha1.QuantumJob) string {
	if t, ok := cr.Annotations[tanglev1alpha1.TenantAnnotation]; ok && t != "" {
		return t
	}
	return cr.Namespace
}

// BuildDocument assembles the wire QuantumJob document from the CR: the CR's
// spec verbatim under the spec-mandated envelope, with Kubernetes labels
// passed through.
func BuildDocument(cr *tanglev1alpha1.QuantumJob) (map[string]any, error) {
	if len(cr.Spec.Raw) == 0 {
		return nil, fmt.Errorf("spec is empty")
	}
	var spec map[string]any
	if err := json.Unmarshal(cr.Spec.Raw, &spec); err != nil {
		return nil, fmt.Errorf("spec is not an object: %w", err)
	}

	meta := map[string]any{
		"name":   cr.Name,
		"tenant": tenantFor(cr),
	}
	if len(cr.Labels) > 0 {
		labels := make(map[string]any, len(cr.Labels))
		for k, v := range cr.Labels {
			labels[k] = v
		}
		meta["labels"] = labels
	}

	return map[string]any{
		"apiVersion": "tangle.dev/v1alpha1",
		"kind":       "QuantumJob",
		"metadata":   meta,
		"spec":       spec,
	}, nil
}

// applyJobStatus projects the control plane's job onto the CR status.
func applyJobStatus(cr *tanglev1alpha1.QuantumJob, job *apiv1alpha1.Job) {
	st := job.GetStatus().AsMap()
	cr.Status.Phase, _ = st["phase"].(string)
	cr.Status.BoundTarget, _ = st["boundTarget"].(string)
	if placement, ok := st["placement"].(map[string]any); ok {
		cr.Status.PlacementReason, _ = placement["reason"].(string)
	}
	if conditions, ok := st["conditions"].([]any); ok && len(conditions) > 0 {
		if last, ok := conditions[len(conditions)-1].(map[string]any); ok {
			cr.Status.Message, _ = last["message"].(string)
		}
	}
	cr.Status.SyncedAt = metav1.NewTime(time.Now().Truncate(time.Second))
}
