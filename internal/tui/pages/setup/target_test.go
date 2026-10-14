package setup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSetupTargetRequests_DoNotUsePageRevision(t *testing.T) {
	for _, step := range []string{"core", "geoip", "endpoints", "complete"} {
		t.Run(step, func(t *testing.T) {
			client := &fakeClient{status: defaultStatus(false)}
			m := loadedModel(client)
			m.ObserveDaemon(protocol.Status{Capabilities: []string{"setup-target-v1"}}, protocol.CoreStatus{})
			var request any
			switch step {
			case "core":
				m.installCore()()
				request = client.installRequest
			case "geoip":
				m.updateGeoIP()()
				request = client.geoIPRequest
			case "endpoints":
				m.saveEndpoints()()
				request = client.update
			case "complete":
				m.complete()()
				request = client.update
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["execution_mode"] != "setup" || fields["if_revision"] != nil {
				t.Fatalf("setup must declare its target without a stale page revision: %s", raw)
			}
		})
	}
}

func TestSetupTargetRequests_OldDaemonDoesNotReceiveMutation(t *testing.T) {
	client := &fakeClient{status: defaultStatus(false)}
	m := loadedModel(client)
	m.ObserveDaemon(protocol.Status{}, protocol.CoreStatus{})
	if cmd := m.installCore(); cmd != nil {
		cmd()
	}
	if client.installCalls != 0 {
		t.Fatal("old daemon received setup mutation")
	}
	if m.lastError == "" {
		t.Fatal("missing upgrade guidance")
	}
}

func supportedModel(client Client, id func() string) *Model {
	return supportedModelWithContext(context.Background(), client, id)
}
func supportedModelWithContext(ctx context.Context, client Client, id func() string) *Model {
	m := NewWithContext(ctx, client, id)
	m.ObserveDaemon(protocol.Status{Capabilities: []string{protocol.CapabilitySetupTarget, protocol.OperationStatusCapability}}, protocol.CoreStatus{})
	return m
}
