package cli

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"testing"
)

type diagnosticSystemProxyClient struct {
	fakeSystemProxyClient
	operation logging.OperationMetadata
}

func (f *diagnosticSystemProxyClient) EnableSystemProxy(ctx context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	f.operation, _ = logging.OperationFromContext(ctx)
	return f.fakeSystemProxyClient.EnableSystemProxy(ctx, request)
}
func (f *diagnosticSystemProxyClient) DisableSystemProxy(ctx context.Context, request protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error) {
	f.operation, _ = logging.OperationFromContext(ctx)
	return f.fakeSystemProxyClient.DisableSystemProxy(ctx, request)
}
func TestSystemProxyDiagnostic_CLIMetadata(t *testing.T) {
	for _, action := range []string{"enable", "disable"} {
		client := &diagnosticSystemProxyClient{}
		root := newRoot(Dependencies{SystemProxyClient: client, NewOperationID: func() string { return "business-id" }}, &runOptions{})
		root.SetArgs([]string{"sysproxy", action, "--json"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if client.operation != (logging.OperationMetadata{ID: "business-id", Name: "system_proxy." + action}) {
			t.Fatalf("metadata=%#v", client.operation)
		}
	}
}
