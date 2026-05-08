package controllers

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// nodeRelevantUpdate returns true only when a Node update changes a field the
// instance controllers actually care about: the set of InternalIP addresses
// (used to build the public endpoint) or the Ready condition status (used to
// pick a healthy node when assembling the endpoint).
//
// Without this predicate, kubelet heartbeats — which bump
// Status.Conditions[].LastHeartbeatTime every ~10s per node — would enqueue
// every Instance on every tick, producing a constant N×M reconcile firehose
// even when nothing materially changed. See issue #42.
func nodeRelevantUpdate(e event.UpdateEvent) bool {
	oldNode, ok := e.ObjectOld.(*corev1.Node)
	if !ok {
		return false
	}
	newNode, ok := e.ObjectNew.(*corev1.Node)
	if !ok {
		return false
	}

	if !sameInternalIPs(oldNode, newNode) {
		return true
	}
	if nodeReadyStatus(oldNode) != nodeReadyStatus(newNode) {
		return true
	}
	return false
}

// nodeWatchPredicate is the predicate.Funcs we attach to the Node watch in
// both reconcilers. Create/Delete events still enqueue (a brand-new or removed
// node may flip a previously-unhealthy endpoint), Generic is unused for Nodes,
// and only meaningful Updates pass through.
func nodeWatchPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: nodeRelevantUpdate,
	}
}

// sameInternalIPs returns true iff a and b expose the same multiset of
// InternalIP addresses. Order is preserved by kubelet in practice, but we
// compare as sets to be robust against reorderings that aren't semantically
// meaningful.
func sameInternalIPs(a, b *corev1.Node) bool {
	aIPs := internalIPs(a)
	bIPs := internalIPs(b)
	if len(aIPs) != len(bIPs) {
		return false
	}
	seen := make(map[string]int, len(aIPs))
	for _, ip := range aIPs {
		seen[ip]++
	}
	for _, ip := range bIPs {
		seen[ip]--
		if seen[ip] < 0 {
			return false
		}
	}
	return true
}

func internalIPs(n *corev1.Node) []string {
	if n == nil {
		return nil
	}
	out := make([]string, 0, 2)
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			out = append(out, a.Address)
		}
	}
	return out
}

// nodeReadyStatus returns the Status of the Ready condition, or
// ConditionUnknown if absent. Comparing by Status (not by the whole condition)
// keeps heartbeat-only ticks from flipping the result.
func nodeReadyStatus(n *corev1.Node) corev1.ConditionStatus {
	if n == nil {
		return corev1.ConditionUnknown
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status
		}
	}
	return corev1.ConditionUnknown
}
