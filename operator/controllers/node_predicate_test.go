package controllers

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// makeNode is a tiny helper to build a Node with a specific InternalIP, Ready
// condition status, and Ready condition LastHeartbeatTime — the three fields
// the predicate has to discriminate between.
func makeNode(name, internalIP string, ready corev1.ConditionStatus, heartbeat time.Time) *corev1.Node {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:              corev1.NodeReady,
					Status:            ready,
					LastHeartbeatTime: metav1.NewTime(heartbeat),
				},
			},
		},
	}
	if internalIP != "" {
		n.Status.Addresses = []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: internalIP},
		}
	}
	return n
}

// TestNodeRelevantUpdate is the headline regression test for issue #42 fix #2.
// Without the predicate, every kubelet heartbeat (~10s/node) would enqueue
// every Instance — heartbeat-only updates MUST return false. Real changes
// (InternalIP swap, Ready flip) MUST still return true so the endpoint can be
// re-resolved.
func TestNodeRelevantUpdate(t *testing.T) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Second) // simulates a kubelet heartbeat tick

	cases := []struct {
		name string
		old  *corev1.Node
		new  *corev1.Node
		want bool
	}{
		{
			name: "heartbeat-only update is ignored",
			old:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			new:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t1),
			want: false,
		},
		{
			name: "InternalIP change triggers reconcile",
			old:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			new:  makeNode("n1", "10.0.0.2", corev1.ConditionTrue, t0),
			want: true,
		},
		{
			name: "Ready True -> False triggers reconcile",
			old:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			new:  makeNode("n1", "10.0.0.1", corev1.ConditionFalse, t0),
			want: true,
		},
		{
			name: "Ready False -> True triggers reconcile",
			old:  makeNode("n1", "10.0.0.1", corev1.ConditionFalse, t0),
			new:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			want: true,
		},
		{
			name: "Ready Unknown -> True triggers reconcile",
			old:  makeNode("n1", "10.0.0.1", corev1.ConditionUnknown, t0),
			new:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			want: true,
		},
		{
			name: "InternalIP added (was empty) triggers reconcile",
			old:  makeNode("n1", "", corev1.ConditionTrue, t0),
			new:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			want: true,
		},
		{
			name: "InternalIP removed triggers reconcile",
			old:  makeNode("n1", "10.0.0.1", corev1.ConditionTrue, t0),
			new:  makeNode("n1", "", corev1.ConditionTrue, t0),
			want: true,
		},
		{
			name: "heartbeat tick + identical IP/Ready -> ignored",
			old:  makeNode("n1", "10.0.0.5", corev1.ConditionTrue, t0),
			new:  makeNode("n1", "10.0.0.5", corev1.ConditionTrue, t1),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeRelevantUpdate(event.UpdateEvent{
				ObjectOld: tc.old,
				ObjectNew: tc.new,
			})
			if got != tc.want {
				t.Errorf("nodeRelevantUpdate: got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNodeRelevantUpdate_NonNodeObjectsReturnFalse defends against a
// programmer error where the predicate is wired to a non-Node watch.
func TestNodeRelevantUpdate_NonNodeObjectsReturnFalse(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	if got := nodeRelevantUpdate(event.UpdateEvent{ObjectOld: pod, ObjectNew: pod}); got {
		t.Error("expected false for non-Node objects")
	}
}

// TestNodeWatchPredicate_DefaultsForCreateDelete confirms that
// nodeWatchPredicate's Create/Delete handlers default to true (Funcs without a
// CreateFunc/DeleteFunc returns true for those events). A node disappearing
// or appearing can change which IP we publish, so we must not filter those.
func TestNodeWatchPredicate_DefaultsForCreateDelete(t *testing.T) {
	p := nodeWatchPredicate()
	n := makeNode("n1", "10.0.0.1", corev1.ConditionTrue, time.Time{})

	if !p.Create(event.CreateEvent{Object: n}) {
		t.Error("Create should default to true")
	}
	if !p.Delete(event.DeleteEvent{Object: n}) {
		t.Error("Delete should default to true")
	}
}
