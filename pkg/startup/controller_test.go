package startup

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func newController(objs ...*corev1.Pod) (*Controller, *fake.Clientset) {
	runtimeObjs := make([]runtime.Object, len(objs))
	for i, o := range objs {
		runtimeObjs[i] = o
	}
	client := fake.NewSimpleClientset(runtimeObjs...)
	return NewController(client), client
}

func newControllerWith(objs ...runtime.Object) (*Controller, *fake.Clientset) {
	cs := fake.NewSimpleClientset(objs...)
	return NewController(cs), cs
}

func podWith(
	name, node string,
	labels map[string]string,
	annotations map[string]string,
	containers []corev1.ContainerStatus,
	conditions []corev1.PodCondition,
) *corev1.Pod {
	if labels == nil {
		labels = map[string]string{}
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			NodeName: node,
		},
		Status: corev1.PodStatus{
			ContainerStatuses: containers,
			Conditions:        conditions,
		},
	}
	return p
}

func labeledStartup() map[string]string {
	return map[string]string{StartPodLabelKey: StartPodLabelValue}
}

func makeNode(name string, taints ...corev1.Taint) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Taints: taints},
	}
}

func TestStartupPodReady_NoPods(t *testing.T) {
	c, _ := newController()
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ready {
		t.Fatalf("expected not ready")
	}
}

func TestStartupPodReady_AnnotationTrue(t *testing.T) {
	p := podWith(
		"p1", "node1",
		labeledStartup(),
		map[string]string{StartPodReadyAnnotation: "true"},
		nil, nil,
	)
	c, _ := newController(p)
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Fatalf("expected ready due to annotation")
	}
}

func TestStartupPodReady_ReadyConditionAndContainers(t *testing.T) {
	p := podWith(
		"p1", "node1",
		labeledStartup(),
		nil,
		[]corev1.ContainerStatus{
			{Name: "c1", Ready: true},
			{Name: "c2", Ready: true},
		},
		[]corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		},
	)
	c, _ := newController(p)
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Fatalf("expected ready (condition + all containers)")
	}
}

func TestStartupPodReady_NotAllContainersReady(t *testing.T) {
	p := podWith(
		"p1", "node1",
		labeledStartup(),
		nil,
		[]corev1.ContainerStatus{
			{Name: "c1", Ready: true},
			{Name: "c2", Ready: false},
		},
		[]corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		},
	)
	c, _ := newController(p)
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ready {
		t.Fatalf("expected not ready (one container not ready)")
	}
}

func TestStartupPodReady_NoReadyCondition(t *testing.T) {
	p := podWith(
		"p1", "node1",
		labeledStartup(),
		nil,
		[]corev1.ContainerStatus{
			{Name: "c1", Ready: true},
		},
		nil, // no PodReady condition
	)
	c, _ := newController(p)
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ready {
		t.Fatalf("expected not ready (missing PodReady condition)")
	}
}

func TestStartupPodReady_OtherNodeIgnored(t *testing.T) {
	// Pod on different node meets readiness, but target node has none.
	p := podWith(
		"p1", "node2",
		labeledStartup(),
		map[string]string{StartPodReadyAnnotation: "true"},
		nil, nil,
	)
	c, _ := newController(p)
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ready {
		t.Fatalf("expected not ready (pod on different node)")
	}
}

func TestStartupPodReady_MultiplePodsOneQualifies(t *testing.T) {
	// First pod not ready, second ready via condition+containers.
	p1 := podWith(
		"p1", "node1",
		labeledStartup(),
		nil,
		[]corev1.ContainerStatus{
			{Name: "c1", Ready: false},
		},
		[]corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionFalse},
		},
	)
	p2 := podWith(
		"p2", "node1",
		labeledStartup(),
		nil,
		[]corev1.ContainerStatus{
			{Name: "c1", Ready: true},
			{Name: "c2", Ready: true},
		},
		[]corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		},
	)
	c, _ := newController(p1, p2)
	ready, err := c.startupPodReady("node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Fatalf("expected ready (second pod qualifies)")
	}
}

