package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type fakeSubscriptionRuntime struct {
	*fakeRuntime
	added    runtimeapi.AddSubscriptionInput
	setInput runtimeapi.SetSubscriptionInput
}

func (f *fakeSubscriptionRuntime) Subscriptions() subscription.PublicCatalog {
	return subscription.PublicCatalog{Profiles: []subscription.PublicProfile{}}
}
func (f *fakeSubscriptionRuntime) AddSubscription(_ context.Context, _ runtimeapi.Operation, input runtimeapi.AddSubscriptionInput) (subscription.PublicProfile, error) {
	f.added = input
	return subscription.PublicProfile{ID: "one", Name: input.Name, ProxyMode: input.ProxyMode}, nil
}
func (f *fakeSubscriptionRuntime) RefreshSubscription(context.Context, runtimeapi.Operation, string) (subscription.PublicProfile, error) {
	return subscription.PublicProfile{}, nil
}
func (f *fakeSubscriptionRuntime) UseSubscription(context.Context, runtimeapi.Operation, string) (subscription.PublicProfile, error) {
	return subscription.PublicProfile{}, nil
}
func (f *fakeSubscriptionRuntime) SetSubscriptionEnabled(context.Context, runtimeapi.Operation, string, bool) (subscription.PublicProfile, error) {
	return subscription.PublicProfile{}, nil
}
func (f *fakeSubscriptionRuntime) SetSubscription(_ context.Context, _ runtimeapi.Operation, _ string, input runtimeapi.SetSubscriptionInput) (subscription.PublicProfile, error) {
	f.setInput = input
	profile := subscription.PublicProfile{ID: "one"}
	if input.ProxyMode != nil {
		profile.ProxyMode = *input.ProxyMode
	}
	return profile, nil
}
func (f *fakeSubscriptionRuntime) RemoveSubscription(context.Context, runtimeapi.Operation, string) error {
	return nil
}

func TestSubscriptionAddRouteAcceptsURLButDoesNotReturnIt(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}
	server := New(Options{Token: "token", Runtime: runtime})
	request := httptest.NewRequest(http.MethodPost, "/v1/subscriptions", strings.NewReader(`{"operation_id":"op","name":"main","url":"https://example.test/?token=secret"}`))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || runtime.added.URL == "" {
		t.Fatalf("status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), runtime.added)
	}
	if strings.Contains(recorder.Body.String(), "example.test") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("response leaked URL: %s", recorder.Body.String())
	}
	var result protocol.SubscriptionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Subscription.ID != "one" {
		t.Fatal(err)
	}
}

func TestSubscriptionAddRouteForwardsProxyMode(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}
	server := New(Options{Token: "token", Runtime: runtime})
	body := `{"operation_id":"op","name":"main","url":"https://example.test/sub","proxy_mode":"auto"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/subscriptions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if runtime.added.ProxyMode != "auto" {
		t.Fatalf("add did not forward proxy mode: %#v", runtime.added)
	}
	var result protocol.SubscriptionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Subscription.ProxyMode != "auto" {
		t.Fatalf("DTO missing proxy mode: %#v", result.Subscription)
	}
}

func TestSubscriptionUpdateRouteForwardsProxyMode(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}
	server := New(Options{Token: "token", Runtime: runtime})
	body := `{"operation_id":"op","proxy_mode":"proxy"}`
	request := httptest.NewRequest(http.MethodPatch, "/v1/subscriptions/one", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if runtime.setInput.ProxyMode == nil || *runtime.setInput.ProxyMode != "proxy" {
		t.Fatalf("update did not forward proxy mode: %#v", runtime.setInput)
	}
}
