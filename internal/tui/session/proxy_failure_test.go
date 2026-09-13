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
	_ = s.pollSnapshots(context.Background(), protocol.Status{Capabilities: []string{protocol.CapabilityProxies, protocol.CapabilityRules}})
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
