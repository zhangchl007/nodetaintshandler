package startup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	corev1 "k8s.io/api/core/v1"
	ktesting "k8s.io/client-go/testing"
)

//
// Additional unit tests
//

// TestRemoveStartupTaint_Idempotent_NoSecondUpdate ensures no Update call on second invocation.
func TestRemoveStartupTaint_Idempotent_NoSecondUpdate(t *testing.T) {
	n := makeNode("n1", StartupTaint)
	c, client := newControllerWith(n)

	var updates int32
	client.Fake.PrependReactor("update", "nodes", func(a ktesting.Action) (bool, runtime.Object, error) {
		atomic.AddInt32(&updates, 1)
		return false, nil, nil // allow normal handling
	})

	// First removal -> expect update
	if err := c.removeStartupTaint(n); err != nil {
		t.Fatalf("first remove err: %v", err)
	}
	if atomic.LoadInt32(&updates) == 0 {
		t.Fatalf("expected at least one update on first removal")
	}
	firstCount := atomic.LoadInt32(&updates)

	// Second removal -> no change, so no new update
	if err := c.removeStartupTaint(n); err != nil {
		t.Fatalf("second remove err: %v", err)
	}
	if atomic.LoadInt32(&updates) != firstCount {
		t.Fatalf("expected no additional update on idempotent removal (got %d want %d)", updates, firstCount)
	}
}

// TestRemoveStartupTaint_RetryOnConflict simulates one conflict then success.
func TestRemoveStartupTaint_RetryOnConflict(t *testing.T) {
	n := makeNode("n1", StartupTaint)
	c, client := newControllerWith(n)

	var attempts int32
	client.Fake.PrependReactor("update", "nodes", func(a ktesting.Action) (bool, runtime.Object, error) {
		// First update returns conflict
		if atomic.AddInt32(&attempts, 1) == 1 {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: "", Resource: "nodes"},
				n.Name,
				errors.New("conflict"),
			)
		}
		return false, nil, nil
	})

	if err := c.removeStartupTaint(n); err != nil {
		t.Fatalf("expected success after retry, got err: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", attempts)
	}
	// Verify taint gone
	got, _ := client.CoreV1().Nodes().Get(context.TODO(), n.Name, metav1.GetOptions{})
	if HasStartupTaint(got) {
		t.Fatalf("taint still present after conflict retry flow")
	}
}

// TestRemoveStartupTaint_NoTaint_NoAnnotationMutation ensures existing annotation unchanged.
func TestRemoveStartupTaint_NoTaint_NoAnnotationMutation(t *testing.T) {
	n := makeNode("n1") // no taint
	if n.Annotations == nil {
		n.Annotations = map[string]string{}
	}
	n.Annotations[NodeStartupCompletedAnnotation] = "12345"
	orig := n.Annotations[NodeStartupCompletedAnnotation]

	c, _ := newControllerWith(n)
	if err := c.removeStartupTaint(n); err != nil {
		t.Fatalf("remove err: %v", err)
	}
	if n.Annotations[NodeStartupCompletedAnnotation] != orig {
		t.Fatalf("annotation modified unexpectedly: got %s want %s", n.Annotations[NodeStartupCompletedAnnotation], orig)
	}
}

// TestBackfill_SkipsCompletedNode ensures completed annotation prevents backfill.
func TestBackfill_SkipsCompletedNode(t *testing.T) {
	n := makeNode("n1") // no taint
	n.Annotations = map[string]string{
		NodeStartupCompletedAnnotation: fmt.Sprintf("%d", time.Now().Unix()),
	}
	c, _ := newControllerWith(n)
	c.backfillTaint()
	if HasStartupTaint(n) {
		t.Fatalf("backfill should skip node with completion annotation")
	}
}

// TestHandlePod_IgnoresUnlabeled ensures unlabeled pod events do not trigger removal.
func TestHandlePod_IgnoresUnlabeled(t *testing.T) {
	n := makeNode("n1", StartupTaint)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plain",
			Namespace: "default",
			Annotations: map[string]string{
				StartPodReadyAnnotation: "true",
			},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	c, _ := newControllerWith(n, p)
	c.handlePod(p)
	if !HasStartupTaint(n) {
		t.Fatalf("taint removed by unlabeled pod event")
	}
}

