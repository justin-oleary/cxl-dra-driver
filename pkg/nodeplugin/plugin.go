// Package nodeplugin implements the DRA node plugin gRPC service.
package nodeplugin

import (
	"context"

	"k8s.io/klog/v2"
	drapb "k8s.io/kubelet/pkg/apis/dra/v1beta1"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

// Plugin implements the DRA node plugin interface.
type Plugin struct {
	drapb.UnimplementedDRAPluginServer
}

// New creates a DRA node plugin.
func New() *Plugin {
	return &Plugin{}
}

// NodePrepareResources prepares CXL memory for pod use.
func (p *Plugin) NodePrepareResources(ctx context.Context, req *drapb.NodePrepareResourcesRequest) (*drapb.NodePrepareResourcesResponse, error) {
	response := &drapb.NodePrepareResourcesResponse{
		Claims: make(map[string]*drapb.NodePrepareResourceResponse),
	}

	for _, claim := range req.Claims {
		// check for context cancellation
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		klog.InfoS("preparing CXL memory",
			"claimUID", claim.Uid,
			"claimName", claim.Name,
			"namespace", claim.Namespace,
		)

		// TODO: real hardware attachment would go here
		// for mock, this is a no-op

		klog.InfoS("CXL memory prepared",
			"claimUID", claim.Uid,
		)

		response.Claims[claim.Uid] = &drapb.NodePrepareResourceResponse{}
	}

	return response, nil
}

// NodeUnprepareResources releases CXL memory after pod termination.
func (p *Plugin) NodeUnprepareResources(ctx context.Context, req *drapb.NodeUnprepareResourcesRequest) (*drapb.NodeUnprepareResourcesResponse, error) {
	response := &drapb.NodeUnprepareResourcesResponse{
		Claims: make(map[string]*drapb.NodeUnprepareResourceResponse),
	}

	for _, claim := range req.Claims {
		// check for context cancellation
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		klog.InfoS("releasing CXL memory",
			"claimUID", claim.Uid,
			"claimName", claim.Name,
		)

		response.Claims[claim.Uid] = &drapb.NodeUnprepareResourceResponse{}
	}

	return response, nil
}

// RegistrationServer handles kubelet plugin registration.
type RegistrationServer struct {
	registerapi.UnimplementedRegistrationServer
	driverName string
	endpoint   string
	versions   []string
}

// NewRegistrationServer creates a registration server for kubelet discovery.
func NewRegistrationServer(driverName, endpoint string, versions []string) *RegistrationServer {
	return &RegistrationServer{
		driverName: driverName,
		endpoint:   endpoint,
		versions:   versions,
	}
}

// GetInfo returns plugin information to kubelet.
func (r *RegistrationServer) GetInfo(ctx context.Context, req *registerapi.InfoRequest) (*registerapi.PluginInfo, error) {
	klog.InfoS("kubelet called GetInfo")
	return &registerapi.PluginInfo{
		Type:              registerapi.DRAPlugin,
		Name:              r.driverName,
		Endpoint:          r.endpoint,
		SupportedVersions: r.versions,
	}, nil
}

// NotifyRegistrationStatus receives registration status from kubelet.
func (r *RegistrationServer) NotifyRegistrationStatus(ctx context.Context, status *registerapi.RegistrationStatus) (*registerapi.RegistrationStatusResponse, error) {
	if status.PluginRegistered {
		klog.InfoS("plugin registered with kubelet successfully")
	} else {
		klog.ErrorS(nil, "plugin registration failed", "error", status.Error)
	}
	return &registerapi.RegistrationStatusResponse{}, nil
}
