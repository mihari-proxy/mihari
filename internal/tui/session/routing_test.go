package session

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

type routingSnapshotClient struct {
	*fakeClient
	calls int
}

type routingProxyFailureClient struct{ *routingSnapshotClient }

func (c routingProxyFailureClient) ProxyGroups(context.Context) (protocol.ProxyGroups, error) {
	return protocol.ProxyGroups{}, errors.New("provider failure")
}

func TestSession_RoutingDoesNotDuplicateProviderFailureSnapshot(t *testing.T) {
	c := routingProxyFailureClient{&routingSnapshotClient{fakeClient: newFakeClient()}}
	s := New(c, Options{})
	status := protocol.Status{Capabilities: []string{protocol.CapabilityProxies, protocol.CapabilityRules, protocol.CapabilityRouting}}
	if err := s.pollStatus(context.Background(), status); err == nil {
		t.Fatal("provider error missing")
	}
	proxies, rules, routing := 0, 0, 0
	for len(s.control) > 0 {
		event := <-s.control
		switch event.Kind {
		case EventProxies:
			proxies++
		case EventRules:
			rules++
		case EventRouting:
			routing++
		}
	}
	if proxies != 1 || rules != 1 || routing != 1 {
		t.Fatalf("proxies=%d rules=%d routing=%d", proxies, rules, routing)
	}
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
