package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/justin-oleary/cxl-dra-driver/pkg/controller"
	"github.com/justin-oleary/cxl-dra-driver/pkg/cxlclient"
)

var (
	kubeconfig  string
	cxlEndpoint string
	healthAddr  string
	ready       atomic.Bool
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (uses in-cluster if empty)")
	flag.StringVar(&cxlEndpoint, "cxl-endpoint", "http://localhost:8080", "CXL switch endpoint")
	flag.StringVar(&healthAddr, "health-addr", ":8081", "health probe bind address")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	defer klog.Flush()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	config, err := buildConfig(kubeconfig)
	if err != nil {
		klog.ErrorS(err, "failed to build kubeconfig")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.ErrorS(err, "failed to create clientset")
		os.Exit(1)
	}

	cxl := cxlclient.New(cxlEndpoint)

	factory := informers.NewSharedInformerFactory(clientset, 30*time.Second)

	ctrl := controller.New(clientset, factory, cxl)

	// start health server
	healthServer := startHealthServer(healthAddr)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = healthServer.Shutdown(shutdownCtx)
	}()

	factory.Start(ctx.Done())

	if err := ctrl.Run(ctx, 2, func() { ready.Store(true) }); err != nil {
		klog.ErrorS(err, "controller exited with error")
		os.Exit(1)
	}
}

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func startHealthServer(addr string) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if ready.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
		}
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	go func() {
		klog.InfoS("starting health server", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "health server error")
		}
	}()

	return server
}
