package main

import (
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zhangchl007/nodetaintshandler/pkg/startup"
	"github.com/zhangchl007/nodetaintshandler/pkg/webhook"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
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
