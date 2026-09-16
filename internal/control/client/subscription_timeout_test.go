package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestSubscriptionTimeout_CredentialBudgetExpiresBeforeDispatch(t *testing.T) {
	calls := 0
	c := NewHTTPWithCredentialProvider("http://mihari", timeoutCredentialFunc(func(ctx context.Context) (string, error) {
		calls++
		<-ctx.Done()
		return "", ctx.Err()
	}), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("expired credential lookup dispatched mutation")
		return nil, context.Canceled
	})})
	err := c.doMutation(context.Background(), logging.OperationMetadata{Name: "subscription.add"}, http.MethodPost, "/v1/subscriptions", protocol.SubscriptionAddRequest{}, new(protocol.SubscriptionResult), runtimeRequestOptions{timeout: 20 * time.Millisecond})
	var outcome interface{ OutcomeUnknown() bool }
	if calls != 1 || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &outcome) || outcome.OutcomeUnknown() {
		t.Fatalf("credential timeout not definitely unsent: %v", err)
	}
}

type timeoutCredentialFunc func(context.Context) (string, error)

func (f timeoutCredentialFunc) Load(ctx context.Context) (string, error) { return f(ctx) }

func invokeSubscriptionMutation(ctx context.Context, c *Client, add bool) error {
	if add {
		_, err := c.AddSubscription(ctx, protocol.SubscriptionAddRequest{})
		return err
	}
	_, err := c.RefreshSubscription(ctx, "sub", protocol.MutationRequest{})
	return err
}

func TestSubscriptionTimeout_LongRequestsOutliveOrdinaryBudget(t *testing.T) {
	for _, add := range []bool{false, true} {
		for _, credential := range []bool{false, true} {
			for _, body := range []bool{false, true} {
				t.Run(strings.Join([]string{map[bool]string{false: "refresh", true: "add"}[add], map[bool]string{false: "static", true: "credential"}[credential], map[bool]string{false: "headers", true: "body"}[body]}, "/"), func(t *testing.T) {
					started, ordinaryExpired := make(chan struct{}), make(chan struct{})
					transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
						if r.URL.Path == "/v1/core" {
							<-r.Context().Done()
							close(ordinaryExpired)
							return nil, r.Context().Err()
						}
						close(started)
						wait := func() error {
							select {
							case <-ordinaryExpired:
								return r.Context().Err()
							case <-r.Context().Done():
								return r.Context().Err()
							}
						}
						if !body {
							if err := wait(); err != nil {
								return nil, err
							}
						}
						reader := io.ReadCloser(io.NopCloser(strings.NewReader(`{"schema":"mihari/v1"}`)))
						if body {
							reader = &timeoutResponseBody{Reader: reader, wait: wait}
						}
						return &http.Response{StatusCode: 200, Body: reader, Header: make(http.Header)}, nil
					})
					hc := &http.Client{Transport: transport, Timeout: 30 * time.Millisecond}
					c := NewHTTP("http://mihari", "token", hc)
					var loads atomic.Int32
					if credential {
						c = NewHTTPWithCredentialProvider("http://mihari", timeoutCredentialFunc(func(context.Context) (string, error) { loads.Add(1); return "token", nil }), hc)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					done := make(chan error, 1)
					go func() { done <- invokeSubscriptionMutation(ctx, c, add) }()
					select {
					case <-started:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					if _, err := c.Core(ctx); err == nil {
						t.Error("ordinary request lost its timeout")
					}
					if err := <-done; err != nil {
						t.Errorf("subscription cut off by ordinary budget: %v", err)
					}
					if hc.Timeout != 30*time.Millisecond {
						t.Error("shared client was modified")
					}
					if credential && loads.Load() != 2 {
						t.Errorf("credential reads = %d", loads.Load())
					}
				})
			}
		}
	}
}

type timeoutResponseBody struct {
	io.Reader
	wait func() error
}

func (b *timeoutResponseBody) Read(p []byte) (int, error) {
	if err := b.wait(); err != nil {
		return 0, err
	}
	return b.Reader.Read(p)
}
func (*timeoutResponseBody) Close() error { return nil }

func TestSubscriptionTimeout_CredentialReadSharesBudget(t *testing.T) {
	for _, add := range []bool{false, true} {
		var calls int
		c := NewHTTPWithCredentialProvider("http://mihari", timeoutCredentialFunc(func(ctx context.Context) (string, error) {
			calls++
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 180*time.Second || time.Until(deadline) < 179*time.Second {
				t.Error("credential read does not have subscription deadline")
			}
			return "", context.Canceled
		}), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("credential failure dispatched request")
			return nil, context.Canceled
		})})
		if err := invokeSubscriptionMutation(context.Background(), c, add); err == nil {
			t.Error("credential error missing")
		}
		if calls != 1 {
			t.Errorf("credential reads = %d", calls)
		}
	}
}

func TestSubscriptionTimeout_ParentDeadlineAndCancellation(t *testing.T) {
	for _, add := range []bool{false, true} {
		for _, cancelExplicitly := range []bool{false, true} {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			c := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if cancelExplicitly {
					cancel()
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})})
			err := invokeSubscriptionMutation(ctx, c, add)
			if !errors.Is(err, ctx.Err()) || ctx.Err() == nil {
				t.Errorf("parent cancellation lost: %v", err)
			}
			cancel()
		}
	}
}
