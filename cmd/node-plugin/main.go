package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	drapb "k8s.io/kubelet/pkg/apis/dra/v1beta1"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	"github.com/justin-oleary/cxl-dra-driver/pkg/nodeplugin"
)

const (
	DriverName       = "cxl.example.com"
	PluginSocketPath = "/var/lib/kubelet/plugins/cxl.example.com/plugin.sock"
	RegistrarSocket  = "/var/lib/kubelet/plugins_registry/cxl.example.com-reg.sock"
)

func main() {
	klog.InitFlags(nil)

	var nodeName string
	flag.StringVar(&nodeName, "node-name", "", "kubernetes node name (required)")
	flag.Parse()
	defer klog.Flush()

	if nodeName == "" {
		klog.ErrorS(nil, "--node-name is required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pluginServer, pluginCleanup, err := startPluginServer(nodeName)
	if err != nil {
		klog.ErrorS(err, "failed to start plugin server")
		os.Exit(1)
	}
	defer pluginCleanup()

	regServer, regCleanup, err := startRegistrationServer()
	if err != nil {
		klog.ErrorS(err, "failed to start registration server")
		os.Exit(1)
	}
	defer regCleanup()

	klog.InfoS("node plugin started", "driver", DriverName, "node", nodeName)

	<-ctx.Done()
	klog.InfoS("shutting down")

	pluginServer.GracefulStop()
	regServer.GracefulStop()
}

func startPluginServer(nodeName string) (*grpc.Server, func(), error) {
	// create directory first, then clean up any stale socket
	if err := os.MkdirAll(filepath.Dir(PluginSocketPath), 0750); err != nil {
		return nil, nil, fmt.Errorf("create socket directory: %w", err)
	}

	// remove stale socket if it exists
	_ = os.Remove(PluginSocketPath)

	listener, err := net.Listen("unix", PluginSocketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on socket: %w", err)
	}

	server := grpc.NewServer()
	plugin := nodeplugin.New()
	drapb.RegisterDRAPluginServer(server, plugin)

	go func() {
		if err := server.Serve(listener); err != nil {
			klog.ErrorS(err, "plugin server error")
		}
	}()

	klog.InfoS("DRA plugin server listening", "socket", PluginSocketPath)

	cleanup := func() {
		_ = os.Remove(PluginSocketPath)
	}

	return server, cleanup, nil
}

func startRegistrationServer() (*grpc.Server, func(), error) {
	// create directory first, then clean up any stale socket
	if err := os.MkdirAll(filepath.Dir(RegistrarSocket), 0750); err != nil {
		return nil, nil, fmt.Errorf("create registration socket directory: %w", err)
	}

	// remove stale socket if it exists
	_ = os.Remove(RegistrarSocket)

	listener, err := net.Listen("unix", RegistrarSocket)
	if err != nil {
		return nil, nil, fmt.Errorf("listen on registration socket: %w", err)
	}

	server := grpc.NewServer()
	regServer := nodeplugin.NewRegistrationServer(DriverName, PluginSocketPath, []string{"v1beta1.DRAPlugin"})
	registerapi.RegisterRegistrationServer(server, regServer)

	go func() {
		if err := server.Serve(listener); err != nil {
			klog.ErrorS(err, "registration server error")
		}
	}()

	klog.InfoS("registration server listening", "socket", RegistrarSocket)

	cleanup := func() {
		_ = os.Remove(RegistrarSocket)
	}

	return server, cleanup, nil
}
