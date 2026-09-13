package session

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type routingSnapshotClient struct {
	*fakeClient
	calls int
}

func (c *routingSnapshotClient) Routing(context.Context) (protocol.RoutingStatus, error) {
	c.calls++
	return protocol.RoutingStatus{DesiredMode: "direct", State: "pending", Revision: 5}, nil
}

func TestSession_RoutingSurvivesCoreSnapshotFailureAndInvalidatesCandidates(t *testing.T) {
	c := &routingSnapshotClient{fakeClient: newFakeClient()}
	c.coreFailures = 1
	s := New(c, Options{})
	status := protocol.Status{Revision: 5, Capabilities: []string{protocol.CapabilityCore, protocol.CapabilityRouting}}
	if err := s.pollStatus(context.Background(), status); err == nil {
		t.Fatal("expected core snapshot error")
	}
	first, second, third := <-s.control, <-s.control, <-s.control
	if first.Kind != EventStatus || second.Kind != EventProxies || second.Err == nil || third.Kind != EventRouting || third.Routing.State != "pending" || third.Epoch != first.Epoch {
		t.Fatal("routing/candidate events missing")
	}
	status.Capabilities = nil
	if err := s.pollStatus(context.Background(), status); err != nil {
		t.Fatal(err)
	}
	lost := <-s.control
	if lost.Epoch <= first.Epoch || c.calls != 1 {
		t.Fatal("capability loss did not fence late routing results")
	}
}
