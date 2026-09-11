package core_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type changingSubscriptionFetcher struct{ content []byte }

// Fetch returns a copy of the subscription document selected by the test.
func (f *changingSubscriptionFetcher) Fetch(context.Context, subscription.FetchRequest) (subscription.FetchResult, error) {
	return subscription.FetchResult{Content: append([]byte(nil), f.content...)}, nil
}

// TestRootManager_SubscriptionReloadOutsideCoreHome verifies startup-bound reload,
// restoration of rejected configuration changes, and a subsequent refresh.
func TestRootManager_SubscriptionReloadOutsideCoreHome(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "refresh"
		if reject {
			name = "rejected refresh restores previous config and permits retry"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			fetcher := &changingSubscriptionFetcher{content: []byte("proxies: []\nrules: ['MATCH,DIRECT']\n")}
			m, f, controller, _ := seamManager(t, func(o *runtimeapi.Options) {
				var err error
				o.Subscriptions, err = subscription.Open(subscription.ServiceOptions{
					CatalogPath: filepath.Join(t.TempDir(), "catalog.yaml"), CacheDir: t.TempDir(), Downloader: fetcher,
				})
				if err != nil {
					t.Fatal(err)
				}
			})
			profile, err := m.AddSubscription(ctx, runtimeapi.Operation{ID: "add", Source: "test"}, runtimeapi.AddSubscriptionInput{Name: "test", URL: "https://subscription.invalid/config"})
			if err != nil {
				t.Fatal(err)
			}
			if !profile.Cached || profile.Generation != 1 {
				t.Fatalf("first fetch did not commit: cached=%v generation=%d", profile.Cached, profile.Generation)
			}
			before := f.Content()
			fetcher.content = []byte("proxies: []\nrules: ['MATCH,REJECT']\n")
			if reject {
				controller.failReloads = controller.reloads + 1
			}
			updated, err := m.RefreshSubscription(ctx, runtimeapi.Operation{ID: "refresh", Source: "test"}, profile.ID)
			if reject {
				assertCode(t, err, protocol.CodeUpstreamFailure)
				if !bytes.Equal(before, f.Content()) || m.Subscriptions().Profiles[0].Generation != profile.Generation || controller.reloads != 3 {
					t.Fatal("rejected refresh changed the committed config or cache generation")
				}
				updated, err = m.RefreshSubscription(ctx, runtimeapi.Operation{ID: "retry", Source: "test"}, profile.ID)
			}
			if err != nil {
				t.Fatal("refresh requires an unexpected daemon restart", err)
			}
			if !updated.Cached || updated.Generation != profile.Generation+1 {
				t.Fatal("refresh did not publish the next cache generation")
			}
			if bytes.Equal(before, f.Content()) || !bytes.Contains(f.Content(), []byte("MATCH,REJECT")) {
				t.Fatal("successful refresh did not install the changed configuration")
			}
		})
	}
}
