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

	startup "github.com/zhangchl007/nodetaintshandler/pkg/startup"
)

// helper: build AdmissionReview JSON once per scenario (simulates apiserver payload)
func buildReviewBytes(node *corev1.Node, op admissionv1.Operation) []byte {
	raw, _ := json.Marshal(node)
	ar := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			UID:       "bench-uid",
			Kind:      v1.GroupVersionKind{Kind: "Node"},
			Operation: op,
			Object:    runtimeRaw(raw),
		},
	}
	b, _ := json.Marshal(ar)
	return b
}

// core benchmark driver
func benchMutate(b *testing.B, body []byte) {
	b.Helper()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mutate-node", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		MutateNode(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status %d", rr.Code)
		}
	}
}

func BenchmarkMutateNode_AddTaint_Empty(b *testing.B) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n-empty"},
		Spec:       corev1.NodeSpec{},
	}
	body := buildReviewBytes(node, admissionv1.Create)
	// Sanity (one run) before timing
	benchMutate(b, body) // warm caches (JSON types, etc.)
	b.ReportAllocs()
	b.ResetTimer()
	benchMutate(b, body)
}

func BenchmarkMutateNode_AddTaint_WithExisting(b *testing.B) {
	// Many pre-existing taints to test append path scaling
	taints := make([]corev1.Taint, 0, 50)
	for i := 0; i < 50; i++ {
		taints = append(taints, corev1.Taint{
			Key:    "k" + string('a'+rune(i%26)),
			Value:  "v",
			Effect: corev1.TaintEffectNoSchedule,
		})
	}
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n-exist"},
		Spec:       corev1.NodeSpec{Taints: taints},
	}
	body := buildReviewBytes(node, admissionv1.Create)
	b.ReportAllocs()
	b.ResetTimer()
	benchMutate(b, body)
}

func BenchmarkMutateNode_Skip_HasStartupTaint(b *testing.B) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n-skip"},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{
			{
				Key:    startup.TaintKey,
				Value:  startup.TaintValue,
				Effect: corev1.TaintEffectNoSchedule,
			},
		}},
	}
	body := buildReviewBytes(node, admissionv1.Create)
	b.ReportAllocs()
	b.ResetTimer()
	benchMutate(b, body)
}

func BenchmarkMutateNode_Skip_OnUpdate(b *testing.B) {
	node := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{Name: "n-update"},
	}
	body := buildReviewBytes(node, admissionv1.Update)
	b.ReportAllocs()
	b.ResetTimer()
	benchMutate(b, body)
}

func BenchmarkMutateNode_InvalidJSON(b *testing.B) {
	body := []byte("{not-json")
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mutate-node", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		MutateNode(rr, req)
		if rr.Code != http.StatusBadRequest {
			b.Fatalf("expected 400, got %d", rr.Code)
		}
	}
}

func BenchmarkMutateNode_AddTaint_Parallel(b *testing.B) {
	node := &corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "n-par"}, Spec: corev1.NodeSpec{}}
	body := buildReviewBytes(node, admissionv1.Create)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/mutate-node", bytes.NewReader(body))
			rr := httptest.NewRecorder()
			MutateNode(rr, req)
			if rr.Code != http.StatusOK {
				b.Fatalf("status %d", rr.Code)
			}
		}
	})
}
