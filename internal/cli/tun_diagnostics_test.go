package cli

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"testing"
)

type diagnosticTunClient struct {
	fakeTunClient
	operation logging.OperationMetadata
}

func (f *diagnosticTunClient) EnableTun(ctx context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	f.operation, _ = logging.OperationFromContext(ctx)
	return f.fakeTunClient.EnableTun(ctx, request)
}
func (f *diagnosticTunClient) DisableTun(ctx context.Context, request protocol.TunMutationRequest) (protocol.TunStatus, error) {
	f.operation, _ = logging.OperationFromContext(ctx)
	return f.fakeTunClient.DisableTun(ctx, request)
}
func TestTunDiagnostic_CLIMetadata(t *testing.T) {
	for _, action := range []string{"enable", "disable"} {
		client := &diagnosticTunClient{}
		root := newRoot(Dependencies{TunClient: client, NewOperationID: func() string { return "business-id" }}, &runOptions{})
		root.SetArgs([]string{"tun", action, "--json"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if client.operation != (logging.OperationMetadata{ID: "business-id", Name: "tun." + action}) {
			t.Fatalf("metadata=%#v", client.operation)
		}
	}
}
