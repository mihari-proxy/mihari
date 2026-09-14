package client

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionSaveOutcome_DecodeLossIsUnknown(t *testing.T) {
	for _, remote := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if remote {
				w.WriteHeader(400)
				if _, err := w.Write([]byte(`{"schema":"mihari/v1","error":{"code":"data_failure","message":"rejected"}}`)); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := w.Write([]byte(`{"schema":"mihari/v1","subscription":`)); err != nil {
				t.Error(err)
			}
		}))
		c := NewHTTP(s.URL, "token", s.Client())
		_, err := c.UpdateSubscription(context.Background(), "a", protocol.SubscriptionUpdateRequest{OperationID: "save"})
		var unknown interface{ OutcomeUnknown() bool }
		got := errors.As(err, &unknown) && unknown.OutcomeUnknown()
		s.Close()
		if got == remote {
			t.Fatalf("remote=%t unknown=%t err=%v", remote, got, err)
		}
	}
}

func TestSubscriptionURL_ReadUsesAuthenticationAndEscapedID(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer token" || r.URL.EscapedPath() != "/v1/subscriptions/a%2Fb/url" {
			t.Error("incorrect reveal request")
		}
		if _, err := w.Write([]byte(`{"schema":"mihari/v1","url":"https://fixture.test/sub"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	c := NewHTTP(s.URL, "token", s.Client())
	result, err := c.SubscriptionURL(context.Background(), "a/b")
	if err != nil || result.Schema != "mihari/v1" || result.URL != "https://fixture.test/sub" {
		t.Fatal("URL response was not decoded")
	}
}

func TestSubscriptionSaveOutcome_PreDispatchIsDefinite(t *testing.T) {
	for _, stage := range []string{"marshal", "request", "credential"} {
		t.Run(stage, func(t *testing.T) {
			c := NewHTTPWithCredentialProvider("http://mihari", &sequenceProvider{value: "token"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unsent request reached transport")
				return nil, errors.New("unexpected dispatch")
			})})
			var input any = protocol.SubscriptionUpdateRequest{OperationID: "unsent"}
			if stage == "marshal" {
				input = make(chan int)
			}
			if stage == "request" {
				c.baseURL = "://invalid"
			}
			if stage == "credential" {
				c.provider = &sequenceProvider{err: context.DeadlineExceeded}
			}
			err := c.doMutation(context.Background(), logging.OperationMetadata{Name: "subscription.set"}, http.MethodPatch, "/v1/subscriptions/a", input, new(protocol.SubscriptionResult))
			var outcome interface{ OutcomeUnknown() bool }
			if err == nil || !errors.As(err, &outcome) || outcome.OutcomeUnknown() {
				t.Fatal("pre-dispatch failure was not explicitly definite")
			}
		})
	}
}
