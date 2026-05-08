package main

import (
	"flag"
	"os"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"github.com/aatchison/deploy-vllm-k8s/operator/controllers"
)

var rtscheme = clientgoscheme.Scheme

func init() {
	utilruntime.Must(vllmv1alpha1.AddToScheme(rtscheme))
}

// durationPtr is a tiny helper for passing literal durations into
// controller-runtime's manager.Options, which uses *time.Duration so the
// fields can distinguish "unset" (defaults applied) from "set to zero".
func durationPtr(d time.Duration) *time.Duration { return &d }

func main() {
	var metricsAddr, probeAddr string
	var enableLeader bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	// Default leader-election ON so an accidental >1 replica deploy can't run two managers
	// against the same objects (which would fight over the SSA fieldOwner "vllm-operator"
	// and create a lost-update / Conflict-storm window). Operators that genuinely want
	// single-replica without a Lease can pass --leader-elect=false.
	flag.BoolVar(&enableLeader, "leader-elect", true, "Enable leader election for the controller manager.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                     rtscheme,
		Metrics:                    metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:     probeAddr,
		LeaderElection:             enableLeader,
		LeaderElectionID:           "vllm-operator-leader",
		LeaderElectionResourceLock: "leases",
		// Conservative-but-snappy lease tuning. 15s/10s/2s matches the
		// kube-controller-manager defaults: ~10s typical failover, ~15s worst
		// case, with low API-server churn (one Lease write every ~2s by the
		// active leader, none by standbys).
		LeaseDuration: durationPtr(15 * time.Second),
		RenewDeadline: durationPtr(10 * time.Second),
		RetryPeriod:   durationPtr(2 * time.Second),
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Single Recorder shared by both reconcilers — events surface in
	// `kubectl describe` keyed by the source name we pass here.
	recorder := mgr.GetEventRecorderFor("vllm-operator")

	if err := (&controllers.VLLMInstanceReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "VLLMInstance")
		os.Exit(1)
	}

	if err := (&controllers.LongContextInstanceReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "LongContextInstance")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctrl.Log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}
