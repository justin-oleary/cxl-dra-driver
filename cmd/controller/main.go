package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
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
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (uses in-cluster if empty)")
	flag.StringVar(&cxlEndpoint, "cxl-endpoint", "http://localhost:8080", "CXL switch endpoint")
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

	factory.Start(ctx.Done())

	if err := ctrl.Run(ctx, 2); err != nil {
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
