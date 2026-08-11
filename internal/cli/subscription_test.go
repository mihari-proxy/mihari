package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type fakeSubscriptionClient struct {
	lastID      string
	lastAdd     protocol.SubscriptionAddRequest
	lastEnabled protocol.SubscriptionEnabledRequest
	lastUpdate  protocol.SubscriptionUpdateRequest
	removed     int
}

func (f *fakeSubscriptionClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return protocol.SubscriptionList{Schema: "mihari/v1", Revision: 2, ActiveID: "one", GlobalInterval: "12h", Subscriptions: []protocol.Subscription{{ID: "one", Name: "main", Enabled: true, Cached: true}}}, nil
}
func (f *fakeSubscriptionClient) Subscription(context.Context, string) (protocol.SubscriptionResult, error) {
	return protocol.SubscriptionResult{Schema: "mihari/v1", Subscription: protocol.Subscription{ID: "one", Name: "main"}}, nil
}
func (f *fakeSubscriptionClient) AddSubscription(_ context.Context, request protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error) {
	f.lastAdd = request
	return protocol.SubscriptionResult{Schema: "mihari/v1", OperationID: request.OperationID, Subscription: protocol.Subscription{ID: "one", Name: request.Name}}, nil
}
func (f *fakeSubscriptionClient) RefreshSubscription(_ context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	f.lastID = id
	return protocol.SubscriptionResult{Schema: "mihari/v1", OperationID: request.OperationID, Subscription: protocol.Subscription{ID: id}}, nil
}
func (f *fakeSubscriptionClient) UseSubscription(ctx context.Context, id string, request protocol.MutationRequest) (protocol.SubscriptionResult, error) {
	return f.RefreshSubscription(ctx, id, request)
}
func (f *fakeSubscriptionClient) SetSubscriptionEnabled(_ context.Context, id string, request protocol.SubscriptionEnabledRequest) (protocol.SubscriptionResult, error) {
	f.lastID, f.lastEnabled = id, request
	return protocol.SubscriptionResult{Schema: "mihari/v1", OperationID: request.OperationID, Subscription: protocol.Subscription{ID: id, Enabled: request.Enabled}}, nil
}
func (f *fakeSubscriptionClient) UpdateSubscription(_ context.Context, id string, request protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error) {
	f.lastID, f.lastUpdate = id, request
	return protocol.SubscriptionResult{Schema: "mihari/v1", OperationID: request.OperationID, Subscription: protocol.Subscription{ID: id}}, nil
}
func (f *fakeSubscriptionClient) RemoveSubscription(_ context.Context, id string, request protocol.MutationRequest) (protocol.MutationResult, error) {
	f.lastID, f.removed = id, f.removed+1
	return protocol.MutationResult{Schema: "mihari/v1", OperationID: request.OperationID}, nil
}

func TestSubscriptionListJSONIsRedacted(t *testing.T) {
	client := &fakeSubscriptionClient{}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"sub", "list", "--json"}, stdout, stderr, Dependencies{SubscriptionClient: client})
	if exit != ExitOK || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"active_id":"one"`) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if strings.Contains(strings.ToLower(stdout.String()), "url") {
		t.Fatalf("output exposed a URL field: %s", stdout)
	}
}

func TestSubscriptionMutationsUseOperationIDAndExplicitValues(t *testing.T) {
	client := &fakeSubscriptionClient{}
	dependencies := Dependencies{SubscriptionClient: client, NewOperationID: func() string { return "fixed" }}
	for _, args := range [][]string{
		{"sub", "add", "main", "https://example.test/sub", "--json"},
		{"sub", "refresh", "one", "--json"},
		{"sub", "use", "one", "--json"},
		{"sub", "disable", "one", "--json"},
		{"sub", "enable", "one", "--json"},
		{"sub", "set", "one", "--interval", "", "--auto-refresh=false", "--json"},
		{"sub", "remove", "one", "--yes", "--json"},
	} {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if exit := Execute(context.Background(), args, stdout, stderr, dependencies); exit != ExitOK || stderr.Len() != 0 {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, exit, stdout, stderr)
		}
	}
	if client.lastAdd.OperationID != "fixed" || client.lastAdd.URL == "" || client.lastEnabled.OperationID != "fixed" {
		t.Fatalf("requests not forwarded: %#v %#v", client.lastAdd, client.lastEnabled)
	}
	if client.lastUpdate.Interval == nil || *client.lastUpdate.Interval != "" || client.lastUpdate.AutoRefresh == nil || *client.lastUpdate.AutoRefresh {
		t.Fatalf("set lost explicit values: %#v", client.lastUpdate)
	}
	if client.removed != 1 {
		t.Fatalf("remove calls=%d", client.removed)
	}
}

func TestSubscriptionRemoveRequiresYes(t *testing.T) {
	client := &fakeSubscriptionClient{}
	exit := Execute(context.Background(), []string{"sub", "remove", "one", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SubscriptionClient: client})
	if exit != ExitUsage || client.removed != 0 {
		t.Fatalf("exit=%d removed=%d", exit, client.removed)
	}
}

func TestSubscriptionAddForwardsProxyMode(t *testing.T) {
	client := &fakeSubscriptionClient{}
	dependencies := Dependencies{SubscriptionClient: client, NewOperationID: func() string { return "op" }}
	exit := Execute(context.Background(), []string{"sub", "add", "main", "https://example.test/sub", "--proxy", "auto", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, dependencies)
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
	if client.lastAdd.ProxyMode != "auto" {
		t.Fatalf("add did not forward proxy mode: %#v", client.lastAdd)
	}
}

func TestSubscriptionAddResolvesDirectAlias(t *testing.T) {
	client := &fakeSubscriptionClient{}
	dependencies := Dependencies{SubscriptionClient: client, NewOperationID: func() string { return "op" }}
	exit := Execute(context.Background(), []string{"sub", "add", "main", "https://example.test/sub", "--proxy", "direct", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, dependencies)
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
	if client.lastAdd.ProxyMode != "" {
		t.Fatalf("--proxy direct should resolve to the empty direct value: %q", client.lastAdd.ProxyMode)
	}
}

func TestSubscriptionAddRejectsInvalidProxyMode(t *testing.T) {
	client := &fakeSubscriptionClient{}
	exit := Execute(context.Background(), []string{"sub", "add", "main", "https://example.test/sub", "--proxy", "bogus", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, Dependencies{SubscriptionClient: client})
	if exit != ExitUsage {
		t.Fatalf("exit=%d want ExitUsage", exit)
	}
	if client.lastAdd.URL != "" {
		t.Fatalf("invalid proxy mode should not reach the client: %#v", client.lastAdd)
	}
}

func TestSubscriptionSetForwardsProxyMode(t *testing.T) {
	client := &fakeSubscriptionClient{}
	dependencies := Dependencies{SubscriptionClient: client, NewOperationID: func() string { return "op" }}
	exit := Execute(context.Background(), []string{"sub", "set", "one", "--proxy", "proxy", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, dependencies)
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
	if client.lastUpdate.ProxyMode == nil || *client.lastUpdate.ProxyMode != "proxy" {
		t.Fatalf("set did not forward proxy mode: %#v", client.lastUpdate)
	}
}
