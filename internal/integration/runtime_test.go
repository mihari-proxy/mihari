package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestMihomoRuntimeLifecycleAndControlCommands(t *testing.T) {
	t.Setenv("MIHARI_FAKE_MIHOMO", "1")
	root := filepath.Join(t.TempDir(), "runtime")
	paths := platform.NewPaths(root)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, paths.CoreBinary)
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	addresses := reserveLoopbackAddresses(t, 3)
	settings.ControllerAddr = addresses[0]
	settings.MixedAddr = addresses[1]
	settings.WebAddr = addresses[2]
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	assembly, err := app.BuildRuntime(paths, settings, "integration", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	endpoint := transporttest.Endpoint(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{
			Endpoint: endpoint, Token: "control-token", Version: "integration", Ready: ready,
			Store: assembly.Store, Runtime: assembly.Manager,
		})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("daemon did not become ready")
	}
	client := controlclient.New(endpoint, "control-token")
	running := waitForCore(t, client, done, func(status protocol.CoreStatus) bool {
		return status.Status == "running" && status.PID != 0 && status.Version == "v1.19.0"
	})

	groups, err := client.ProxyGroups(context.Background())
	if err != nil || len(groups.Groups) != 1 || groups.Groups[0].Name != "GLOBAL" {
		t.Fatalf("groups=%#v err=%v", groups, err)
	}
	if _, err := client.SelectProxy(context.Background(), "GLOBAL", protocol.ProxySelectionRequest{OperationID: "select-1", Name: "REJECT"}); err != nil {
		t.Fatal(err)
	}
	connections, err := client.Connections(context.Background())
	if err != nil || len(connections.Connections) != 1 {
		t.Fatalf("connections=%#v err=%v", connections, err)
	}
	rules, err := client.Rules(context.Background())
	if err != nil || len(rules.Rules) != 1 {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		name := request.URL.Query().Get("name")
		_, _ = io.WriteString(writer, "proxies:\n  - {name: "+name+", type: direct}\n")
	}))
	first, err := client.AddSubscription(context.Background(), protocol.SubscriptionAddRequest{OperationID: "sub-add-a", Name: "A", URL: provider.URL + "?name=A&token=private-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.AddSubscription(context.Background(), protocol.SubscriptionAddRequest{OperationID: "sub-add-b", Name: "B", URL: provider.URL + "?name=B&token=private-b"})
	if err != nil {
		t.Fatal(err)
	}
	refreshSubscription(t, client, first.Subscription.ID, "sub-refresh-a")
	refreshSubscription(t, client, second.Subscription.ID, "sub-refresh-b")
	provider.Close()
	if _, err := client.UseSubscription(context.Background(), second.Subscription.ID, protocol.MutationRequest{OperationID: "sub-use-b"}); err != nil {
		t.Fatalf("offline activation failed: %v", err)
	}
	subscriptions, err := client.Subscriptions(context.Background())
	if err != nil || subscriptions.ActiveID != second.Subscription.ID || len(subscriptions.Subscriptions) != 2 {
		t.Fatalf("subscriptions=%#v err=%v", subscriptions, err)
	}
	rawSubscriptions, _ := json.Marshal(subscriptions)
	if stringContainsAny(string(rawSubscriptions), "private-a", "private-b", provider.URL) {
		t.Fatalf("subscription response leaked URL: %s", rawSubscriptions)
	}
	var streamEvents int
	if err := client.Stream(context.Background(), "traffic", func(protocol.StreamEvent) error {
		streamEvents++
		return nil
	}); err != nil || streamEvents != 1 {
		t.Fatalf("stream events=%d err=%v", streamEvents, err)
	}
	if _, err := client.RestartCore(context.Background(), protocol.MutationRequest{OperationID: "restart-1"}); err != nil {
		t.Fatal(err)
	}
	restarted := waitForCore(t, client, done, func(status protocol.CoreStatus) bool {
		return status.Status == "running" && status.PID != 0 && status.PID != running.PID && status.Restarts >= 1
	})
	if restarted.PID == running.PID {
		t.Fatalf("core did not restart: before=%#v after=%#v", running, restarted)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func refreshSubscription(t *testing.T, client *controlclient.Client, id, operationID string) {
	t.Helper()
	for attempt := range 5 {
		_, err := client.RefreshSubscription(context.Background(), id, protocol.MutationRequest{OperationID: operationID + string(rune('0'+attempt))})
		if err == nil {
			return
		}
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeRevisionConflict {
			t.Fatal(err)
		}
	}
	t.Fatal("subscription refresh remained conflicted")
}

func stringContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func copyExecutable(t *testing.T, destination string) {
	t.Helper()
	sourcePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(destinationFile, source); err != nil {
		destinationFile.Close()
		t.Fatal(err)
	}
	if err := destinationFile.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForCore(t *testing.T, client *controlclient.Client, daemonDone <-chan error, accept func(protocol.CoreStatus) bool) protocol.CoreStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := client.Core(ctx)
		if err == nil && accept(status) {
			return status
		}
		select {
		case daemonError := <-daemonDone:
			t.Fatalf("daemon stopped while waiting for core: %v", daemonError)
		case <-ctx.Done():
			t.Fatalf("core did not reach expected state; last=%#v err=%v", status, err)
		case <-ticker.C:
		}
	}
}
