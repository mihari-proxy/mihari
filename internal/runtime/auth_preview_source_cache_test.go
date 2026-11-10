package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/subscription"
)

func TestAuthPreviewSourceCache_ReusesOnlyCurrentSourceGeneration(t *testing.T) {
	var cache authPreviewSourceCache
	reads := 0
	reader := func() (subscription.Document, error) {
		reads++
		return subscription.Document{"authentication": []any{"synthetic:secret"}, "skip-auth-prefixes": []any{}, "proxies": []any{"unrelated"}, "secret": "unrelated-secret"}, nil
	}
	key := authPreviewSourceKey{id: "synthetic", generation: 1}
	for range 3 {
		doc, err := cache.load(t.Context(), key, reader)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc) != 2 || doc["authentication"] == nil {
			t.Fatal("cache retained unrelated configuration instead of only authentication fields")
		}
	}
	if reads != 1 {
		t.Fatalf("same source parsed %d times", reads)
	}
	key.generation++
	if _, err := cache.load(t.Context(), key, reader); err != nil {
		t.Fatal(err)
	}
	key.id = "another"
	if _, err := cache.load(t.Context(), key, reader); err != nil {
		t.Fatal(err)
	}
	if reads != 3 {
		t.Fatal("source identity/generation changes did not invalidate cached authentication")
	}
}

func TestAuthPreviewSourceCache_ErrorIsRetriedAndCanceledWaiterDoesNotOwnLoad(t *testing.T) {
	var cache authPreviewSourceCache
	key := authPreviewSourceKey{id: "fixture", generation: 1}
	sentinel := errors.New("synthetic source failure")
	if _, err := cache.load(t.Context(), key, func() (subscription.Document, error) { return nil, sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	done := make(chan struct{})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); <-done })
	var reads atomic.Int32
	reader := func() (subscription.Document, error) {
		reads.Add(1)
		close(started)
		<-release
		return subscription.Document{"authentication": []any{"fixture:synthetic"}}, nil
	}
	go func() { defer close(done); _, err := cache.load(t.Context(), key, reader); finished <- err }()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.load(ctx, key, reader); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled waiter did not return promptly")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := cache.load(t.Context(), key, func() (subscription.Document, error) { reads.Add(1); return nil, sentinel }); err != nil {
		t.Fatal("successful source was not retained", err)
	}
	if reads.Load() != 1 {
		t.Fatal("source parsed again after cancellation of another request")
	}
}

func TestAuthPreviewSourceCache_LateOldGenerationCannotReplaceNewSnapshot(t *testing.T) {
	var cache authPreviewSourceCache
	key := authPreviewSourceKey{id: "synthetic", generation: 1}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	done := make(chan struct{})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); <-done })
	go func() {
		defer close(done)
		_, err := cache.load(t.Context(), key, func() (subscription.Document, error) {
			close(started)
			<-release
			return subscription.Document{"authentication": "old"}, nil
		})
		finished <- err
	}()
	<-started
	next := key
	next.generation++
	if _, err := cache.load(t.Context(), next, func() (subscription.Document, error) { return subscription.Document{"authentication": "new"}, nil }); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	doc, err := cache.load(t.Context(), next, func() (subscription.Document, error) {
		t.Error("late old load evicted the current snapshot")
		return nil, errors.New("unexpected reparse")
	})
	if err != nil || doc["authentication"] != "new" {
		t.Fatal("new source snapshot was overwritten", err)
	}
}
