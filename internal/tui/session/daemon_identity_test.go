package session

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type identityStreamClient struct{ *fakeClient }

func (c identityStreamClient) Stream(ctx context.Context, kind string, receive func(protocol.StreamEvent) error) error {
	if err := receive(protocol.StreamEvent{Stream: kind, Data: []byte("{}")}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestSession_StreamEventsRetainTheirStartingOwner(t *testing.T) {
	s := New(identityStreamClient{newFakeClient()}, Options{})
	owner := protocol.Status{PID: 42, StartedAt: time.Unix(100, 0)}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	done := make(chan error, 1)
	go func() { done <- s.runStreams(ctx, make(chan struct{}, 1), owner) }()
	t.Cleanup(func() { cancel(); <-done })
	seen := map[EventKind]bool{}
	for len(seen) < 4 {
		var event Event
		select {
		case event = <-s.ordered:
		case event = <-s.traffic:
		case event = <-s.memory:
		case <-ctx.Done():
			t.Fatal("streams did not publish all resources")
		}
		if event.Status.PID != owner.PID || !event.Status.StartedAt.Equal(owner.StartedAt) {
			t.Fatalf("%s stream lost starting owner", event.Kind)
		}
		seen[event.Kind] = true
	}
}

func TestSession_SnapshotsIdentifyTheirDaemon(t *testing.T) {
	f := newFakeClient()
	s := New(f, Options{OrderedQueueSize: 32})
	status := protocol.Status{PID: 42, StartedAt: time.Unix(100, 0), Capabilities: []string{protocol.CapabilityCore, protocol.CapabilitySubscriptions, protocol.CapabilityProxies, protocol.CapabilityRules, protocol.CapabilityRuleProviders, protocol.CapabilityPreferences, protocol.CapabilityWebGUI, protocol.CapabilityLogging}}
	s.observeLoggingCapability(status)
	if err := s.poll(context.Background(), status); err != nil {
		t.Fatal(err)
	}
	if len(s.control) < 8 {
		t.Fatal("resource snapshots missing")
	}
	for len(s.control) > 0 {
		event := <-s.control
		if event.Status.PID != status.PID || !event.Status.StartedAt.Equal(status.StartedAt) {
			t.Fatalf("%s snapshot lost daemon identity", event.Kind)
		}
	}
}

func TestSession_DaemonReplacementInvalidatesRevisionCaches(t *testing.T) {
	for _, condition := range []string{"new PID", "reused PID", "same owner"} {
		t.Run(condition, func(t *testing.T) {
			s := &Session{loggingEpoch: 3}
			status := protocol.Status{PID: 42, StartedAt: time.Unix(100, 0), Capabilities: []string{protocol.CapabilityLogging, protocol.CapabilityRouting}}
			s.observeLoggingCapability(status)
			revision := uint64(6797)
			s.loggingRevision = &revision
			switch condition {
			case "new PID":
				status.PID++
			case "reused PID":
				status.StartedAt = time.Unix(101, 0)
			}
			s.observeLoggingCapability(status)
			if condition == "same owner" {
				if s.loggingEpoch != 3 || s.loggingRevision == nil {
					t.Fatal("unchanged owner invalidated a usable cache")
				}
			} else if s.loggingEpoch != 4 || s.loggingRevision != nil {
				t.Fatalf("new daemon retained old revision cache: epoch=%d revision=%v", s.loggingEpoch, s.loggingRevision)
			}
		})
	}
}
