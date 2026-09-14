package integration

import (
	"context"
	"encoding/json"
	"errors"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type editController struct {
	stubMihomoController
	reloads atomic.Int64
}

func (c *editController) Reload(context.Context, string, bool) error { c.reloads.Add(1); return nil }

func TestSubscriptionEdit_IPCPreservesCacheAndRestartState(t *testing.T) {
	controller := &editController{}
	f := newSubscriptionControlFixture(t, controller)
	f.fetcher.releaseFetch()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	refreshed, err := f.client.RefreshSubscription(ctx, f.profileID, protocol.MutationRequest{OperationID: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.client.UseSubscription(ctx, f.profileID, protocol.MutationRequest{OperationID: "use"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.client.Subscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	url, interval := "https://fixture.test/new?token=private-source", "6h"
	beforeReloads := controller.reloads.Load()
	updated, err := f.client.UpdateSubscription(ctx, f.profileID, protocol.SubscriptionUpdateRequest{OperationID: "edit", IfRevision: &current.Revision, URL: &url, Interval: &interval})
	if err != nil {
		t.Fatal(err)
	}
	p := updated.Subscription
	if controller.reloads.Load() != beforeReloads {
		t.Fatal("URL edit reloaded core")
	}
	if !p.Cached || !p.CacheOutdated || !p.IntervalRefreshRequired || p.ScheduleFrom.IsZero() || p.Generation != refreshed.Subscription.Generation || !p.UpdatedAt.Equal(refreshed.Subscription.UpdatedAt) {
		t.Fatal("edit did not retain cache and reset schedule")
	}
	current, err = f.client.Subscriptions(ctx)
	if err != nil || current.ActiveID != f.profileID {
		t.Fatal("active selection lost")
	}
	raw, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-source") || strings.Contains(string(raw), "https://") {
		t.Fatal("ordinary IPC leaked URL")
	}
	revealed, err := f.client.SubscriptionURL(ctx, f.profileID)
	if err != nil || revealed.URL != url {
		t.Fatal("authenticated reveal did not return current URL")
	}
	_, err = controlclient.New(f.endpoint, "wrong-token").SubscriptionURL(ctx, f.profileID)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodePermissionDenied {
		t.Fatal("unauthenticated reveal was not denied")
	}
	status, err := f.client.OperationStatus(ctx, "edit")
	if err != nil || status.State != "finished" {
		t.Fatal("PATCH settlement was not tracked")
	}
	if err = f.stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := subscription.Open(subscription.ServiceOptions{CatalogPath: f.catalogPath, CacheDir: f.cacheDir, Downloader: f.fetcher})
	if err != nil {
		t.Fatal(err)
	}
	c := reopened.Snapshot()
	pub := c.Public().Profiles[0]
	if c.ActiveID != f.profileID || !pub.CacheOutdated || !pub.IntervalRefreshRequired || !pub.ScheduleFrom.Equal(updated.Subscription.ScheduleFrom) {
		t.Fatal("restart lost cache identity or forced expiry")
	}
	prepared, err := reopened.PrepareRefresh(ctx, f.profileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.CommitRefresh(prepared); err != nil {
		t.Fatal(err)
	}
	pub = reopened.Snapshot().Public().Profiles[0]
	if pub.CacheOutdated || pub.IntervalRefreshRequired || !pub.ScheduleFrom.IsZero() {
		t.Fatal("successful refresh did not settle state")
	}
}

func TestSubscriptionEdit_IPCStaleFetchCannotWriteBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			f := newSubscriptionControlFixture(t)
			if fail {
				f.fetcher.failure = errors.New("old fetch failed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := f.client.RefreshSubscription(ctx, f.profileID, protocol.MutationRequest{OperationID: "old-fetch"})
				done <- err
			}()
			select {
			case <-f.fetcher.entered:
			case <-ctx.Done():
				t.Fatal("fetch did not begin")
			}
			url := "https://fixture.test/new"
			_, err := f.client.UpdateSubscription(ctx, f.profileID, protocol.SubscriptionUpdateRequest{OperationID: "new-url", URL: &url})
			if err != nil {
				t.Fatal(err)
			}
			f.fetcher.releaseFetch()
			select {
			case err = <-done:
				if err == nil {
					t.Fatal("stale fetch succeeded")
				}
			case <-ctx.Done():
				t.Fatal("fetch did not settle")
			}
			got, err := f.client.Subscription(ctx, f.profileID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Subscription.Cached || got.Subscription.LastError != "" || got.Subscription.ScheduleFrom.IsZero() {
				t.Fatal("stale fetch changed edited profile")
			}
		})
	}
}

func TestSubscriptionEdit_LostResponseObservationNeverReplaysPatch(t *testing.T) {
	f := newSubscriptionControlFixture(t)
	handler := controlserver.New(controlserver.Options{Token: "token", Runtime: f.manager}).Handler()
	var writes atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			handler.ServeHTTP(w, r)
			return
		}
		writes.Add(1)
		recorded := httptest.NewRecorder()
		handler.ServeHTTP(recorded, r)
		if recorded.Code != http.StatusOK {
			t.Errorf("PATCH failed before response loss: %d", recorded.Code)
		}
		// The mutation has committed, but the client sees a truncated success body.
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"schema":"mihari/v1"`)); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	client := controlclient.NewHTTP(s.URL, "token", s.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	name := "Updated"
	_, err := client.UpdateSubscription(ctx, f.profileID, protocol.SubscriptionUpdateRequest{OperationID: "lost-patch", Name: &name})
	var unknown interface{ OutcomeUnknown() bool }
	if !errors.As(err, &unknown) || !unknown.OutcomeUnknown() {
		t.Fatal("lost success was classified as a definite failure")
	}
	revision := f.manager.Snapshot().Revision
	for i := 0; i < 2; i++ {
		state, err := client.OperationStatus(ctx, "lost-patch")
		if err != nil || state.State != "finished" {
			t.Fatal("settlement query failed")
		}
		current, err := client.Subscription(ctx, f.profileID)
		if err != nil || current.Subscription.Name != name {
			t.Fatal("committed state could not be observed")
		}
	}
	if writes.Load() != 1 || f.manager.Snapshot().Revision != revision {
		t.Fatal("observation replayed a mutation")
	}
}

func TestSubscriptionEdit_ZeroScheduleOmittedFromPublicJSON(t *testing.T) {
	for _, value := range []any{protocol.Subscription{}, subscription.PublicProfile{}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["schedule_from"]; exists {
			t.Fatal("zero schedule was serialized")
		}
	}
}
