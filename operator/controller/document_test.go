// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	apiv1alpha1 "github.com/mAengo31/rabi/gen/go/tangle/api/v1alpha1"
	tanglev1alpha1 "github.com/mAengo31/rabi/operator/api/v1alpha1"
)

func cr(name, namespace string, annotations, labels map[string]string, spec string) *tanglev1alpha1.QuantumJob {
	return &tanglev1alpha1.QuantumJob{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Annotations: annotations, Labels: labels,
		},
		Spec: runtime.RawExtension{Raw: []byte(spec)},
	}
}

func TestTenantFor(t *testing.T) {
	if got := tenantFor(cr("j", "team-a", nil, nil, "{}")); got != "team-a" {
		t.Fatalf("tenant = %q, want namespace team-a", got)
	}
	annotated := cr("j", "team-a", map[string]string{tanglev1alpha1.TenantAnnotation: "org/lab"}, nil, "{}")
	if got := tenantFor(annotated); got != "org/lab" {
		t.Fatalf("tenant = %q, want annotation org/lab", got)
	}
}

func TestBuildDocument(t *testing.T) {
	spec := `{"workload":{"kind":"gate-model","gateModel":{"program":{"format":"openqasm3","inline":"T1BFTlFBU00="},"shots":100}}}`
	doc, err := BuildDocument(cr("bell", "demo", nil, map[string]string{"exp": "a"}, spec))
	if err != nil {
		t.Fatal(err)
	}
	if doc["apiVersion"] != "tangle.dev/v1alpha1" || doc["kind"] != "QuantumJob" {
		t.Fatalf("envelope wrong: %v", doc)
	}
	meta := doc["metadata"].(map[string]any)
	if meta["name"] != "bell" || meta["tenant"] != "demo" {
		t.Fatalf("metadata wrong: %v", meta)
	}
	if meta["labels"].(map[string]any)["exp"] != "a" {
		t.Fatalf("labels not passed through: %v", meta)
	}
	workload := doc["spec"].(map[string]any)["workload"].(map[string]any)
	if workload["kind"] != "gate-model" {
		t.Fatalf("spec not verbatim: %v", doc["spec"])
	}
	// The document must survive the structpb path used at submit time.
	if _, err := structpb.NewStruct(doc); err != nil {
		t.Fatalf("document not structpb-encodable: %v", err)
	}

	if _, err := BuildDocument(cr("x", "d", nil, nil, "")); err == nil {
		t.Fatal("empty spec must error")
	}
	if _, err := BuildDocument(cr("x", "d", nil, nil, "[1,2]")); err == nil {
		t.Fatal("non-object spec must error")
	}
}

func TestApplyJobStatus(t *testing.T) {
	statusDoc := map[string]any{
		"phase":       "SCHEDULED",
		"boundTarget": "sim/ibm-torino-r",
		"placement":   map[string]any{"reason": "policy calib-aware/v0 selected ..."},
		"conditions": []any{
			map[string]any{"type": "FormatAvailable", "status": "False", "message": "warned"},
			map[string]any{"type": "Cancelled", "status": "True", "message": "latest wins"},
		},
	}
	st, err := structpb.NewStruct(statusDoc)
	if err != nil {
		t.Fatal(err)
	}
	target := cr("bell", "demo", nil, nil, "{}")
	applyJobStatus(target, &apiv1alpha1.Job{JobId: "id-1", Status: st})

	if target.Status.Phase != "SCHEDULED" || target.Status.BoundTarget != "sim/ibm-torino-r" {
		t.Fatalf("status projection wrong: %+v", target.Status)
	}
	if target.Status.PlacementReason == "" || target.Status.Message != "latest wins" {
		t.Fatalf("reason/message wrong: %+v", target.Status)
	}
	if target.Status.SyncedAt.IsZero() {
		t.Fatal("syncedAt not set")
	}
}

func TestDeepCopyRoundTrip(t *testing.T) {
	original := cr("bell", "demo", map[string]string{"a": "b"}, map[string]string{"l": "v"},
		`{"workload":{"kind":"gate-model"}}`)
	original.Status.Phase = "PENDING"
	copied := original.DeepCopy()
	copied.Spec.Raw[2] = 'X'
	copied.Status.Phase = "RUNNING"
	if original.Status.Phase != "PENDING" {
		t.Fatal("status not deep-copied")
	}
	var a, b map[string]any
	_ = json.Unmarshal(original.Spec.Raw, &a)
	if err := json.Unmarshal(copied.Spec.Raw, &b); err == nil && a["workload"] == nil {
		t.Fatal("unexpected")
	}
	if string(original.Spec.Raw) == string(copied.Spec.Raw) {
		t.Fatal("spec raw not deep-copied")
	}
}
