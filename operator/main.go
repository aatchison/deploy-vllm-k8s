package main

import (
	"context"
	"flag"
	"os"
	"time"

	// Blank-import automaxprocs so GOMAXPROCS is clamped to the cgroup CPU
	// quota at process start. Without this, on a large node (e.g. 32 cores)
	// with a 500m CPU limit, the Go runtime would default GOMAXPROCS to
	// runtime.NumCPU() (32) and oversubscribe the GC mark workers and
	// scheduler — wasting ~10-30% CPU under load. Issue #82.
	_ "go.uber.org/automaxprocs"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	vllmv1alpha1 "github.com/aatchison/deploy-vllm-k8s/operator/api/v1alpha1"
	"github.com/aatchison/deploy-vllm-k8s/operator/controllers"
)

// eventBroadcasterRunnable adapts a record.EventBroadcaster onto
// manager.Runnable so the broadcaster shuts down cleanly when the manager's
// stop context is cancelled. Without this, dropped events would race the
// process exit.
type eventBroadcasterRunnable struct {
	broadcaster record.EventBroadcaster
}

func (e eventBroadcasterRunnable) Start(ctx context.Context) error {
	<-ctx.Done()
	e.broadcaster.Shutdown()
	return nil
}

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
	//
	// We wire the broadcaster manually instead of calling
	// mgr.GetEventRecorderFor: controller-runtime v0.24 deprecated that
	// helper in favor of mgr.GetEventRecorder, but the new method returns
	// k8s.io/client-go/tools/events.EventRecorder — a different interface
	// with a 7-arg Eventf shape (regarding/related/type/reason/action/note/args).
	// Reshaping every Eventf callsite (and the FakeRecorder-based tests)
	// would be a much larger surface change than this issue warrants. The
	// underlying record.EventBroadcaster + EventRecorder types are NOT
	// deprecated, so building one directly off the manager's REST config
	// gives the reconcilers the same record.EventRecorder behaviour they
	// already depend on, without tripping SA1019.
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		ctrl.Log.Error(err, "unable to build clientset for event recorder")
		os.Exit(1)
	}
	broadcaster := record.NewBroadcaster()
	// Mirror controller-runtime's own GetEventRecorderFor wiring: forward
	// events to apiserver via the core/v1 events sink and tee a structured
	// log line for any in-process observers (controller logs).
	broadcaster.StartStructuredLogging(0)
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	// Stop the broadcaster on manager shutdown so in-flight events drain
	// instead of getting cut off mid-write.
	if err := mgr.Add(eventBroadcasterRunnable{broadcaster: broadcaster}); err != nil {
		ctrl.Log.Error(err, "unable to register event broadcaster shutdown")
		os.Exit(1)
	}
	recorder := broadcaster.NewRecorder(rtscheme, corev1.EventSource{Component: "vllm-operator"})

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
