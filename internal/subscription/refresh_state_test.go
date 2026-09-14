package subscription

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
)

type stateFetcher func(context.Context, FetchRequest) (FetchResult, error)

func (f stateFetcher) Fetch(ctx context.Context, r FetchRequest) (FetchResult, error) {
	return f(ctx, r)
}

func TestSubscriptionRefreshState_StaleFailureAfterURLEdit(t *testing.T) {
	s, address := newServiceForTest(t, http.NotFoundHandler())
	p, err := s.Add("primary", address, "")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	s.downloader = stateFetcher(func(ctx context.Context, _ FetchRequest) (FetchResult, error) {
		close(started)
		select {
		case <-release:
			return FetchResult{}, errors.New("old source failed")
		case <-ctx.Done():
			return FetchResult{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.PrepareRefresh(ctx, p.ID); done <- err }()
	<-started
	_, _, err = s.Mutate(func(c *Catalog) error { c.Profiles[0].URL = address + "/new"; c.Profiles[0].Version++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("expected old fetch failure")
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("old source error wrote into new subscription state")
	}
}

func TestSubscriptionRefreshState_CommitAndRollback(t *testing.T) {
	s, address := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	p, err := s.Add("primary", address, "")
	if err != nil {
		t.Fatal(err)
	}
	prep, err := s.PrepareRefresh(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitRefresh(prep); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Mutate(func(c *Catalog) error {
		p := &c.Profiles[0]
		p.URL = address + "/new"
		p.ScheduleFrom = time.Unix(450, 0).UTC()
		p.IntervalRefreshRequired = true
		p.Version++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	prep, err = s.PrepareRefresh(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.CommitRefresh(prep)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot().Profiles[0]
	if got.CacheURL != got.URL || !got.ScheduleFrom.IsZero() || got.IntervalRefreshRequired {
		t.Fatal("successful refresh did not settle cache state")
	}
	if err = s.Rollback(receipt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("rollback did not restore cache state")
	}
}

func TestSubscriptionRefreshState_Rejects304FromChangedSource(t *testing.T) {
	s, address := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	p, err := s.Add("primary", address, "")
	if err != nil {
		t.Fatal(err)
	}
	prep, err := s.PrepareRefresh(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitRefresh(prep); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Mutate(func(c *Catalog) error { c.Profiles[0].URL = address + "/new"; c.Profiles[0].Version++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	s.downloader = stateFetcher(func(context.Context, FetchRequest) (FetchResult, error) { return FetchResult{NotModified: true}, nil })
	if _, err = s.PrepareRefresh(context.Background(), p.ID); err == nil {
		t.Fatal("changed source accepted the old cache as not-modified")
	}
}

func TestSubscriptionRefreshState_Valid304ClearsIntervalExpiry(t *testing.T) {
	s, address := newServiceForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	p, err := s.Add("primary", address, "")
	if err != nil {
		t.Fatal(err)
	}
	prep, err := s.PrepareRefresh(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitRefresh(prep); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Mutate(func(c *Catalog) error {
		p := &c.Profiles[0]
		p.IntervalRefreshRequired = true
		p.ScheduleFrom = time.Now().UTC()
		p.Version++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot().Profiles[0]
	s.downloader = stateFetcher(func(context.Context, FetchRequest) (FetchResult, error) { return FetchResult{NotModified: true}, nil })
	prep, err = s.PrepareRefresh(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitRefresh(prep); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot().Profiles[0]
	if after.Generation != before.Generation || after.IntervalRefreshRequired || !after.ScheduleFrom.IsZero() || after.CacheURL != after.URL {
		t.Fatal("304 did not settle expiry without replacing generation")
	}
}
