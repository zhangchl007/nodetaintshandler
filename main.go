package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	startup "github.com/zhangchl007/nodetaintshandler/pkg/startup"
	"github.com/zhangchl007/nodetaintshandler/pkg/webhook"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	certPath = "/tls/tls.crt"
	keyPath  = "/tls/tls.key"
)

var ready atomic.Bool

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	cfg, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("in-cluster config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("clientset: %v", err)
	}

	stop := make(chan struct{})
	go startup.NewController(clientset).Run(stop)

	// Always start webhook (avoids env misconfig causing 404 probes)
	startWebhook(ctx)

	go func() {
		<-ctx.Done()
		klog.Info("Shutdown signal received")
		close(stop)
		time.Sleep(300 * time.Millisecond)
	}()

	klog.Info("Controller + webhook running")
	select {}
}

func startWebhook(ctx context.Context) {
	// Wait for mounted certs (handles slight Secret projection delay)
	if err := waitForFiles(60*time.Second, certPath, keyPath); err != nil {
		klog.Fatalf("TLS files not available: %v", err)
	}

	mux := http.NewServeMux()
	// Business webhook
	webhook.Register(mux)
	// Probes
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	srv := &http.Server{
		Addr:              ":8443",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		klog.Fatalf("load keypair: %v", err)
	}
	srv.TLSConfig = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}

	go func() {
		klog.Infof("Starting webhook HTTPS server on %s", srv.Addr)
		ready.Store(true) // mark ready just before serving
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			klog.Fatalf("webhook server error: %v", err)
		}
	}()
}

func waitForFiles(timeout time.Duration, paths ...string) error {
	deadline := time.Now().Add(timeout)
	for {
		missing := false
		for _, p := range paths {
			if !fileExists(p) {
				missing = true
				break
			}
		}
		if !missing {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for TLS files")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
