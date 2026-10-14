package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

func TestSetupAdd_ActivatesTargetAndPreservesOtherProfiles(t *testing.T) {
	var manager *Manager
	manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		manager.Observe(supervisor.Observation{Status: supervisor.StatusRunning, PID: 1234})
		_, _ = w.Write([]byte("proxies: []\n"))
	}))
	b, err := manager.AddSubscription(t.Context(), Operation{ID: "b"}, AddSubscriptionInput{Name: "B", URL: source})
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{ID: "setup-a", ExecutionMode: protocol.ExecutionModeSetup}
	a, err := manager.AddSubscription(t.Context(), op, AddSubscriptionInput{Name: "A", URL: source})
	if err != nil {
		t.Fatal(err)
	}
	got := manager.Subscriptions()
	if got.ActiveID != a.ID || len(got.Profiles) != 2 || got.Profiles[0].ID != b.ID {
		t.Fatalf("catalog=%+v", got)
	}
	if _, err := manager.AddSubscription(t.Context(), op, AddSubscriptionInput{Name: "A", URL: source}); err != nil {
		t.Fatal(err)
	}
	if len(manager.Subscriptions().Profiles) != 2 {
		t.Fatal("replay created duplicate profile")
	}
}

func TestSetupCore_ReuseStillAppliesCapturedChannel(t *testing.T) {
	var manager *Manager
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	manager = newTestManager(Options{Settings: settings, StagingDir: t.TempDir(), Installer: &fakeInstaller{detectVersion: func(ctx context.Context, _ string) (string, error) {
		_, err := manager.updateSettings(ctx, func(s *config.Settings) error { s.CoreChannel = "alpha"; return nil })
		return "v1.19.0", err
	}}})
	manager.installRequest.BinaryPath = t.TempDir() + "/mihomo"
	if err := os.WriteFile(manager.installRequest.BinaryPath, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Install(t.Context(), Operation{ID: "reuse", ExecutionMode: protocol.ExecutionModeSetup})
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.settingsSnapshot().CoreChannel; got != "stable" {
		t.Fatalf("concurrent choice replaced setup target: %s", got)
	}
}

func TestSetupGeoIP_RechecksReadyPairAndStrictRevision(t *testing.T) {
	ready := false
	prepared := &fakeGeoIPCandidate{valid: true, identity: "pair"}
	service := &fakeGeoIPService{statusFunc: func() geoip.Status {
		return geoip.Status{Country: geoip.DatabaseStatus{Available: ready}, ASN: geoip.DatabaseStatus{Available: ready}}
	}}
	manager := newTestManager(Options{GeoIP: service, PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { ready = true; return prepared, nil }})
	_, err := manager.UpdateGeoIP(t.Context(), Operation{ID: "pair", ExecutionMode: protocol.ExecutionModeSetup})
	if err != nil || prepared.commits != 0 || prepared.cleanups != 1 {
		t.Fatalf("err=%v candidate=%+v", err, prepared)
	}
	stale := uint64(99)
	_, err = manager.UpdateGeoIP(t.Context(), Operation{ID: "stale", ExecutionMode: protocol.ExecutionModeSetup, IfRevision: &stale})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("reused pair bypassed precondition: %v", err)
	}
}

func TestSetupGeoIP_StalePairHasBoundedRetries(t *testing.T) {
	service := &fakeGeoIPService{statusFunc: func() geoip.Status { return geoip.Status{} }}
	var candidates []*fakeGeoIPCandidate
	manager := newTestManager(Options{GeoIP: service, PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) {
		p := &fakeGeoIPCandidate{valid: true, identity: "pair", commitErr: geoip.ErrStaleCandidate}
		candidates = append(candidates, p)
		return p, nil
	}})
	_, err := manager.UpdateGeoIP(t.Context(), Operation{ID: "pair", ExecutionMode: protocol.ExecutionModeSetup})
	if err == nil || len(candidates) != 3 || service.recordedError {
		t.Fatalf("err=%v attempts=%d recorded=%v", err, len(candidates), service.recordedError)
	}
	for _, p := range candidates {
		if p.cleanups != 1 {
			t.Fatalf("cleanup=%d", p.cleanups)
		}
	}
}

