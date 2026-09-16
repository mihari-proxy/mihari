package runtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type timeoutRoutingController struct {
	*routingController
	reload func(context.Context) error
}

type timeoutSubscriptionFetcher func(context.Context, subscription.FetchRequest) (subscription.FetchResult, error)

func (f timeoutSubscriptionFetcher) Fetch(ctx context.Context, r subscription.FetchRequest) (subscription.FetchResult, error) {
	return f(ctx, r)
}

func TestSubscriptionTimeout_ManagerSharesDeadlineAcrossStages(t *testing.T) {
	for _, shortParent := range []bool{false, true} {
		var downloadDeadline time.Time
		fetcher := timeoutSubscriptionFetcher(func(ctx context.Context, _ subscription.FetchRequest) (subscription.FetchResult, error) {
			var ok bool
			downloadDeadline, ok = ctx.Deadline()
			if !ok || time.Until(downloadDeadline) > 120*time.Second {
				t.Error("download missing daemon deadline")
			}
			return subscription.FetchResult{Content: []byte("proxies: []\n")}, nil
		})
		m, _, c, url := subscriptionManagerWithDownloader(t, http.NotFoundHandler(), fetcher)
		m.validateConfig = func(ctx context.Context, _ string) error {
			deadline, _ := ctx.Deadline()
			if deadline.IsZero() || !deadline.Equal(downloadDeadline) {
				t.Error("validation renewed deadline")
			}
			return nil
		}
		c.reload = func(ctx context.Context) error {
			deadline, _ := ctx.Deadline()
			if deadline.IsZero() || !deadline.Equal(downloadDeadline) {
				t.Error("reload renewed deadline")
			}
			return nil
		}
		ctx := context.Background()
		if shortParent {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
		}
		profile, err := m.AddSubscription(ctx, Operation{ID: "add"}, AddSubscriptionInput{Name: "fixture", URL: url})
		if err != nil || !profile.Cached {
			t.Fatalf("add failed: %+v %v", profile, err)
		}
		if shortParent {
			deadline, _ := ctx.Deadline()
			if !downloadDeadline.Equal(deadline) {
				t.Error("parent deadline extended")
			}
		}
		if _, err := m.RefreshSubscription(ctx, Operation{ID: "refresh"}, profile.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSubscriptionTimeout_CompensationIsBounded(t *testing.T) {
	m, _, c, _ := subscriptionManager(t, http.NotFoundHandler())
	m.subscriptionRecoveryTimeout = 20 * time.Millisecond
	calls := 0
	c.reload = func(ctx context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("reload failed")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	err := m.commitRuntimeConfigBytes(context.Background(), configCandidate{content: []byte("proxies: []\n")})
	var api protocol.APIError
	if calls != 2 || !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &api) || api.Details["degraded"] != true {
		t.Fatalf("unconfirmed recovery not bounded/degraded: %v", err)
	}
}

func TestSubscriptionTimeout_ManagerBoundsExecution(t *testing.T) {
	for _, stage := range []string{"download", "validation", "lock", "reload"} {
		t.Run(stage, func(t *testing.T) {
			m, service, controller, url := subscriptionManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "download" {
					<-r.Context().Done()
					return
				}
				if _, err := w.Write([]byte("proxies: []\n")); err != nil {
					t.Error(err)
				}
			}))
			profile, err := service.Add("fixture", url, "")
			if err != nil {
				t.Fatal(err)
			}
			m.subscriptionTimeout = 100 * time.Millisecond
			before, err := os.ReadFile(m.runtimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			locked := false
			m.validateConfig = func(ctx context.Context, _ string) error {
				if stage == "validation" {
					<-ctx.Done()
					return ctx.Err()
				}
				if stage == "lock" {
					if err := m.lockMutation(ctx); err != nil {
						return err
					}
					locked = true
				}
				return nil
			}
			calls := 0
			controller.reload = func(ctx context.Context) error {
				calls++
				if stage == "reload" && calls == 1 {
					<-ctx.Done()
					return ctx.Err()
				}
				return ctx.Err()
			}
			_, err = m.RefreshSubscription(context.Background(), Operation{ID: "bounded"}, profile.ID)
			if locked {
				m.unlock()
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stage %s not bounded: %v", stage, err)
			}
			after, readErr := os.ReadFile(m.runtimeConfig)
			if readErr != nil || string(after) != string(before) {
				t.Fatal("failed refresh changed valid config")
			}
			if service.Snapshot().Profiles[0].Generation != 0 {
				t.Fatal("failed refresh committed cache")
			}
		})
	}
}

func TestSubscriptionTimeout_AddRefreshDoesNotRenewDeadline(t *testing.T) {
	var observed time.Time
	fetch := timeoutSubscriptionFetcher(func(ctx context.Context, _ subscription.FetchRequest) (subscription.FetchResult, error) {
		observed, _ = ctx.Deadline()
		return subscription.FetchResult{}, errors.New("fixture download failure")
	})
	m, service, _, url := subscriptionManagerWithDownloader(t, http.NotFoundHandler(), fetch)
	m.subscriptionTimeout = time.Second
	// Registration completes before Refresh starts. Give the nested entry a
	// larger default to prove it still inherits the original Add deadline.
	m.refreshLogSecrets = func([]string) { m.subscriptionTimeout = 2 * time.Second }
	start := time.Now()
	profile, err := m.AddSubscription(context.Background(), Operation{ID: "add"}, AddSubscriptionInput{Name: "fixture", URL: url})
	if err != nil || profile.ID == "" || profile.LastError == "" || len(service.Snapshot().Profiles) != 1 {
		t.Fatalf("registration lost: %+v %v", profile, err)
	}
	if observed.IsZero() || observed.After(start.Add(1500*time.Millisecond)) {
		t.Fatal("nested refresh renewed Add deadline")
	}
}

func TestSubscriptionTimeout_WaiterCannotCancelOwner(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	fetch := timeoutSubscriptionFetcher(func(ctx context.Context, _ subscription.FetchRequest) (subscription.FetchResult, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return subscription.FetchResult{}, ctx.Err()
		}
		return subscription.FetchResult{Content: []byte("proxies: []\n")}, nil
	})
	m, service, _, url := subscriptionManagerWithDownloader(t, http.NotFoundHandler(), fetch)
	profile, err := service.Add("fixture", url, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.RefreshSubscription(ctx, Operation{ID: "same"}, profile.ID); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitCtx, stopWait := context.WithCancel(ctx)
	stopWait()
	_, waitErr := m.RefreshSubscription(waitCtx, Operation{ID: "same"}, profile.ID)
	close(release)
	ownerErr := <-done
	if !errors.Is(waitErr, context.Canceled) || ownerErr != nil {
		t.Fatalf("waiter=%v owner=%v", waitErr, ownerErr)
	}
}

func (c timeoutRoutingController) Reload(ctx context.Context, _ string, _ bool) error {
	return c.reload(ctx)
}

func TestSubscriptionTimeout_ReloadCancellationRestoresPreviousState(t *testing.T) {
	for _, routing := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "routing"}[routing], func(t *testing.T) {
			m, base := routingFixture(t)
			m.settings.SetRoutingMode("global")
			base.mode, base.selected = "global", "Node B"
			if !routing {
				m.settings.Routing = nil
			}
			m.runtimeConfig = filepath.Join(t.TempDir(), "runtime.yaml")
			before := "mode: global\n"
			if err := os.WriteFile(m.runtimeConfig, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "refresh-timeout"}))
			defer cancel()
			calls := 0
			m.controller = timeoutRoutingController{base, func(recovery context.Context) error {
				calls++
				if calls == 1 {
					cancel()
					return ctx.Err()
				}
				if recovery.Err() != nil {
					t.Error("recovery inherited cancellation")
					return recovery.Err()
				}
				deadline, ok := recovery.Deadline()
				limit := 10 * time.Second
				if calls == 3 {
					limit = 15 * time.Second
				}
				if !ok || time.Until(deadline) > limit || time.Until(deadline) < limit-time.Second {
					t.Errorf("recovery %d missing independent bounded deadline", calls)
				}
				return nil
			}}
			err := m.commitRuntimeConfig(ctx, configCandidate{content: []byte("mode: direct\n")})
			var api protocol.APIError
			if !errors.Is(err, context.Canceled) || !errors.As(err, &api) || api.Details["degraded"] == true {
				t.Fatalf("recovery failed: %v", err)
			}
			wantCalls := 2
			if routing {
				wantCalls = 3
			}
			if calls != wantCalls {
				t.Errorf("reloads = %d want %d", calls, wantCalls)
			}
			got, readErr := os.ReadFile(m.runtimeConfig)
			if readErr != nil || string(got) != before {
				t.Fatal("previous config not restored")
			}
			if routing && (base.mode != "global" || base.selected != "Node B") {
				t.Fatal("routing not restored")
			}
		})
	}
}
