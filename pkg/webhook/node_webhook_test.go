package webhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	startup "github.com/zhangchl007/nodetaintshandler/pkg/startup"
)

func buildAdmissionReview(node *corev1.Node, op admissionv1.Operation, kind string) []byte {
	raw, _ := json.Marshal(node)
	if kind == "" {
		kind = "Node"
	}
	review := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "uid-123",
			Kind:      v1.GroupVersionKind{Kind: kind},
			Operation: op,
			Object:    runtimeRaw(raw),
		},
	}
	b, _ := json.Marshal(review)
	return b
}
func runtimeRaw(raw []byte) runtime.RawExtension {
	return runtime.RawExtension{Raw: raw}
}

func perform(body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mutate-node", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	MutateNode(rr, req)
	return rr
}

func decodeReview(t *testing.T, rr *httptest.ResponseRecorder) admissionv1.AdmissionReview {
	t.Helper()
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return out
}

func assertPatchNone(t *testing.T, ar admissionv1.AdmissionReview) {
	t.Helper()
	if ar.Response == nil {
		t.Fatalf("nil response")
	}
	if !ar.Response.Allowed {
		t.Fatalf("expected Allowed true")
	}
	if len(ar.Response.Patch) != 0 {
		t.Fatalf("expected no patch, got %v", string(ar.Response.Patch))
	}
	if ar.Response.PatchType != nil {
		t.Fatalf("expected nil PatchType")
	}
}

func extractPatch(t *testing.T, ar admissionv1.AdmissionReview) []patchOp {
	t.Helper()
	if ar.Response == nil || len(ar.Response.Patch) == 0 {
		t.Fatalf("no patch present")
	}
	var ops []patchOp
	if err := json.Unmarshal(ar.Response.Patch, &ops); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	return ops
}

func TestMutateNode_AddsTaintWhenNoTaints(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n1"},
		Spec:       corev1.NodeSpec{},
	}
	body := buildAdmissionReview(node, admissionv1.Create, "Node")
	rr := perform(body)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	ar := decodeReview(t, rr)
	if !ar.Response.Allowed {
		t.Fatalf("expected Allowed true")
	}
	ops := extractPatch(t, ar)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Op != "add" || op.Path != "/spec/taints" {
		t.Fatalf("unexpected op %+v", op)
	}
	// Value should be slice
	valBytes, _ := json.Marshal(op.Value)
	var taints []corev1.Taint
	if err := json.Unmarshal(valBytes, &taints); err != nil {
		t.Fatalf("unmarshal taints: %v", err)
	}
	if len(taints) != 1 {
		t.Fatalf("expected 1 taint, got %d", len(taints))
	}
	t0 := taints[0]
	if t0.Key != startup.TaintKey || t0.Value != startup.TaintValue || t0.Effect != corev1.TaintEffectNoSchedule {
		t.Fatalf("unexpected taint %+v", t0)
	}
}

func TestMutateNode_AppendsTaintWhenExistingTaints(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n2"},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: "foo", Value: "bar", Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	body := buildAdmissionReview(node, admissionv1.Create, "Node")
	rr := perform(body)
	ar := decodeReview(t, rr)
	ops := extractPatch(t, ar)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]
	if op.Path != "/spec/taints/-" || op.Op != "add" {
		t.Fatalf("unexpected op %+v", op)
	}
	valBytes, _ := json.Marshal(op.Value)
	var taint corev1.Taint
	if err := json.Unmarshal(valBytes, &taint); err != nil {
		t.Fatalf("unmarshal taint: %v", err)
	}
	if taint.Key != startup.TaintKey || taint.Value != startup.TaintValue || taint.Effect != corev1.TaintEffectNoSchedule {
		t.Fatalf("unexpected taint %+v", taint)
	}
}

func TestMutateNode_SkipsWhenAlreadyHasStartupTaint(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n3"},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{
				Key:    startup.TaintKey,
				Value:  startup.TaintValue,
				Effect: corev1.TaintEffectNoSchedule,
			}},
		},
	}
	body := buildAdmissionReview(node, admissionv1.Create, "Node")
	ar := decodeReview(t, perform(body))
	assertPatchNone(t, ar)
}

func TestMutateNode_SkipsOnUpdateOperation(t *testing.T) {
	node := &corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "n4"}}
	body := buildAdmissionReview(node, admissionv1.Update, "Node")
	ar := decodeReview(t, perform(body))
	assertPatchNone(t, ar)
}

func TestMutateNode_SkipsWhenKindNotNode(t *testing.T) {
	podLike := &corev1.Pod{ObjectMeta: v1.ObjectMeta{Name: "p1"}}
	body := buildAdmissionReview(
		&corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "ignored"}}, // Raw object still Node but Kind mismatch triggers skip
		admissionv1.Create,
		"Pod",
	)
	// Replace raw with actual pod for realism
	rawPod, _ := json.Marshal(podLike)
	var ar admissionv1.AdmissionReview
	_ = json.Unmarshal(body, &ar)
	ar.Request.Object = runtimeRaw(rawPod)
	fixed, _ := json.Marshal(ar)

	resp := perform(fixed)
	assertPatchNone(t, decodeReview(t, resp))
}

func TestMutateNode_SkipsAKSSystemMode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{
			Name:   "n5",
			Labels: map[string]string{aksModeLabel: "system"},
		},
	}
	body := buildAdmissionReview(node, admissionv1.Create, "Node")
	ar := decodeReview(t, perform(body))
	assertPatchNone(t, ar)
}

func TestMutateNode_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mutate-node", bytes.NewBufferString("{not-json"))
	rr := httptest.NewRecorder()
	MutateNode(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestMutateNode_RequestNil(t *testing.T) {
	rr := perform([]byte(`{}`))
	ar := decodeReview(t, rr)
	assertPatchNone(t, ar)
}
