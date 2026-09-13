package session

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type proxyFailureClient struct {
	*fakeClient
	failure error
}

func (c proxyFailureClient) ProxyGroups(context.Context) (protocol.ProxyGroups, error) {
	return protocol.ProxyGroups{}, c.failure
}

func TestPoll_ProxyFailurePublishesErrorAndContinuesRules(t *testing.T) {
	failure := errors.New("read failed")
	s := New(proxyFailureClient{newFakeClient(), failure}, Options{})
	if err := s.pollSnapshots(context.Background(), protocol.Status{Capabilities: []string{protocol.CapabilityProxies, protocol.CapabilityRules}}); !errors.Is(err, failure) {
		t.Fatal("poll did not return the proxy failure")
	}
	var proxyError, rules bool
	for len(s.control) > 0 {
		event := <-s.control
		if event.Kind == EventProxies && errors.Is(event.Err, failure) {
			proxyError = true
		}
		if event.Kind == EventRules {
			rules = true
		}
	}
	if !proxyError || !rules {
		t.Fatalf("proxyError=%v rules=%v", proxyError, rules)
	}
}

func TestPoll_ProxySnapshotDoesNotClaimStreamObservationTime(t *testing.T) {
	s := New(newFakeClient(), Options{})
	if err := s.pollSnapshots(context.Background(), protocol.Status{Capabilities: []string{protocol.CapabilityProxies}}); err != nil {
		t.Fatal(err)
	}
	for len(s.control) > 0 {
		event := <-s.control
		if event.Kind == EventProxies {
			if !event.ObservedAt.IsZero() {
				t.Fatal("proxy poll can overwrite the last daemon stream observation")
			}
			return
		}
	}
	t.Fatal("proxy snapshot missing")
}
