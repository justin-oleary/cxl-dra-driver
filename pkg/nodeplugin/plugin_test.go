package nodeplugin

import (
	"context"
	"testing"

	drapb "k8s.io/kubelet/pkg/apis/dra/v1beta1"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
)

func TestPlugin_NodePrepareResources(t *testing.T) {
	plugin := New()

	tests := []struct {
		name    string
		req     *drapb.NodePrepareResourcesRequest
		wantLen int
	}{
		{
			name: "single claim",
			req: &drapb.NodePrepareResourcesRequest{
				Claims: []*drapb.Claim{
					{
						Uid:       "claim-1",
						Name:      "test-claim",
						Namespace: "default",
					},
				},
			},
			wantLen: 1,
		},
		{
			name: "multiple claims",
			req: &drapb.NodePrepareResourcesRequest{
				Claims: []*drapb.Claim{
					{Uid: "claim-1", Name: "test-claim-1", Namespace: "default"},
					{Uid: "claim-2", Name: "test-claim-2", Namespace: "default"},
					{Uid: "claim-3", Name: "test-claim-3", Namespace: "default"},
				},
			},
			wantLen: 3,
		},
		{
			name:    "empty claims",
			req:     &drapb.NodePrepareResourcesRequest{Claims: nil},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := plugin.NodePrepareResources(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Claims) != tt.wantLen {
				t.Errorf("expected %d claims in response, got %d", tt.wantLen, len(resp.Claims))
			}
			// verify each claim has a response
			for _, claim := range tt.req.Claims {
				if _, ok := resp.Claims[claim.Uid]; !ok {
					t.Errorf("missing response for claim %s", claim.Uid)
				}
			}
		})
	}
}

func TestPlugin_NodePrepareResources_ContextCancellation(t *testing.T) {
	plugin := New()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &drapb.NodePrepareResourcesRequest{
		Claims: []*drapb.Claim{
			{Uid: "claim-1", Name: "test-claim", Namespace: "default"},
		},
	}

	_, err := plugin.NodePrepareResources(ctx, req)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestPlugin_NodeUnprepareResources(t *testing.T) {
	plugin := New()

	tests := []struct {
		name    string
		req     *drapb.NodeUnprepareResourcesRequest
		wantLen int
	}{
		{
			name: "single claim",
			req: &drapb.NodeUnprepareResourcesRequest{
				Claims: []*drapb.Claim{
					{Uid: "claim-1", Name: "test-claim", Namespace: "default"},
				},
			},
			wantLen: 1,
		},
		{
			name: "multiple claims",
			req: &drapb.NodeUnprepareResourcesRequest{
				Claims: []*drapb.Claim{
					{Uid: "claim-1", Name: "test-claim-1", Namespace: "default"},
					{Uid: "claim-2", Name: "test-claim-2", Namespace: "default"},
				},
			},
			wantLen: 2,
		},
		{
			name:    "empty claims",
			req:     &drapb.NodeUnprepareResourcesRequest{Claims: nil},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := plugin.NodeUnprepareResources(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Claims) != tt.wantLen {
				t.Errorf("expected %d claims in response, got %d", tt.wantLen, len(resp.Claims))
			}
		})
	}
}

func TestPlugin_NodeUnprepareResources_ContextCancellation(t *testing.T) {
	plugin := New()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := &drapb.NodeUnprepareResourcesRequest{
		Claims: []*drapb.Claim{
			{Uid: "claim-1", Name: "test-claim", Namespace: "default"},
		},
	}

	_, err := plugin.NodeUnprepareResources(ctx, req)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestRegistrationServer_GetInfo(t *testing.T) {
	server := NewRegistrationServer(
		"cxl.example.com",
		"/var/lib/kubelet/plugins/cxl.example.com/plugin.sock",
		[]string{"v1beta1.DRAPlugin"},
	)

	info, err := server.GetInfo(context.Background(), &registerapi.InfoRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Type != registerapi.DRAPlugin {
		t.Errorf("expected type %s, got %s", registerapi.DRAPlugin, info.Type)
	}
	if info.Name != "cxl.example.com" {
		t.Errorf("expected name cxl.example.com, got %s", info.Name)
	}
	if info.Endpoint != "/var/lib/kubelet/plugins/cxl.example.com/plugin.sock" {
		t.Errorf("unexpected endpoint: %s", info.Endpoint)
	}
	if len(info.SupportedVersions) != 1 || info.SupportedVersions[0] != "v1beta1.DRAPlugin" {
		t.Errorf("unexpected versions: %v", info.SupportedVersions)
	}
}

func TestRegistrationServer_NotifyRegistrationStatus(t *testing.T) {
	server := NewRegistrationServer("cxl.example.com", "/test/socket", nil)

	tests := []struct {
		name   string
		status *registerapi.RegistrationStatus
	}{
		{
			name:   "success",
			status: &registerapi.RegistrationStatus{PluginRegistered: true},
		},
		{
			name:   "failure",
			status: &registerapi.RegistrationStatus{PluginRegistered: false, Error: "test error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.NotifyRegistrationStatus(context.Background(), tt.status)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp == nil {
				t.Error("expected non-nil response")
			}
		})
	}
}
