package webhook

import (
	"encoding/json"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	startup "github.com/zhangchl007/nodetaintshandler/pkg/startup"
)

type patchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

const aksModeLabel = "kubernetes.azure.com/mode"

// MutateNode adds the startup taint only on node CREATE if missing.
func MutateNode(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body error", http.StatusBadRequest)
		return
	}
	review := admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, &review); err != nil {
		http.Error(w, "unmarshal error", http.StatusBadRequest)
		return
	}
	if review.Request == nil || review.Request.Kind.Kind != "Node" {
		writeResponse(w, review, nil)
		return
	}
	if review.Request.Operation != admissionv1.Create {
		// Do not re-taint on updates; controller manages removal.
		writeResponse(w, review, nil)
		return
	}

	node := &corev1.Node{}
	if err := json.Unmarshal(review.Request.Object.Raw, node); err != nil {
		writeResponse(w, review, nil)
		return
	}

	if startup.HasStartupTaint(node) {
		writeResponse(w, review, nil)
		return
	}

	// AKS: skip system-mode nodes to avoid needing kube-system tolerations there
	if val, ok := node.Labels[aksModeLabel]; ok && val == "system" {
		klog.Infof("Skipping startup taint for system-mode node %s", node.Name)
		writeResponse(w, review, nil)
		return
	}

	var ops []patchOp
	if len(node.Spec.Taints) == 0 {
		ops = append(ops, patchOp{
			Op:   "add",
			Path: "/spec/taints",
			Value: []corev1.Taint{{
				Key:    startup.TaintKey,
				Value:  startup.TaintValue,
				Effect: corev1.TaintEffectNoSchedule,
			}},
		})
	} else {
		ops = append(ops, patchOp{
			Op:   "add",
			Path: "/spec/taints/-",
			Value: corev1.Taint{
				Key:    startup.TaintKey,
				Value:  startup.TaintValue,
				Effect: corev1.TaintEffectNoSchedule,
			},
		})
	}
	patchBytes, _ := json.Marshal(ops)
	writePatch(w, review, patchBytes)
}

func writePatch(w http.ResponseWriter, in admissionv1.AdmissionReview, patch []byte) {
	pt := admissionv1.PatchTypeJSONPatch
	var uid types.UID
	if in.Request != nil {
		uid = in.Request.UID
	}
	resp := admissionv1.AdmissionReview{
		TypeMeta: in.TypeMeta,
		Response: &admissionv1.AdmissionResponse{
			UID:       uid,
			Allowed:   true,
			Patch:     patch,
			PatchType: &pt,
		},
	}
	out, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func writeResponse(w http.ResponseWriter, in admissionv1.AdmissionReview, _ []byte) {
	var uid types.UID
	if in.Request != nil {
		uid = in.Request.UID
	}
	resp := admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: true,
		},
	}
	out, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// Register registers handlers on a mux.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("/mutate-node", MutateNode)
	klog.Info("Webhook handler registered (/mutate-node)")
}
