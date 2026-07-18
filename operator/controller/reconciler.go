// SPDX-License-Identifier: Apache-2.0

// Package controller reconciles QuantumJob custom resources against the rabi
// control plane: submit on create, mirror status while in flight, cancel on
// delete. The control plane remains the source of truth for job state; the
// CR is a projection.
package controller

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	tanglev1alpha1 "github.com/rabi-project/rabi/operator/api/v1alpha1"
)

// resyncEvery keeps status lag well under the 5s bar (T8.e2e).
const resyncEvery = 2 * time.Second

// RabiClient is the control-plane surface the reconciler needs.
type RabiClient struct {
	Jobs   apiv1alpha1.JobsServiceClient
	APIKey string
}

// DialRabi connects to the rabi gRPC endpoint.
func DialRabi(addr, apiKey string) (*RabiClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dialing rabi at %s: %w", addr, err)
	}
	return &RabiClient{Jobs: apiv1alpha1.NewJobsServiceClient(conn), APIKey: apiKey}, nil
}

func (c *RabiClient) ctx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.APIKey)
}

// Reconciler reconciles QuantumJob resources.
type Reconciler struct {
	client.Client
	Rabi *RabiClient
}

// Reconcile implements the loop.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr tanglev1alpha1.QuantumJob
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Deletion: best-effort cancel, then release the finalizer.
	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cr, tanglev1alpha1.Finalizer) {
			if cr.Status.JobID != "" {
				_, err := r.Rabi.Jobs.CancelJob(r.Rabi.ctx(ctx),
					&apiv1alpha1.JobRef{JobId: cr.Status.JobID})
				if err != nil && status.Code(err) != codes.FailedPrecondition &&
					status.Code(err) != codes.NotFound {
					return ctrl.Result{}, fmt.Errorf("cancelling job on delete: %w", err)
				}
				logger.Info("cancelled control-plane job on CR deletion",
					"jobId", cr.Status.JobID)
			}
			controllerutil.RemoveFinalizer(&cr, tanglev1alpha1.Finalizer)
			if err := r.Update(ctx, &cr); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&cr, tanglev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&cr, tanglev1alpha1.Finalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
	}

	if cr.Status.JobID == "" {
		return r.submit(ctx, &cr)
	}
	return r.sync(ctx, &cr)
}

// submit sends the document to the control plane, adopting an existing job
// first (a crash between SubmitJob and the status write must not double-run:
// jobs are searched by tenant + document name before submitting anew).
func (r *Reconciler) submit(ctx context.Context, cr *tanglev1alpha1.QuantumJob) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	tenant := tenantFor(cr)

	if adopted := r.adopt(ctx, tenant, cr.Name); adopted != "" {
		logger.Info("adopted existing control-plane job", "jobId", adopted)
		cr.Status.JobID = adopted
		if err := r.Status().Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: resyncEvery}, nil
	}

	doc, err := BuildDocument(cr)
	if err != nil {
		return r.markMessage(ctx, cr, "InvalidSpec: "+err.Error())
	}
	docStruct, err := structpb.NewStruct(doc)
	if err != nil {
		return r.markMessage(ctx, cr, "InvalidSpec: "+err.Error())
	}

	job, err := r.Rabi.Jobs.SubmitJob(r.Rabi.ctx(ctx), &apiv1alpha1.SubmitJobRequest{
		Tenant:     tenant,
		QuantumJob: docStruct,
	})
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			// Admission rejection: terminal for this spec — record and stop.
			return r.markMessage(ctx, cr, "AdmissionRejected: "+err.Error())
		}
		return ctrl.Result{}, fmt.Errorf("submitting job: %w", err)
	}

	logger.Info("submitted job", "jobId", job.GetJobId(), "tenant", tenant)
	cr.Status.JobID = job.GetJobId()
	applyJobStatus(cr, job)
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: resyncEvery}, nil
}

// adopt finds a control-plane job already created for this CR (same tenant
// and document name), newest first.
func (r *Reconciler) adopt(ctx context.Context, tenant, name string) string {
	resp, err := r.Rabi.Jobs.ListJobs(r.Rabi.ctx(ctx), &apiv1alpha1.ListJobsRequest{
		Tenant: tenant, PageSize: 200,
	})
	if err != nil {
		return ""
	}
	for _, job := range resp.GetJobs() {
		doc := job.GetQuantumJob().AsMap()
		if meta, ok := doc["metadata"].(map[string]any); ok && meta["name"] == name {
			return job.GetJobId()
		}
	}
	return ""
}

// sync mirrors control-plane state onto the CR status.
func (r *Reconciler) sync(ctx context.Context, cr *tanglev1alpha1.QuantumJob) (ctrl.Result, error) {
	job, err := r.Rabi.Jobs.GetJob(r.Rabi.ctx(ctx), &apiv1alpha1.JobRef{JobId: cr.Status.JobID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return r.markMessage(ctx, cr, "JobLost: control plane no longer knows job "+cr.Status.JobID)
		}
		return ctrl.Result{}, fmt.Errorf("fetching job: %w", err)
	}

	before := cr.Status
	applyJobStatus(cr, job)
	if cr.Status != before {
		if err := r.Status().Update(ctx, cr); err != nil {
			if errors.IsConflict(err) {
				return ctrl.Result{RequeueAfter: resyncEvery}, nil
			}
			return ctrl.Result{}, err
		}
	}

	switch cr.Status.Phase {
	case "SUCCEEDED", "FAILED", "CANCELLED":
		return ctrl.Result{}, nil // terminal: stop polling
	default:
		return ctrl.Result{RequeueAfter: resyncEvery}, nil
	}
}

func (r *Reconciler) markMessage(ctx context.Context, cr *tanglev1alpha1.QuantumJob, message string) (ctrl.Result, error) {
	cr.Status.Message = message
	cr.Status.SyncedAt = metav1.Now()
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tanglev1alpha1.QuantumJob{}).
		Complete(r)
}
