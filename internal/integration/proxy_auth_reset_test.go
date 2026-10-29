package integration

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/config"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	controlserver "github.com/mihari-proxy/mihari/internal/control/server"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/daemon"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestProxyAuthAndReset_AuthenticatedIPCAndPersistence(t *testing.T) {
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	if err := config.Save(paths.Settings, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ControlToken, []byte("fixture-control"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.CoreBinary, []byte("fixture-core-retained"), 0600); err != nil {
		t.Fatal(err)
	}
	subs, err := subscription.Open(subscription.ServiceOptions{CatalogPath: paths.SubscriptionCatalog, CacheDir: paths.SubscriptionCache})
	if err != nil {
		t.Fatal(err)
	}
	onboard, err := onboarding.Open(onboarding.Options{StatePath: paths.Onboarding})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(state.Snapshot{Health: "ok", Core: state.CoreState{Status: "stopped"}})
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Settings: settings, SettingsPath: paths.Settings, RuntimeConfig: paths.RuntimeConfig, StagingDir: paths.SubscriptionStaging, Subscriptions: subs, Onboarding: onboard, SysProxy: &sysproxy.FakeBackend{}, BusinessReset: app.BusinessReset{Paths: paths}})
	control := controlserver.New(controlserver.Options{Token: "fixture-control", Store: store, Runtime: manager})
	server := httptest.NewServer(control.Handler())
	defer server.Close()
	client := controlclient.NewHTTP(server.URL, "fixture-control", server.Client())
	request := protocol.ProxyAuthUpdateRequest{OperationID: "auth", Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "fixture-secret"}}}}
	result, err := client.UpdateProxyAuth(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Users) != 1 || result.Users[0].Password != "" || result.Config.Users[0].Password != "" {
		t.Fatal("ordinary response includes a password")
	}
	stored, err := config.Load(paths.Settings)
	if err != nil || stored.ProxyAuth.Users[0].Password != "fixture-secret" {
		t.Fatalf("auth was not persisted: %v", err)
	}
	revealed, err := client.ProxyAuth(t.Context(), true)
	if err != nil || revealed.Users[0].Password != "fixture-secret" {
		t.Fatalf("explicit reveal failed: %v", err)
	}
	for _, path := range []string{"/v1/proxy-auth?reveal=true", "/v1/data-reset"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated request accepted: %s", path)
		}
	}
	preview, err := client.DataResetPreview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ResetBusinessData(t.Context(), protocol.DataResetRequest{OperationID: "one-confirm", Digest: preview.Digest, Confirm: true}); err == nil {
		t.Fatal("missing second confirmation accepted")
	}
	if _, err := client.ResetBusinessData(t.Context(), protocol.DataResetRequest{OperationID: "no-confirm", Digest: preview.Digest}); err == nil {
		t.Fatal("missing confirmations accepted")
	}
	after, err := config.Load(paths.Settings)
	if err != nil || after.ProxyAuth == nil {
		t.Fatal("unconfirmed reset modified settings")
	}
	reset, err := client.ResetBusinessData(t.Context(), protocol.DataResetRequest{OperationID: "reset", Digest: preview.Digest, Confirm: true, ConfirmAgain: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reset.SetupRequired || !reset.RestartRequired {
		t.Fatal("reset did not return Setup/restart state")
	}
	after, err = config.Load(paths.Settings)
	if err != nil || after.ProxyAuth != nil || after.ControllerSecret == settings.ControllerSecret {
		t.Fatalf("reset credentials/settings incorrect: %v", err)
	}
	core, err := os.ReadFile(paths.CoreBinary)
	if err != nil || string(core) != "fixture-core-retained" {
		t.Fatal("reset removed the downloaded core")
	}
	token, err := os.ReadFile(paths.ControlToken)
	if err != nil || string(token) != "fixture-control" {
		t.Fatal("reset changed control credential")
	}
	if _, err := client.Status(t.Context()); err != nil {
		t.Fatal("local IPC unavailable after reset")
	}
}

func TestProxyAuthAndReset_RunningCoreStopsBeforeResetAndRemainsStopped(t *testing.T) {
	t.Setenv("MIHARI_FAKE_MIHOMO", "1")
	paths := platform.NewPaths(t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, paths.CoreBinary)
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	addresses := reserveLoopbackAddresses(t, 3)
	settings.ControllerAddr, settings.MixedAddr, settings.WebAddr = addresses[0], addresses[1], addresses[2]
	if err := config.Save(paths.Settings, settings); err != nil {
		t.Fatal(err)
	}
	resetStore := &rejectingFirstReset{BusinessReset: app.BusinessReset{Paths: paths}, reject: true}
	assembly, err := app.BuildRuntimeWithOptions(paths, settings, "fixture", io.Discard, io.Discard, app.RuntimeBuildOptions{SysProxy: &sysproxy.FakeBackend{}, ResetStore: resetStore})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	joined := false
	endpoint := transporttest.Endpoint(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{Endpoint: endpoint, Token: "fixture", Ready: ready, Store: assembly.Store, Runtime: assembly.Manager})
	}()
	t.Cleanup(func() {
		if joined {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon cleanup: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not release its fake core")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon not ready")
	}
	client := controlclient.New(endpoint, "fixture")
	waitForCore(t, client, done, func(s protocol.CoreStatus) bool { return s.Status == "running" && s.PID != 0 })
	_, err = client.UpdateProxyAuth(t.Context(), protocol.ProxyAuthUpdateRequest{OperationID: "online-auth", Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "u", Password: "p"}}}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.DataResetPreview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ResetBusinessData(t.Context(), protocol.DataResetRequest{OperationID: "rejected-before-marker", Digest: preview.Digest, Confirm: true, ConfirmAgain: true})
	var rejected protocol.APIError
	if !errors.As(err, &rejected) || rejected.Code != protocol.CodeRevisionConflict {
		t.Fatalf("expected pre-marker rejection: %v", err)
	}
	waitForCore(t, client, done, func(s protocol.CoreStatus) bool { return s.Status == "running" && s.PID != 0 })
	pending, pendingErr := resetStore.Pending()
	if pendingErr != nil || pending {
		t.Fatal("rejected reset crossed its commit marker")
	}
	preview, err = client.DataResetPreview(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ResetBusinessData(t.Context(), protocol.DataResetRequest{OperationID: "online-reset", Digest: preview.Digest, Confirm: true, ConfirmAgain: true})
	if err != nil {
		t.Fatal(err)
	}
	core, err := client.Core(t.Context())
	if err != nil || core.PID != 0 || core.Status != "stopped" {
		t.Fatalf("core survived reset: %+v err=%v", core, err)
	}
	if _, err := os.Stat(paths.CoreBinary); err != nil {
		t.Fatal("downloaded core removed")
	}
	if _, err := assembly.Manager.PrepareCoreStart(t.Context()); err == nil {
		t.Fatal("core start bypassed the Setup barrier")
	}
	// Reassemble only after the first daemon and its child have joined. Use new random
	// test endpoints rather than binding the workstation's default ports after reset.
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reset daemon did not stop")
	}
	fresh, err := config.Load(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	addresses = reserveLoopbackAddresses(t, 3)
	fresh.ControllerAddr, fresh.MixedAddr, fresh.WebAddr = addresses[0], addresses[1], addresses[2]
	if err := config.Save(paths.Settings, fresh); err != nil {
		t.Fatal(err)
	}
	next, err := app.BuildRuntimeWithOptions(paths, fresh, "fixture", io.Discard, io.Discard, app.RuntimeBuildOptions{SysProxy: &sysproxy.FakeBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	ready2 := make(chan struct{})
	done2 := make(chan error, 1)
	endpoint2 := transporttest.Endpoint(t)
	go func() {
		done2 <- daemon.Run(ctx2, daemon.Options{Endpoint: endpoint2, Token: "fixture", Ready: ready2, Store: next.Store, Runtime: next.Manager})
	}()
	t.Cleanup(func() {
		cancel2()
		select {
		case err := <-done2:
			if err != nil {
				t.Errorf("reinitialized daemon cleanup: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("reinitialized daemon leaked")
		}
	})
	select {
	case <-ready2:
	case <-time.After(5 * time.Second):
		t.Fatal("reinitialized daemon not ready")
	}
	client2 := controlclient.New(endpoint2, "fixture")
	status, err := client2.Status(t.Context())
	if err != nil || !status.SetupRequired {
		t.Fatalf("Setup barrier lost after daemon restart: %v", err)
	}
	if _, err := next.Manager.PrepareCoreStart(t.Context()); err == nil {
		t.Fatal("restart bypassed Setup barrier")
	}
	complete := true
	if _, err := client2.UpdateOnboarding(t.Context(), protocol.OnboardingUpdateRequest{OperationID: "complete-reset-setup", Complete: &complete}); err != nil {
		t.Fatal(err)
	}
	waitForCore(t, client2, done2, func(s protocol.CoreStatus) bool { return s.Status == "running" && s.PID != 0 })
	status, err = client2.Status(t.Context())
	if err != nil || status.SetupRequired {
		t.Fatalf("Setup did not release startup barrier: %v", err)
	}

}

type rejectingFirstReset struct {
	app.BusinessReset
	reject bool
}

func (r *rejectingFirstReset) Reset(ctx context.Context, digest string, warning func(error)) (config.Settings, string, error) {
	if r.reject {
		r.reject = false
		return config.Settings{}, "", protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "fixture source changed before reset commit"}
	}
	return r.BusinessReset.Reset(ctx, digest, warning)
}
