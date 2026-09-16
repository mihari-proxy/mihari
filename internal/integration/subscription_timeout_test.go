package integration

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type subscriptionTimeoutTransport func(*http.Request) (*http.Response, error)

func (f subscriptionTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSubscriptionTimeout_IPCFallbackAndFailedRefreshPreserveCache(t *testing.T) {
	// Refusal exercises the real proxy -> direct chain without a 30-second wait.
	// Actual successful-response body expiry is covered by downloader_timeout_test.
	proxy := httptest.NewServer(http.NotFoundHandler())
	proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	var directCalls atomic.Int32
	downloader := subscription.NewDownloader(subscription.DownloaderOptions{
		ProxyURL: proxyURL,
		Client: &http.Client{Transport: subscriptionTimeoutTransport(func(r *http.Request) (*http.Response, error) {
			directCalls.Add(1)
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > 120*time.Second || time.Until(deadline) < 115*time.Second {
				t.Error("daemon execution budget missing")
			}
			if fail.Load() {
				return nil, errors.New("fixture direct failure")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("proxies: []\n"))}, nil
		})},
	})
	f := newSubscriptionControlFixtureWithDownloader(t, downloader, &editController{})
	base := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return transport.DialContext(ctx, f.endpoint)
	}}
	t.Cleanup(base.CloseIdleConnections)
	c := controlclient.NewHTTP("http://mihari", "subscription-integration-token", &http.Client{Timeout: 10 * time.Second, Transport: subscriptionTimeoutTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/subscriptions" && r.Method == http.MethodPost || strings.HasSuffix(r.URL.Path, "/refresh") {
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) < 179*time.Second || time.Until(deadline) > 180*time.Second {
				t.Error("IPC mutation budget missing")
			}
			if time.Until(deadline) <= 120*time.Second+10*time.Second+15*time.Second {
				t.Error("IPC cannot cover execution and both recoveries")
			}
		}
		return base.RoundTrip(r)
	})})
	mode := "auto"
	if _, err := c.UpdateSubscription(context.Background(), f.profileID, protocol.SubscriptionUpdateRequest{OperationID: "auto", ProxyMode: &mode}); err != nil {
		t.Fatal(err)
	}
	first, err := c.RefreshSubscription(context.Background(), f.profileID, protocol.MutationRequest{OperationID: "fallback"})
	if err != nil || !first.Subscription.Cached || directCalls.Load() != 1 || first.Subscription.ProxyMode != "auto" {
		t.Fatalf("IPC fallback failed: %+v %v", first, err)
	}
	fail.Store(true)
	if _, err := c.RefreshSubscription(context.Background(), f.profileID, protocol.MutationRequest{OperationID: "both-failed"}); err == nil {
		t.Fatal("both failures reported success")
	}
	current, err := c.Subscription(context.Background(), f.profileID)
	if err != nil || current.Subscription.Generation != first.Subscription.Generation || !current.Subscription.Cached || current.Subscription.LastError == "" {
		t.Fatalf("failed refresh lost cache: %+v %v", current, err)
	}
	added, err := c.AddSubscription(context.Background(), protocol.SubscriptionAddRequest{OperationID: "register-failed-fetch", Name: "registered", URL: "https://example.test/another", ProxyMode: "auto"})
	if err != nil || added.Subscription.ID == "" || added.Subscription.LastError == "" {
		t.Fatalf("durable registration lost: %+v %v", added, err)
	}
	list, err := c.Subscriptions(context.Background())
	if err != nil || len(list.Subscriptions) != 2 || directCalls.Load() != 3 {
		t.Fatalf("mutation replayed or registration duplicated: calls=%d profiles=%d err=%v", directCalls.Load(), len(list.Subscriptions), err)
	}
}
