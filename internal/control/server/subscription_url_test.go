package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type urlSubscriptionRuntime struct {
	*fakeSubscriptionRuntime
	reads int
}

func (f *urlSubscriptionRuntime) SubscriptionURL(ctx context.Context, id string) (string, error) {
	f.reads++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if id != "one" {
		return "", protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "subscription not found"}
	}
	return "https://fixture.test/sub?token=private", nil
}

func TestSubscriptionURL_AuthenticatedRead(t *testing.T) {
	f := &urlSubscriptionRuntime{fakeSubscriptionRuntime: &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}, catalog: subscription.PublicCatalog{Profiles: []subscription.PublicProfile{{ID: "one", Name: "primary", CacheOutdated: true, IntervalRefreshRequired: true, ScheduleFrom: time.Unix(100, 0).UTC()}}}}}
	s := New(Options{Token: "token", Runtime: f})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/subscriptions/one/url", nil))
	var response struct{ Schema, URL string }
	if w.Code != 200 {
		t.Fatalf("reveal status=%d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Schema != "mihari/v1" || response.URL != "https://fixture.test/sub?token=private" {
		t.Fatal("incorrect reveal response")
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/subscriptions/one/url", nil))
	if w.Code != http.StatusUnauthorized || f.reads != 1 {
		t.Fatal("unauthenticated caller read URL")
	}
	for _, path := range []string{"/v1/subscriptions", "/v1/subscriptions/one"} {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), `"url"`) {
			t.Fatal("normal response leaked URL or failed")
		}
		if !strings.Contains(w.Body.String(), `"cache_outdated":true`) || !strings.Contains(w.Body.String(), `"interval_refresh_required":true`) {
			t.Fatal("public cache state was not projected")
		}
	}
}

type trackedSubscriptionRuntime struct {
	*fakeSubscriptionRuntime
	started, release chan struct{}
}

func (f *trackedSubscriptionRuntime) SetSubscription(ctx context.Context, op runtimeapi.Operation, id string, input runtimeapi.SetSubscriptionInput) (subscription.PublicProfile, error) {
	close(f.started)
	select {
	case <-f.release:
		return subscription.PublicProfile{ID: id}, nil
	case <-ctx.Done():
		return subscription.PublicProfile{}, ctx.Err()
	}
}

func TestSubscriptionSaveTracking_PatchSettlement(t *testing.T) {
	f := &trackedSubscriptionRuntime{fakeSubscriptionRuntime: &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}, started: make(chan struct{}), release: make(chan struct{})}
	s := New(Options{Token: "token", Runtime: f})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(httptest.NewRecorder(), authorizedRequest(http.MethodPatch, "/v1/subscriptions/one", strings.NewReader(`{"operation_id":"patch-track","name":"new"}`)))
	}()
	<-f.started
	state := readOperationState(t, s, "patch-track")
	close(f.release)
	<-done
	if state != "running" {
		t.Fatalf("PATCH state=%s, want running", state)
	}
	if readOperationState(t, s, "patch-track") != "finished" {
		t.Fatal("PATCH did not settle")
	}
}

func TestSubscriptionURL_UnknownAndUnavailableRemainSafe(t *testing.T) {
	for _, runtime := range []RuntimeAPI{&fakeRuntime{}, &urlSubscriptionRuntime{fakeSubscriptionRuntime: &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}}}} {
		s := New(Options{Token: "token", Runtime: runtime})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/subscriptions/missing/url", nil))
		if w.Code < 400 || strings.Contains(w.Body.String(), "private") {
			t.Fatal("invalid reveal was not safely rejected")
		}
	}
}
