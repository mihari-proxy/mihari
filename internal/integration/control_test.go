package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type controlledSubscriptionFetcher struct {
	entered     chan struct{}
	enteredOnce sync.Once
	release     chan struct{}
	releaseOnce sync.Once
}

func newControlledSubscriptionFetcher() *controlledSubscriptionFetcher {
	return &controlledSubscriptionFetcher{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (f *controlledSubscriptionFetcher) Fetch(ctx context.Context, _ subscription.FetchRequest) (subscription.FetchResult, error) {
	f.enteredOnce.Do(func() { close(f.entered) })
	select {
	case <-f.release:
		return subscription.FetchResult{Content: []byte("proxies: []\n")}, nil
	case <-ctx.Done():
		return subscription.FetchResult{}, ctx.Err()
	}
}

func (f *controlledSubscriptionFetcher) releaseFetch() {
	f.releaseOnce.Do(func() { close(f.release) })
}

type subscriptionControlFixture struct {
	client    *controlclient.Client
	fetcher   *controlledSubscriptionFetcher
	profileID string
	cancel    context.CancelFunc
	done      chan struct{}
	daemonErr error
}

func newSubscriptionControlFixture(t *testing.T) *subscriptionControlFixture {
	t.Helper()
	root := t.TempDir()
	fetcher := newControlledSubscriptionFetcher()
	service, err := subscription.Open(subscription.ServiceOptions{
		CatalogPath: filepath.Join(root, "subscriptions", "catalog.yaml"),
		CacheDir:    filepath.Join(root, "subscriptions", "cache"),
		Downloader:  fetcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.Add("controlled", "https://example.test/subscription", subscription.ProxyModeDirect)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := filepath.Join(root, "runtime", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(runtimeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeConfig, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(state.Snapshot{
		Version:   "integration",
		StartedAt: time.Now().UTC(),
		Health:    "ok",
	})
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	manager := runtimeapi.New(runtimeapi.Options{
		Store:          store,
		Coordinator:    state.NewCoordinator(store),
		Subscriptions:  service,
		Settings:       settings,
		RuntimeConfig:  runtimeConfig,
		StagingDir:     filepath.Join(root, "staging"),
		ValidateConfig: func(context.Context, string) error { return nil },
	})

	endpoint := transporttest.Endpoint(t)
	const token = "subscription-integration-token"
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	fixture := &subscriptionControlFixture{
		client:    controlclient.New(endpoint, token),
		fetcher:   fetcher,
		profileID: profile.ID,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	go func() {
		defer close(fixture.done)
		fixture.daemonErr = daemon.Run(ctx, daemon.Options{
			Endpoint: endpoint,
			Token:    token,
			Version:  "integration",
			Ready:    ready,
			Store:    store,
			Runtime:  manager,
		})
	}()
	t.Cleanup(func() {
		fetcher.releaseFetch()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer shutdownCancel()
		if err := fixture.stop(shutdownCtx); err != nil {
			t.Errorf("daemon cleanup: %v", err)
		}
	})
	select {
	case <-ready:
	case <-fixture.done:
		t.Fatalf("daemon exited before becoming ready: %v", fixture.daemonErr)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	return fixture
}

func (f *subscriptionControlFixture) stop(ctx context.Context) error {
	f.cancel()
	select {
	case <-f.done:
		return f.daemonErr
	case <-ctx.Done():
		return fmt.Errorf("daemon did not stop: %w", ctx.Err())
	}
}

func TestControlPlaneLifecycleAndConcurrentStatus(t *testing.T) {
	endpoint := transporttest.Endpoint(t)
	const token = "integration-token"
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{
			Endpoint: endpoint,
			Token:    token,
			Version:  "integration",
			Ready:    ready,
		})
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("daemon did not become ready")
	}

	client := controlclient.New(endpoint, token)
	errors := make(chan error, 64)
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			// Generous per-request budget: under -race on a busy CI runner a
			// cold 3s window was flaky even though nothing is actually stuck.
			requestCtx, requestCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer requestCancel()
			status, err := client.Status(requestCtx)
			if err != nil {
				errors <- err
				return
			}
			if status.Schema != "mihari/v1" || status.ProtocolVersion != "v1" || status.Health != "ok" || status.Revision != 0 {
				errors <- fmt.Errorf("unexpected status: %#v", status)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	unauthorizedStdout := &bytes.Buffer{}
	unauthorizedStderr := &bytes.Buffer{}
	unauthorizedExit := cli.Execute(context.Background(), []string{"status", "--json"}, unauthorizedStdout, unauthorizedStderr, cli.Dependencies{
		StatusClient: controlclient.New(endpoint, "wrong-token"),
	})
	if unauthorizedExit != cli.ExitPermission {
		t.Fatalf("unauthorized exit=%d stdout=%q stderr=%q", unauthorizedExit, unauthorizedStdout.String(), unauthorizedStderr.String())
	}
	var unauthorizedEnvelope protocol.ErrorEnvelope
	if err := json.Unmarshal(unauthorizedStderr.Bytes(), &unauthorizedEnvelope); err != nil {
		t.Fatalf("decode unauthorized error: %v: %q", err, unauthorizedStderr.String())
	}
	if unauthorizedEnvelope.Error.Code != protocol.CodePermissionDenied {
		t.Fatalf("unauthorized error=%#v", unauthorizedEnvelope.Error)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not stop")
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exit := cli.Execute(context.Background(), []string{"status", "--json"}, stdout, stderr, cli.Dependencies{StatusClient: client})
	if exit != cli.ExitDaemonUnavailable {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatalf("decode post-shutdown error: %v: %q", err, stderr.String())
	}
	if envelope.Error.Code != protocol.CodeDaemonUnavailable {
		t.Fatalf("post-shutdown error=%#v", envelope.Error)
	}
}

func TestSubscriptionRemoveOverIPCRejectsLateRefreshCommit(t *testing.T) {
	fixture := newSubscriptionControlFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	refreshDone := make(chan error, 1)
	go func() {
		defer close(refreshDone)
		_, refreshErr := fixture.client.RefreshSubscription(ctx, fixture.profileID, protocol.MutationRequest{OperationID: "subscription-refresh"})
		refreshDone <- refreshErr
	}()
	t.Cleanup(func() {
		fixture.fetcher.releaseFetch()
		select {
		case <-refreshDone:
		case <-time.After(8 * time.Second):
			t.Error("refresh goroutine did not stop")
		}
	})
	select {
	case <-fixture.fetcher.entered:
	case err := <-refreshDone:
		t.Fatalf("refresh finished before entering fetch: %v", err)
	case <-ctx.Done():
		t.Fatalf("refresh did not enter fetch: %v", ctx.Err())
	}

	if _, err := fixture.client.RemoveSubscription(ctx, fixture.profileID, protocol.MutationRequest{OperationID: "subscription-remove"}); err != nil {
		t.Fatal(err)
	}
	fixture.fetcher.releaseFetch()

	select {
	case err := <-refreshDone:
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || (apiError.Code != protocol.CodeRevisionConflict && apiError.Code != protocol.CodeInvalidArgument) {
			t.Fatalf("refresh error=%v", err)
		}
	case <-ctx.Done():
		t.Fatalf("refresh did not finish: %v", ctx.Err())
	}

	list, err := fixture.client.Subscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range list.Subscriptions {
		if profile.ID == fixture.profileID {
			t.Fatalf("deleted subscription was recreated: %#v", profile)
		}
	}
	if err := fixture.stop(ctx); err != nil {
		t.Fatalf("stop daemon: %v", err)
	}
}
