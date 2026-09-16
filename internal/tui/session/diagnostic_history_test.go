package session

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"time"
)

type diagnosticHistoryClient struct {
	*fakeClient
	requests []struct {
		instance string
		after    uint64
	}
	pages []protocol.DiagnosticList
	err   error
}

func (c *diagnosticHistoryClient) Diagnostics(_ context.Context, instance string, after uint64, limit int) (protocol.DiagnosticList, error) {
	c.requests = append(c.requests, struct {
		instance string
		after    uint64
	}{instance, after})
	if c.err != nil {
		return protocol.DiagnosticList{}, c.err
	}
	page := c.pages[0]
	c.pages = c.pages[1:]
	return page, nil
}

func TestDiagnosticHistory_ResumesCursorAndPublishesRestart(t *testing.T) {
	client := &diagnosticHistoryClient{fakeClient: newFakeClient(), pages: []protocol.DiagnosticList{
		{Schema: "mihari.diagnostics/v1", InstanceID: "first", LatestSequence: 1, NextSequence: 1, State: protocol.DiagnosticAvailable, Records: []protocol.Diagnostic{{ID: "first:1"}}},
		{Schema: "mihari.diagnostics/v1", InstanceID: "first", LatestSequence: 2, NextSequence: 2, State: protocol.DiagnosticAvailable, Records: []protocol.Diagnostic{{ID: "first:2"}}},
		{Schema: "mihari.diagnostics/v1", InstanceID: "next", LatestSequence: 1, NextSequence: 1, State: protocol.DiagnosticRestarted, Records: []protocol.Diagnostic{{ID: "next:1"}}},
	}}
	s := New(client, Options{})
	for i := 0; i < 3; i++ {
		if err := s.pollStatus(context.Background(), protocol.Status{Capabilities: []string{protocol.CapabilityDiagnostics}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.requests) != 3 || client.requests[1].instance != "first" || client.requests[1].after != 1 || client.requests[2].after != 2 {
		t.Fatalf("history synchronization lost its cursor: %+v", client.requests)
	}
	count := 0
	for len(s.control) > 0 {
		event := <-s.control
		if event.Kind == EventDiagnostics {
			count++
			if count == 3 && event.Diagnostics.State != protocol.DiagnosticRestarted {
				t.Fatal("restart was hidden")
			}
		}
	}
	if count != 3 {
		t.Fatalf("history events=%d", count)
	}
}

func TestDiagnosticHistory_QueryFailureDoesNotFailBusinessPoll(t *testing.T) {
	cause := errors.New("fixture history query failed")
	client := &diagnosticHistoryClient{fakeClient: newFakeClient(), err: cause}
	s := New(client, Options{})
	if err := s.pollStatus(context.Background(), protocol.Status{Capabilities: []string{protocol.CapabilityDiagnostics}}); err != nil {
		t.Fatal("diagnostic lookup broke healthy daemon poll")
	}
	found := false
	for len(s.control) > 0 {
		event := <-s.control
		if event.Kind == EventDiagnostics && errors.Is(event.Err, cause) {
			found = true
		}
	}
	if !found || len(client.requests) != 1 {
		t.Fatal("query failure disappeared or recursively retried")
	}
}

func TestSession_ResourceFailurePreservesCauseAndContinuesReads(t *testing.T) {
	fake := newFakeClient()
	fake.coreFailures = 1
	s := New(fake, Options{})
	err := s.pollStatus(context.Background(), protocol.Status{Capabilities: []string{protocol.CapabilityCore, protocol.CapabilitySubscriptions, protocol.CapabilityRules}})
	if err == nil {
		t.Fatal("missing core failure")
	}
	found := map[EventKind]Event{}
	for len(s.control) > 0 {
		event := <-s.control
		found[event.Kind] = event
	}
	if found[EventCore].Err == nil || found[EventSubscriptions].Subscriptions.Schema == "" || found[EventRules].Rules.Schema == "" {
		t.Fatal("failed resource was hidden or independent reads skipped")
	}
	if _, ok := found[EventProxies]; ok {
		t.Fatal("invented an unexecuted proxy read failure")
	}
}

func TestSession_StreamFailurePublishesOwnerOccurrence(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "stream-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	owner := diagnostics.NewOwner(history, nil)
	s := New(failureSessionClient{fakeClient: newFakeClient(), err: errors.New("token=stream-fixture")}, Options{Reporter: owner.Report, PollInterval: time.Hour})
	if s.superviseStreams(sessionContext(t)) == nil {
		t.Fatal("expected fresh status failure")
	}
	found := false
	for len(s.control) > 0 {
		event := <-s.control
		if event.Kind != EventDiagnostics {
			continue
		}
		snapshot, ok := diagnostics.Snapshot(event.Err)
		if !ok || snapshot.ID == "" || history.Get(snapshot.ID).Diagnostic == nil {
			t.Fatal("stream event lost owner identity")
		}
		found = true
	}
	if !found {
		t.Fatal("stream failure was only sent to the file logger")
	}
}
