// SPDX-License-Identifier: Apache-2.0

// rabi-operator reconciles QuantumJob custom resources against the rabi
// control plane: kubectl apply a QuantumJob, watch it route and run.
package main

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	tanglev1alpha1 "github.com/rabi-project/rabi/operator/api/v1alpha1"
	"github.com/rabi-project/rabi/operator/controller"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("setup")

	token := os.Getenv("RABI_TOKEN")
	if token == "" {
		logger.Error(nil, "RABI_TOKEN must be set (API token or bootstrap token)")
		os.Exit(1)
	}
	rabiAddr := envOr("RABI_API_ADDR", "localhost:9090")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Error(err, "adding client-go scheme")
		os.Exit(1)
	}
	if err := tanglev1alpha1.AddToScheme(scheme); err != nil {
		logger.Error(err, "adding tangle.dev scheme")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: envOr("METRICS_ADDR", ":8082")},
		HealthProbeBindAddress: envOr("HEALTH_ADDR", ":8083"),
	})
	if err != nil {
		logger.Error(err, "creating manager")
		os.Exit(1)
	}

	rabi, err := controller.DialRabi(rabiAddr, token)
	if err != nil {
		logger.Error(err, "dialing rabi")
		os.Exit(1)
	}

	if err := (&controller.Reconciler{Client: mgr.GetClient(), Rabi: rabi}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "setting up reconciler")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		logger.Error(err, "adding healthz")
		os.Exit(1)
	}

	logger.Info("rabi-operator starting", "rabi", rabiAddr)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		logger.Error(err, "manager exited")
		os.Exit(1)
	}
}
