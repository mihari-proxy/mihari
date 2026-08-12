package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type fakeSubscriptionRuntime struct {
	*fakeRuntime
	catalog   subscription.PublicCatalog
	operation runtimeapi.Operation
	profileID string
	enabled   bool
	err       error
	added     runtimeapi.AddSubscriptionInput
	setInput  runtimeapi.SetSubscriptionInput
}

func (f *fakeSubscriptionRuntime) Subscriptions() subscription.PublicCatalog {
	return f.catalog
}

func (f *fakeSubscriptionRuntime) AddSubscription(_ context.Context, operation runtimeapi.Operation, input runtimeapi.AddSubscriptionInput) (subscription.PublicProfile, error) {
	f.operation = operation
	f.added = input
	return subscription.PublicProfile{ID: "one", Name: input.Name, ProxyMode: input.ProxyMode}, f.err
}

func (f *fakeSubscriptionRuntime) RefreshSubscription(_ context.Context, operation runtimeapi.Operation, profileID string) (subscription.PublicProfile, error) {
	f.operation = operation
	f.profileID = profileID
	return f.mutatedProfile(), f.err
}

func (f *fakeSubscriptionRuntime) UseSubscription(_ context.Context, operation runtimeapi.Operation, profileID string) (subscription.PublicProfile, error) {
	f.operation = operation
	f.profileID = profileID
	return f.mutatedProfile(), f.err
}

func (f *fakeSubscriptionRuntime) SetSubscriptionEnabled(_ context.Context, operation runtimeapi.Operation, profileID string, enabled bool) (subscription.PublicProfile, error) {
	f.operation = operation
	f.profileID = profileID
	f.enabled = enabled
	profile := f.mutatedProfile()
	profile.Enabled = enabled
	return profile, f.err
}

func (f *fakeSubscriptionRuntime) SetSubscription(_ context.Context, operation runtimeapi.Operation, profileID string, input runtimeapi.SetSubscriptionInput) (subscription.PublicProfile, error) {
	f.operation = operation
	f.profileID = profileID
	f.setInput = input
	profile := subscription.PublicProfile{ID: profileID}
	if input.Name != nil {
		profile.Name = *input.Name
	}
	if input.Interval != nil {
		profile.Interval = *input.Interval
	}
	if input.AutoRefresh != nil {
		profile.AutoRefresh = *input.AutoRefresh
	}
	if input.ProxyMode != nil {
		profile.ProxyMode = *input.ProxyMode
	}
	return profile, f.err
}

func (f *fakeSubscriptionRuntime) RemoveSubscription(_ context.Context, operation runtimeapi.Operation, profileID string) error {
	f.operation = operation
	f.profileID = profileID
	return f.err
}

func (f *fakeSubscriptionRuntime) mutatedProfile() subscription.PublicProfile {
	return subscription.PublicProfile{ID: f.profileID, Name: "updated", Enabled: f.enabled, ProxyMode: subscription.ProxyModeAuto}
}

func TestSubscriptionAddRouteAcceptsURLButDoesNotReturnIt(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Revision: 9}}}
	server := New(Options{Token: "token", Runtime: runtime})
	request := httptest.NewRequest(http.MethodPost, "/v1/subscriptions", strings.NewReader(`{"operation_id":"add-url-1","if_revision":7,"name":"main","url":"https://example.test/?token=secret"}`))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || runtime.added.Name != "main" || runtime.added.URL != "https://example.test/?token=secret" {
		t.Fatalf("status=%d body=%s input=%#v", recorder.Code, recorder.Body.String(), runtime.added)
	}
	if runtime.operation.ID != "add-url-1" || runtime.operation.Source != "control" || runtime.operation.IfRevision == nil || *runtime.operation.IfRevision != 7 {
		t.Fatalf("operation=%#v", runtime.operation)
	}
	if strings.Contains(recorder.Body.String(), "example.test") || strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("response leaked URL: %s", recorder.Body.String())
	}
	var result protocol.SubscriptionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "mihari/v1" || result.OperationID != "add-url-1" || result.Revision != 9 || result.Subscription.ID != "one" || result.Subscription.Name != "main" {
		t.Fatalf("result=%#v", result)
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

func TestSubscriptionUpdateRouteForwardsAllFieldsAndOperation(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Revision: 21}}}
	server := New(Options{Token: "token", Runtime: runtime})
	body := `{"operation_id":"update-1","if_revision":20,"name":"renamed","url":"https://example.test/new?token=private","interval":"","auto_refresh":false,"global_interval":"24h","proxy_mode":"proxy"}`
	request := httptest.NewRequest(http.MethodPatch, "/v1/subscriptions/one", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if runtime.operation.ID != "update-1" || runtime.operation.Source != "control" || runtime.operation.IfRevision == nil || *runtime.operation.IfRevision != 20 || runtime.profileID != "one" {
		t.Fatalf("operation=%#v profileID=%q", runtime.operation, runtime.profileID)
	}
	input := runtime.setInput
	if input.Name == nil || *input.Name != "renamed" ||
		input.URL == nil || *input.URL != "https://example.test/new?token=private" ||
		input.Interval == nil || *input.Interval != "" ||
		input.AutoRefresh == nil || *input.AutoRefresh ||
		input.GlobalPeriod == nil || *input.GlobalPeriod != "24h" ||
		input.ProxyMode == nil || *input.ProxyMode != "proxy" {
		t.Fatalf("update input=%#v", input)
	}
	if strings.Contains(recorder.Body.String(), "example.test") || strings.Contains(recorder.Body.String(), "private") {
		t.Fatalf("response leaked URL: %s", recorder.Body.String())
	}
	var result protocol.SubscriptionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "mihari/v1" || result.OperationID != "update-1" || result.Revision != 21 ||
		result.Subscription.ID != "one" || result.Subscription.Name != "renamed" || result.Subscription.Interval != "" ||
		result.Subscription.AutoRefresh || result.Subscription.ProxyMode != "proxy" {
		t.Fatalf("result=%#v", result)
	}
}