// TestStartupPodReady_AnnotationOverridesContainerReadiness
func TestStartupPodReady_AnnotationOverridesContainerReadiness(t *testing.T) {
	p := podWith(
		"p1", "n1",
		labeledStartup(),
		map[string]string{StartPodReadyAnnotation: "true"},
		[]corev1.ContainerStatus{{Name: "c1", Ready: false}},
		[]corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
	)
	c, _ := newController(p)
	ready, err := c.startupPodReady("n1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ready {
		t.Fatalf("expected ready due to annotation override")
	}
}

//
// Additional benchmarks
//

// BenchmarkHandleNodeReady measures handleNode when pod already qualifies.
func BenchmarkHandleNodeReady(b *testing.B) {
	n := makeNode("n1", StartupTaint)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "init-n1",
			Namespace: "default",
			Labels:    map[string]string{StartPodLabelKey: StartPodLabelValue},
			Annotations: map[string]string{
				StartPodReadyAnnotation: "true",
			},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	c, client := newControllerWith(n, p)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-add taint so handleNode does work each iteration.
		if !HasStartupTaint(n) {
			n.Spec.Taints = append(n.Spec.Taints, StartupTaint)
			_, _ = client.CoreV1().Nodes().Update(context.TODO(), n, metav1.UpdateOptions{})
		}
		c.handleNode(n)
	}
}

// BenchmarkHandleNodeNotReady measures cost when not ready.
func BenchmarkHandleNodeNotReady(b *testing.B) {
	n := makeNode("n1", StartupTaint)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "init-n1",
			Namespace: "default",
			Labels:    map[string]string{StartPodLabelKey: StartPodLabelValue},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	c, _ := newControllerWith(n, p)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.handleNode(n)
	}
}

// BenchmarkStartupPodReadyManyPods simulates many pods on node with last one ready.
func BenchmarkStartupPodReadyManyPods(b *testing.B) {
	const podCount = 200
	objs := make([]runtime.Object, 0, podCount)
	for i := 0; i < podCount; i++ {
		labels := map[string]string{}
		annotations := map[string]string{}
		if i == podCount-1 {
			labels = labeledStartup()
			annotations[StartPodReadyAnnotation] = "true"
		}
		objs = append(objs, podWith(
			fmt.Sprintf("p-%d", i),
			"n1",
			labels,
			annotations,
			nil,
			nil,
		))
	}
	c, _ := newControllerWith(objs...)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ready, err := c.startupPodReady("n1")
		if err != nil {
			b.Fatalf("err: %v", err)
		}
		if !ready {
			b.Fatalf("expected ready")
		}
	}
}

// BenchmarkRemoveStartupTaintDeepSlice tests removal with large taint slice.
func BenchmarkRemoveStartupTaintDeepSlice(b *testing.B) {
	// Create many non-startup taints plus one startup.
	const extra = 200
	taints := make([]corev1.Taint, 0, extra+1)
	for i := 0; i < extra; i++ {
		taints = append(taints, corev1.Taint{
			Key:    "k" + strconv.Itoa(i),
			Value:  "v" + strconv.Itoa(i),
			Effect: corev1.TaintEffectNoSchedule,
		})
	}
	taints = append(taints, StartupTaint)
	n := makeNode("n1", taints...)
	c, client := newControllerWith(n)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Ensure startup taint present
		if !HasStartupTaint(n) {
			n.Spec.Taints = append(n.Spec.Taints, StartupTaint)
			_, _ = client.CoreV1().Nodes().Update(context.TODO(), n, metav1.UpdateOptions{})
		}
		if err := c.removeStartupTaint(n); err != nil {
			b.Fatalf("remove err: %v", err)
		}
	}
}

// Silence unused imports if timing not directly used elsewhere.
var _ = time.Second
