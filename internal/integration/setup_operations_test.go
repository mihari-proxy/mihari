package integration

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSetupOperationIPC_TracksRegistrationThroughFirstDownload(t *testing.T) {
	fixture := newSubscriptionControlFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := fixture.client.AddSubscription(ctx, protocol.SubscriptionAddRequest{OperationID: "setup-new-sub", Name: "new", URL: "https://example.test/new"})
		done <- err
	}()
	select {
	case <-fixture.fetcher.entered:
	case err := <-done:
		t.Fatalf("subscription request returned before download: %v", err)
	case <-ctx.Done():
		t.Fatal("first download did not start")
	}
	status, err := fixture.client.OperationStatus(ctx, "setup-new-sub")
	if err != nil || status.State != "running" {
		t.Fatalf("observation during first download=%s err=%v", status.State, err)
	}
	fixture.fetcher.releaseFetch()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("subscription request did not settle")
	}
	status, err = fixture.client.OperationStatus(ctx, "setup-new-sub")
	if err != nil || status.State != "finished" {
		t.Fatalf("settled observation=%s err=%v", status.State, err)
	}
	profiles, err := fixture.client.Subscriptions(ctx)
	if err != nil || len(profiles.Subscriptions) != 2 {
		t.Fatal("read-only observation changed subscription registration")
	}
}