func TestStartupPodReady_ListError(t *testing.T) {
	c, client := newController()
	client.Fake.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})
	ready, err := c.startupPodReady("node1")
	if err == nil {
		t.Fatalf("expected error")
	}
	if ready {
		t.Fatalf("expected ready=false on error")
	}
}

func TestHasStartupTaint(t *testing.T) {
	n1 := makeNode("n1", StartupTaint)
	if !HasStartupTaint(n1) {
		t.Fatalf("expected startup taint present")
	}
	n2 := makeNode("n2")
	if HasStartupTaint(n2) {
		t.Fatalf("did not expect startup taint")
	}
}

func TestRemoveStartupTaint(t *testing.T) {
	n := makeNode("n1", StartupTaint)
	c, client := newControllerWith(n)
	if err := c.removeStartupTaint(n); err != nil {
		t.Fatalf("remove err: %v", err)
	}
	got, _ := client.CoreV1().Nodes().Get(ctx(), "n1", metav1.GetOptions{})
	if HasStartupTaint(got) {
		t.Fatalf("taint not removed")
	}
	if got.Annotations[NodeStartupCompletedAnnotation] == "" {
		t.Fatalf("completion annotation missing")
	}
}

func TestHandleNode_RemovesWhenPodReady(t *testing.T) {
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
	c.handleNode(n)
	got, _ := client.CoreV1().Nodes().Get(ctx(), "n1", metav1.GetOptions{})
	if HasStartupTaint(got) {
		t.Fatalf("expected taint removed")
	}
}

func TestHandlePod_TrigersRemovalOnReadyTransition(t *testing.T) {
	n := makeNode("n1", StartupTaint)
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "init-n1",
			Namespace: "default",
			Labels:    map[string]string{StartPodLabelKey: StartPodLabelValue},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	c, client := newControllerWith(n, p)

	// Not ready yet
	c.handlePod(p)
	still, _ := client.CoreV1().Nodes().Get(ctx(), "n1", metav1.GetOptions{})
	if !HasStartupTaint(still) {
		t.Fatalf("taint removed too early")
	}

	// Mark ready and update
	p.Annotations = map[string]string{StartPodReadyAnnotation: "true"}
	_, _ = client.CoreV1().Pods("default").Update(ctx(), p, metav1.UpdateOptions{})
	c.handlePod(p)
	got, _ := client.CoreV1().Nodes().Get(ctx(), "n1", metav1.GetOptions{})
	if HasStartupTaint(got) {
		t.Fatalf("taint should be removed after readiness")
	}
}

func TestBackfillTaint_AddsWhenEligible(t *testing.T) {
	n := makeNode("n1") // no taint
	c, client := newControllerWith(n)
	c.backfillTaint()
	got, _ := client.CoreV1().Nodes().Get(ctx(), "n1", metav1.GetOptions{})
	if !HasStartupTaint(got) {
		t.Fatalf("expected backfill to add taint")
	}
}

func TestBackfillTaint_SkipsWhenWorkloadPresent(t *testing.T) {
	n := makeNode("n1")
	work := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	c, _ := newControllerWith(n, work)
	c.backfillTaint()
	if HasStartupTaint(n) {
		t.Fatalf("should skip adding taint when workload pod exists")
	}
}

func TestHasWorkloadPods(t *testing.T) {
	n := makeNode("n1")
	sys := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sys", Namespace: "kube-system"},
		Spec:       corev1.PodSpec{NodeName: "n1"},
	}
	user := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "user", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "n1"},
	}
	c1, _ := newControllerWith(n, sys)
	if c1.hasWorkloadPods("n1") {
		t.Fatalf("system pod should not count as workload")
	}
	c2, _ := newControllerWith(n, user)
	if !c2.hasWorkloadPods("n1") {
		t.Fatalf("user pod should count as workload")
	}
}

// helper context
func ctx() context.Context {
	return context.TODO()
}

// Silence unused import (time) if not already used
var _ = time.Second