func TestSubscriptionListAndShowRoutesReturnSecretFreeDTOs(t *testing.T) {
	updatedAt := time.Date(2026, time.August, 12, 9, 30, 0, 0, time.UTC)
	runtime := &fakeSubscriptionRuntime{
		fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Revision: 8}},
		catalog: subscription.PublicCatalog{
			ActiveID:       "one",
			GlobalInterval: "12h",
			Profiles: []subscription.PublicProfile{{
				ID: "one", Name: "primary", Enabled: true, AutoRefresh: true, Interval: "6h", Cached: true,
				Generation: 3, UpdatedAt: updatedAt, LastError: "temporary upstream error", Upload: 11, Download: 22, Total: 33, Expire: 44,
				ProxyMode: subscription.ProxyModeAuto,
			}},
		},
	}
	server := New(Options{Token: "token", Runtime: runtime})

	list := httptest.NewRecorder()
	server.Handler().ServeHTTP(list, authorizedRequest(http.MethodGet, "/v1/subscriptions", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var rawList map[string]json.RawMessage
	if err := json.Unmarshal(list.Body.Bytes(), &rawList); err != nil {
		t.Fatal(err)
	}
	var rawSubscriptions []json.RawMessage
	if err := json.Unmarshal(rawList["subscriptions"], &rawSubscriptions); err != nil || len(rawSubscriptions) != 1 {
		t.Fatalf("raw subscriptions=%q err=%v", rawList["subscriptions"], err)
	}
	assertSubscriptionDTOHasNoSourceURL(t, rawSubscriptions[0])
	var catalog protocol.SubscriptionList
	if err := json.Unmarshal(list.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Schema != "mihari/v1" || catalog.Revision != 8 || catalog.ActiveID != "one" || catalog.GlobalInterval != "12h" || len(catalog.Subscriptions) != 1 {
		t.Fatalf("list=%#v", catalog)
	}
	profile := catalog.Subscriptions[0]
	if profile.ID != "one" || profile.Name != "primary" || !profile.Enabled || !profile.AutoRefresh || profile.Interval != "6h" || !profile.Cached || profile.Generation != 3 || !profile.UpdatedAt.Equal(updatedAt) || profile.LastError != "temporary upstream error" || profile.Upload != 11 || profile.Download != 22 || profile.Total != 33 || profile.Expire != 44 || profile.ProxyMode != subscription.ProxyModeAuto {
		t.Fatalf("list profile=%#v", profile)
	}

	show := httptest.NewRecorder()
	server.Handler().ServeHTTP(show, authorizedRequest(http.MethodGet, "/v1/subscriptions/one", nil))
	if show.Code != http.StatusOK {
		t.Fatalf("show status=%d body=%s", show.Code, show.Body.String())
	}
	var rawShow map[string]json.RawMessage
	if err := json.Unmarshal(show.Body.Bytes(), &rawShow); err != nil {
		t.Fatal(err)
	}
	assertSubscriptionDTOHasNoSourceURL(t, rawShow["subscription"])
	var result protocol.SubscriptionResult
	if err := json.Unmarshal(show.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schema != "mihari/v1" || result.Revision != 8 || result.OperationID != "" || result.Subscription != profile {
		t.Fatalf("show=%#v want profile=%#v", result, profile)
	}
}

func assertSubscriptionDTOHasNoSourceURL(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("subscription JSON=%q err=%v", raw, err)
	}
	for _, field := range []string{"url", "source_url", "subscription_url"} {
		if _, found := object[field]; found {
			t.Fatalf("subscription DTO leaked forbidden %q field: %s", field, raw)
		}
	}
}

func TestSubscriptionShowRouteMapsNotFound(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}, catalog: subscription.PublicCatalog{Profiles: []subscription.PublicProfile{}}}
	server := New(Options{Token: "token", Runtime: runtime})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/v1/subscriptions/missing", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodeInvalidArgument {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
}

