package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/klog/v2"

	startup "github.com/zhangchl007/nodetaintshandler/pkg/startup"
	"github.com/zhangchl007/nodetaintshandler/pkg/webhook"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	// Use in-cluster config since running as a pod
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Error getting in-cluster config: %v", err)
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		klog.Fatalf("Error creating clientset: %v", err)
	}

	// Startup controller
	stop := make(chan struct{})
	go startup.NewController(clientset).Run(stop)

	// Optional webhook (enable with env STARTUP_WEBHOOK=1)
	if os.Getenv("STARTUP_WEBHOOK") == "1" {
		mux := http.NewServeMux()
		webhook.Register(mux)
		go func() {
			// For production: serve TLS with cert/key mounted
			klog.Info("Starting mutating webhook server (insecure demo mode)")
			if err := http.ListenAndServe(":8443", mux); err != nil {
				klog.Fatalf("webhook server error: %v", err)
			}
		}()
	}

	// Health/ready endpoints
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200); w.Write([]byte("ok")) })
	http.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200); w.Write([]byte("ready")) })

	// Graceful shutdown
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		klog.Info("Shutdown signal received")
		close(stop)
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()

	klog.Info("Controller running")
	// Block forever
	select {}
}

func serveMutate(w http.ResponseWriter, r *http.Request) {
	klog.Info("Received a request for mutation")

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, 1048576) // 1 MB limit

	admissionReview := admissionv1.AdmissionReview{}
	if err := json.NewDecoder(r.Body).Decode(&admissionReview); err != nil {
		klog.Errorf("could not decode AdmissionReview: %v", err)
		http.Error(w, "could not decode AdmissionReview", http.StatusBadRequest)
		return
	}

	klog.Infof("AdmissionReview for %s %s", admissionReview.Request.Kind, admissionReview.Request.Namespace)

	// Your mutation logic here...

	// Example: Add a finalizer to the pod
	patch := []byte(`[{ "op": "add", "path": "/metadata/finalizers", "value": ["example.com/finalizer"] }]`)
	admissionResponse := admissionv1.AdmissionResponse{
		Allowed:   true,
		PatchType: func() *admissionv1.PatchType { pt := admissionv1.PatchTypeJSONPatch; return &pt }(),
		Patch:     patch,
	}

	response := admissionv1.AdmissionReview{
		Response: &admissionResponse,
	}
	respBytes, err := json.Marshal(response)
	if err != nil {
		klog.Errorf("could not encode AdmissionReview response: %v", err)
		http.Error(w, "could not encode AdmissionReview response", http.StatusInternalServerError)
		return
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, string(respBytes)); err != nil {
		klog.Errorf("could not write response: %v", err)
	}
}
