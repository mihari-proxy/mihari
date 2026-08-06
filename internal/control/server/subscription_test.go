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
	added runtimeapi.AddSubscriptionInput
}

func (f *fakeSubscriptionRuntime) Subscriptions() subscription.PublicCatalog {
	return subscription.PublicCatalog{Profiles: []subscription.PublicProfile{}}
}
func (f *fakeSubscriptionRuntime) AddSubscription(_ context.Context, _ runtimeapi.Operation, input runtimeapi.AddSubscriptionInput) (subscription.PublicProfile, error) {
	f.added = input
	return subscription.PublicProfile{ID: "one", Name: input.Name}, nil
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
func (f *fakeSubscriptionRuntime) SetSubscription(context.Context, runtimeapi.Operation, string, runtimeapi.SetSubscriptionInput) (subscription.PublicProfile, error) {
	return subscription.PublicProfile{}, nil
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
