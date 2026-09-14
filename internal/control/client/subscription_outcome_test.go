package client

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionSaveOutcome_DecodeLossIsUnknown(t *testing.T) {
	for _, remote := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if remote {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"schema":"mihari/v1","error":{"code":"data_failure","message":"rejected"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"schema":"mihari/v1","subscription":`))
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
		_, _ = w.Write([]byte(`{"schema":"mihari/v1","url":"https://fixture.test/sub"}`))
	}))
	defer s.Close()
	c := NewHTTP(s.URL, "token", s.Client())
	result, err := c.SubscriptionURL(context.Background(), "a/b")
	if err != nil || result.Schema != "mihari/v1" || result.URL != "https://fixture.test/sub" {
		t.Fatal("URL response was not decoded")
	}
}