func TestSubscriptionProfileMutationRoutesForwardOperation(t *testing.T) {
	tests := []struct {
		name, method, path, body string
		operationID              string
		wantEnabled              *bool
	}{
		{name: "refresh", method: http.MethodPost, path: "/v1/subscriptions/one/refresh", body: `{"operation_id":"refresh-1","if_revision":7}`, operationID: "refresh-1"},
		{name: "use", method: http.MethodPut, path: "/v1/subscriptions/one/active", body: `{"operation_id":"use-1","if_revision":7}`, operationID: "use-1"},
		{name: "enable false", method: http.MethodPut, path: "/v1/subscriptions/one/enabled", body: `{"operation_id":"enable-1","if_revision":7,"enabled":false}`, operationID: "enable-1", wantEnabled: boolPtr(false)},
		{name: "remove", method: http.MethodDelete, path: "/v1/subscriptions/one", body: `{"operation_id":"remove-1","if_revision":7}`, operationID: "remove-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{snapshot: state.Snapshot{Revision: 8}}}
			server := New(Options{Token: "token", Runtime: runtime})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, authorizedRequest(test.method, test.path, strings.NewReader(test.body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var operationID string
			var subscription protocol.Subscription
			if test.name == "remove" {
				var result protocol.MutationResult
				if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Schema != "mihari/v1" || result.Revision != 8 {
					t.Fatalf("result=%#v err=%v", result, err)
				}
				operationID = result.OperationID
			} else {
				var result protocol.SubscriptionResult
				if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Schema != "mihari/v1" || result.Revision != 8 || result.Subscription.ID != "one" {
					t.Fatalf("result=%#v err=%v", result, err)
				}
				operationID = result.OperationID
				subscription = result.Subscription
			}
			if operationID != test.operationID || runtime.operation.ID != test.operationID || runtime.operation.Source != "control" || runtime.operation.IfRevision == nil || *runtime.operation.IfRevision != 7 || runtime.profileID != "one" {
				t.Fatalf("response operation=%q runtime operation=%#v profileID=%q", operationID, runtime.operation, runtime.profileID)
			}
			if test.wantEnabled != nil && (runtime.enabled != *test.wantEnabled || subscription.Enabled != *test.wantEnabled) {
				t.Fatalf("runtime enabled=%v response enabled=%v want %v", runtime.enabled, subscription.Enabled, *test.wantEnabled)
			}
		})
	}
}

func TestSubscriptionRoutesRejectInvalidBodiesAndUnavailableRuntime(t *testing.T) {
	invalidBodies := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"operation_id":"one","unexpected":true}`},
		{name: "multiple JSON objects", body: `{"operation_id":"one"}{"operation_id":"two"}`},
		{name: "missing operation ID", body: `{}`},
		{name: "oversized body", body: `{"operation_id":"one","padding":"` + strings.Repeat("x", maxControlBodySize) + `"}`},
	}
	for _, test := range invalidBodies {
		t.Run(test.name, func(t *testing.T) {
			server := New(Options{Token: "token", Runtime: &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/v1/subscriptions/one/refresh", strings.NewReader(test.body)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var envelope protocol.ErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodeInvalidArgument {
				t.Fatalf("envelope=%#v err=%v", envelope, err)
			}
		})
	}

	t.Run("runtime capability unavailable", func(t *testing.T) {
		server := New(Options{Token: "token", Runtime: strippedRuntime{&fakeRuntime{}}})
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/v1/subscriptions", nil))
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope protocol.ErrorEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodeInvalidState {
			t.Fatalf("envelope=%#v err=%v", envelope, err)
		}
	})

	t.Run("runtime API error", func(t *testing.T) {
		runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}, err: protocol.APIError{Code: protocol.CodePermissionDenied, Message: "subscription access denied"}}
		server := New(Options{Token: "token", Runtime: runtime})
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/v1/subscriptions/one/refresh", bytes.NewBufferString(`{"operation_id":"one"}`)))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var envelope protocol.ErrorEnvelope
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodePermissionDenied || envelope.Error.Message != "subscription access denied" {
			t.Fatalf("envelope=%#v err=%v", envelope, err)
		}
	})
}

func boolPtr(value bool) *bool { return &value }
