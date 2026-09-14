package runtime

import (
	"bytes"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestSubscriptionEdit_URLPreservesActiveCache(t *testing.T) {
	m, service, controller, address := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "old-tag")
		w.Header().Set("Last-Modified", "Mon, 14 Sep 2026 00:00:00 GMT")
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	ctx := context.Background()
	a, err := m.AddSubscription(ctx, Operation{ID: "edit-add-a"}, AddSubscriptionInput{Name: "a", URL: address + "/a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddSubscription(ctx, Operation{ID: "edit-add-b"}, AddSubscriptionInput{Name: "b", URL: address + "/b"}); err != nil {
		t.Fatal(err)
	}
	before := service.Snapshot()
	previous := before.Profiles[before.Index(a.ID)]
	cached, err := os.ReadFile(service.CachePath(a.ID))
	if err != nil {
		t.Fatal(err)
	}
	controller.mu.Lock()
	reloads := controller.reloads
	controller.mu.Unlock()
	changed := address + "/new"
	start := time.Now().UTC()
	result, err := m.SetSubscription(ctx, Operation{ID: "edit-url"}, a.ID, SetSubscriptionInput{URL: &changed})
	if err != nil {
		t.Fatal(err)
	}
	after := service.Snapshot()
	p := after.Profiles[after.Index(a.ID)]
	if after.ActiveID != a.ID || p.Generation != previous.Generation || !p.UpdatedAt.Equal(previous.UpdatedAt) || !result.Cached {
		t.Fatal("URL edit discarded the active cache")
	}
	if p.CacheURL != previous.URL || !result.CacheOutdated || p.ETag != "" || p.LastModified != "" {
		t.Fatal("URL source or validators not separated")
	}
	if p.ScheduleFrom.Before(start) || p.ScheduleFrom.After(time.Now().UTC()) || p.IntervalRefreshRequired {
		t.Fatal("URL edit did not reset only the schedule")
	}
	actual, err := os.ReadFile(service.CachePath(a.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cached, actual) {
		t.Fatal("URL edit changed cache bytes")
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if reloads != controller.reloads {
		t.Fatal("URL edit reloaded the running configuration")
	}
}

func TestSubscriptionRefreshState_ApplyFailureKeepsOldCacheState(t *testing.T) {
	m, service, controller, address := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	ctx := context.Background()
	p, err := m.AddSubscription(ctx, Operation{ID: "apply-add"}, AddSubscriptionInput{Name: "a", URL: address})
	if err != nil {
		t.Fatal(err)
	}
	changedURL := address + "/new"
	interval := "2h"
	if _, err = m.SetSubscription(ctx, Operation{ID: "apply-edit"}, p.ID, SetSubscriptionInput{URL: &changedURL, Interval: &interval}); err != nil {
		t.Fatal(err)
	}
	before := service.Snapshot().Profiles[0]
	controller.mu.Lock()
	failures := 0
	controller.reload = func(context.Context) error {
		failures++
		if failures == 1 {
			return errors.New("apply refused")
		}
		return nil
	}
	controller.mu.Unlock()
	if _, err = m.RefreshSubscription(ctx, Operation{ID: "apply-refresh"}, p.ID); err == nil {
		t.Fatal("expected apply failure")
	}
	after := service.Snapshot().Profiles[0]
	if after.LastError == "" {
		t.Fatal("apply failure was not recorded on the subscription")
	}
	if after.CacheURL != before.CacheURL || after.Generation != before.Generation || !after.ScheduleFrom.Equal(before.ScheduleFrom) || !after.IntervalRefreshRequired || service.Snapshot().ActiveID != p.ID {
		t.Fatal("apply failure did not restore old cache state")
	}
}

func TestSubscriptionEdit_IntervalResetsSchedule(t *testing.T) {
	m, service, _, address := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	ctx := context.Background()
	p, err := m.AddSubscription(ctx, Operation{ID: "interval-add"}, AddSubscriptionInput{Name: "a", URL: address})
	if err != nil {
		t.Fatal(err)
	}
	interval := "6h"
	changed, err := m.SetSubscription(ctx, Operation{ID: "interval-edit"}, p.ID, SetSubscriptionInput{Interval: &interval})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.IntervalRefreshRequired || changed.ScheduleFrom.IsZero() || !changed.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatal("interval change did not invalidate cache age independently")
	}
	from := changed.ScheduleFrom
	name := "renamed"
	mode := subscription.ProxyModeAuto
	enabled := false
	same, err := m.SetSubscription(ctx, Operation{ID: "interval-same"}, p.ID, SetSubscriptionInput{Interval: &interval, Name: &name, ProxyMode: &mode, AutoRefresh: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if !same.ScheduleFrom.Equal(from) || !same.IntervalRefreshRequired {
		t.Fatal("unrelated edit changed schedule state")
	}
	_, _, err = service.Mutate(func(c *subscription.Catalog) error { c.Profiles[c.Index(p.ID)].LastError = "old failure"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	url := address + "/new"
	changed, err = m.SetSubscription(ctx, Operation{ID: "interval-new-url"}, p.ID, SetSubscriptionInput{URL: &url})
	if err != nil {
		t.Fatal(err)
	}
	if changed.LastError != "" || !changed.IntervalRefreshRequired {
		t.Fatal("new URL retained old failure or cleared interval flag")
	}
}

func TestSubscriptionURL_OnlyCurrentSourceAndSafeFailures(t *testing.T) {
	m, _, _, address := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	ctx := context.Background()
	p, err := m.AddSubscription(ctx, Operation{ID: "url-add"}, AddSubscriptionInput{Name: "Main", URL: address + "/old"})
	if err != nil {
		t.Fatal(err)
	}
	url := address + "/new"
	if _, err = m.SetSubscription(ctx, Operation{ID: "url-set"}, p.ID, SetSubscriptionInput{URL: &url}); err != nil {
		t.Fatal(err)
	}
	revealed, err := m.SubscriptionURL(ctx, p.ID)
	if err != nil || revealed != url {
		t.Fatal("reveal did not use current source")
	}
	_, err = m.SubscriptionURL(ctx, "missing")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
		t.Fatal("unknown ID classification")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = m.SubscriptionURL(canceled, p.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled reveal was not stopped")
	}
	var empty Manager
	if _, err = empty.SubscriptionURL(ctx, p.ID); err == nil {
		t.Fatal("missing manager accepted reveal")
	}
}