func TestSetupCompletion_DoesNotReapplyEarlierTargets(t *testing.T) {
	service, err := onboarding.Open(onboarding.Options{StatePath: t.TempDir() + "/onboarding.json", InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(Options{Settings: config.Defaults(), Onboarding: service})
	mixed := "127.0.0.1:19500"
	if _, err := manager.UpdateOnboarding(t.Context(), Operation{ID: "ports", ExecutionMode: protocol.ExecutionModeSetup}, onboarding.Update{MixedAddr: &mixed}); err != nil {
		t.Fatal(err)
	}
	later := "127.0.0.1:19501"
	if _, err := manager.UpdateOnboarding(t.Context(), Operation{ID: "later"}, onboarding.Update{MixedAddr: &later}); err != nil {
		t.Fatal(err)
	}
	manager.Observe(supervisor.Observation{Status: supervisor.StatusRunning})
	complete := true
	got, err := manager.UpdateOnboarding(t.Context(), Operation{ID: "done", ExecutionMode: protocol.ExecutionModeSetup}, onboarding.Update{Complete: &complete})
	if err != nil || !got.Status.Complete || got.Status.MixedAddr != later {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestSetupAdd_ReloadFailureRollsBackWholeStep(t *testing.T) {
	manager, _, controller, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	b, err := manager.AddSubscription(t.Context(), Operation{ID: "b"}, AddSubscriptionInput{Name: "B", URL: source})
	if err != nil {
		t.Fatal(err)
	}
	controller.reloadErr = errors.New("synthetic reload failure")
	_, err = manager.AddSubscription(t.Context(), Operation{ID: "a", ExecutionMode: protocol.ExecutionModeSetup}, AddSubscriptionInput{Name: "A", URL: source})
	if err == nil {
		t.Fatal("setup activation did not reload")
	}
	got := manager.Subscriptions()
	if got.ActiveID != b.ID || len(got.Profiles) != 1 {
		t.Fatalf("half-committed setup: %+v", got)
	}
}

func TestSetupRefresh_ExplicitTargetBecomesEnabledAndActive(t *testing.T) {
	manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	for _, name := range []string{"B", "A"} {
		if _, err := manager.AddSubscription(t.Context(), Operation{ID: name}, AddSubscriptionInput{Name: name, URL: source}); err != nil {
			t.Fatal(err)
		}
	}
	a := manager.Subscriptions().Profiles[1]
	if _, err := manager.SetSubscriptionEnabled(t.Context(), Operation{ID: "disable-a"}, a.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := manager.RefreshSubscription(t.Context(), Operation{ID: "refresh-a", ExecutionMode: protocol.ExecutionModeSetup}, a.ID)
	if err != nil || !got.Enabled || manager.Subscriptions().ActiveID != a.ID {
		t.Fatalf("profile=%+v catalog=%+v err=%v", got, manager.Subscriptions(), err)
	}
}

func TestSetupAdd_RepreparesWithoutDownloadingAgain(t *testing.T) {
	for _, keepChanging := range []bool{false, true} {
		t.Run(fmt.Sprint(keepChanging), func(t *testing.T) {
			var fetches atomic.Int32
			manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fetches.Add(1); _, _ = w.Write([]byte("proxies: []\n")) }))
			prepares := 0
			manager.validateConfig = func(ctx context.Context, _ string) error {
				prepares++
				if prepares == 1 || keepChanging {
					if err := manager.lockMutation(ctx); err != nil {
						return err
					}
					defer manager.unlock()
					_, err := manager.updateSettings(ctx, func(s *config.Settings) error { s.MixedAddr = fmt.Sprintf("127.0.0.1:%d", 19200+prepares); return nil })
					return err
				}
				return nil
			}
			_, err := manager.AddSubscription(t.Context(), Operation{ID: "a", ExecutionMode: protocol.ExecutionModeSetup}, AddSubscriptionInput{Name: "A", URL: source})
			if fetches.Load() != 1 {
				t.Fatalf("fetches=%d", fetches.Load())
			}
			if keepChanging {
				if err == nil || prepares != 3 || len(manager.Subscriptions().Profiles) != 0 {
					t.Fatalf("err=%v prepares=%d catalog=%+v", err, prepares, manager.Subscriptions())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(manager.runtimeConfig)
				if err != nil {
					t.Fatal(err)
				}
				if prepares != 2 || !strings.Contains(string(content), "19201") {
					t.Fatalf("prepares=%d config=%s", prepares, content)
				}
			}
			files, err := os.ReadDir(manager.stagingDir)
			if err != nil || len(files) != 0 {
				t.Fatalf("staging files=%v err=%v", files, err)
			}
		})
	}
}

func TestSetupCore_UnrelatedObservationDoesNotRejectAndExplicitRevisionStillApplies(t *testing.T) {
	var manager *Manager
	commits := 0
	manager = newTestManager(Options{Supervisor: &fakeSupervisor{}, Installer: &fakeInstaller{prepare: func(context.Context, core.InstallRequest) (PreparedCore, error) {
		manager.Observe(supervisor.Observation{Status: "missing"})
		return &fakeCandidate{version: "v1.19.0", commit: func() (core.InstallResult, error) {
			commits++
			return core.InstallResult{Version: "v1.19.0", Updated: true}, nil
		}}, nil
	}}})
	_, err := manager.Install(t.Context(), Operation{ID: "install", ExecutionMode: protocol.ExecutionModeSetup})
	if err != nil || commits != 1 {
		t.Fatalf("err=%v commits=%d", err, commits)
	}
	stale := uint64(0)
	_, err = manager.Install(t.Context(), Operation{ID: "stale", ExecutionMode: protocol.ExecutionModeSetup, IfRevision: &stale})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict || commits != 1 {
		t.Fatalf("err=%v commits=%d", err, commits)
	}
}

func TestSetupCore_ReuseValidatesCurrentConfiguration(t *testing.T) {
	rejection := errors.New("existing core rejects setup configuration")
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("ab", 32)
	manager := newTestManager(Options{Settings: settings, StagingDir: t.TempDir(), ValidateConfig: func(context.Context, string) error { return rejection }, Installer: &fakeInstaller{detectVersion: func(context.Context, string) (string, error) { return "v1.19.0", nil }}})
	manager.installRequest.BinaryPath = t.TempDir() + "/mihomo"
	if err := os.WriteFile(manager.installRequest.BinaryPath, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Install(t.Context(), Operation{ID: "reuse-invalid", ExecutionMode: protocol.ExecutionModeSetup})
	if !errors.Is(err, rejection) {
		t.Fatalf("invalid existing core accepted: %v", err)
	}
	if manager.store.Load().Core.Version != "" {
		t.Fatal("rejected core published")
	}
}

func TestSetupRefresh_MetadataChangePreservesTargetAndLatestMetadata(t *testing.T) {
	manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	a, err := manager.AddSubscription(t.Context(), Operation{ID: "a"}, AddSubscriptionInput{Name: "A", URL: source})
	if err != nil {
		t.Fatal(err)
	}
	once := false
	manager.validateConfig = func(ctx context.Context, _ string) error {
		if !once {
			once = true
			name := "renamed"
			_, err := manager.SetSubscription(ctx, Operation{ID: "rename"}, a.ID, SetSubscriptionInput{Name: &name})
			return err
		}
		return nil
	}
	got, err := manager.RefreshSubscription(t.Context(), Operation{ID: "setup-refresh", ExecutionMode: protocol.ExecutionModeSetup}, a.ID)
	if err != nil || got.Name != "renamed" || manager.Subscriptions().ActiveID != a.ID {
		t.Fatalf("profile=%+v err=%v", got, err)
	}
}

func TestSetupCommit_UnconfirmedCompensationStopsCoreAndBlocksMutations(t *testing.T) {
	manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	if _, err := manager.AddSubscription(t.Context(), Operation{ID: "seed"}, AddSubscriptionInput{Name: "B", URL: source}); err != nil {
		t.Fatal(err)
	}
	stopped := false
	manager.supervisor = &coreMaintenanceSupervisor{fakeSupervisor: &fakeSupervisor{}, maintain: func(_ context.Context, work func() error) error { stopped = true; return work() }}
	catalogPath := filepath.Join(filepath.Dir(filepath.Dir(manager.runtimeConfig)), "subscriptions", "catalog.yaml")
	manager.validateConfig = func(context.Context, string) error {
		if err := os.Remove(catalogPath); err != nil {
			return err
		}
		if err := os.Mkdir(catalogPath, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(catalogPath, "blocked"), []byte("fixture"), 0600)
	}
	_, err := manager.AddSubscription(t.Context(), Operation{ID: "setup", ExecutionMode: protocol.ExecutionModeSetup}, AddSubscriptionInput{Name: "A", URL: source})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Details["degraded"] != true || !stopped || manager.store.Load().Health != "degraded" {
		t.Fatalf("err=%v stopped=%v state=%+v", err, stopped, manager.store.Load())
	}
	if err := manager.lockMutation(t.Context()); err == nil {
		manager.unlock()
		t.Fatal("unsafe mutation allowed after failed compensation")
	}
}

func TestSetupRefresh_SourceChangeOrDeletionDoesNotRestoreOldObject(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprint(deleted), func(t *testing.T) {
			manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
			a, err := manager.AddSubscription(t.Context(), Operation{ID: "a"}, AddSubscriptionInput{Name: "A", URL: source})
			if err != nil {
				t.Fatal(err)
			}
			// Keep A inactive so concurrent edits need no nested config generation.
			b, err := manager.AddSubscription(t.Context(), Operation{ID: "b"}, AddSubscriptionInput{Name: "B", URL: source})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.UseSubscription(t.Context(), Operation{ID: "use-b"}, b.ID); err != nil {
				t.Fatal(err)
			}
			once := false
			manager.validateConfig = func(ctx context.Context, _ string) error {
				if once {
					return nil
				}
				once = true
				if deleted {
					return manager.RemoveSubscription(ctx, Operation{ID: "delete"}, a.ID)
				}
				url := source + "?new-source"
				_, err := manager.SetSubscription(ctx, Operation{ID: "change-source"}, a.ID, SetSubscriptionInput{URL: &url})
				return err
			}
			_, err = manager.RefreshSubscription(t.Context(), Operation{ID: "setup-refresh", ExecutionMode: protocol.ExecutionModeSetup}, a.ID)
			var api protocol.APIError
			if !errors.As(err, &api) || (api.Code != protocol.CodeInvalidArgument && api.Code != protocol.CodeInvalidState) {
				t.Fatalf("expected explicit identity error: %v", err)
			}
			if manager.Subscriptions().ActiveID != b.ID {
				t.Fatal("invalidated target changed active subscription")
			}
			profiles := manager.Subscriptions().Profiles
			if deleted && len(profiles) != 1 {
				t.Fatalf("deleted target resurrected: %+v", profiles)
			}
			if !deleted {
				url, readErr := manager.SubscriptionURL(t.Context(), a.ID)
				if readErr != nil || url != source+"?new-source" {
					t.Fatalf("source overwritten: %s %v", url, readErr)
				}
			}
		})
	}
}

func TestSetupRefresh_ReprepareRemainsBounded(t *testing.T) {
	manager, _, _, source := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("proxies: []\n")) }))
	a, err := manager.AddSubscription(t.Context(), Operation{ID: "a"}, AddSubscriptionInput{Name: "A", URL: source})
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	manager.validateConfig = func(ctx context.Context, _ string) error {
		attempts++
		name := fmt.Sprintf("name-%d", attempts)
		_, err := manager.SetSubscription(ctx, Operation{ID: name}, a.ID, SetSubscriptionInput{Name: &name})
		return err
	}
	_, err = manager.RefreshSubscription(t.Context(), Operation{ID: "setup-refresh", ExecutionMode: protocol.ExecutionModeSetup}, a.ID)
	if err == nil || attempts != 3 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
	if manager.mutationDegraded.Load() {
		t.Fatal("preparation conflict entered degraded")
	}
}
