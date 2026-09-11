//go:build linux || darwin

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

// The controller owns provider contents; Mihari serializes its refresh and
// publishes a new revision only when that native update succeeds.
func TestUnixProviderContract_NativeRefresh(t *testing.T) {
	for _, tc := range []struct {
		name         string
		revision     uint64
		status       int
		wantCode     protocol.ErrorCode
		wantCalls    int32
		wantRevision uint64
	}{
		{name: "success and deduplication", revision: 11, status: http.StatusNoContent, wantCalls: 1, wantRevision: 12},
		{name: "stale revision", revision: 10, status: http.StatusNoContent, wantCode: protocol.CodeRevisionConflict, wantRevision: 11},
		{name: "upstream failure is cached", revision: 11, status: http.StatusBadGateway, wantCode: protocol.CodeUpstreamFailure, wantCalls: 1, wantRevision: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const name = "domains / custom"
			const secret = "fixture-controller-secret"
			var updates, ruleCount atomic.Int32
			ruleCount.Store(1)
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+secret {
					t.Error("missing controller authentication")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch {
				case r.Method == http.MethodPut && r.URL.EscapedPath() == "/providers/rules/domains%20%2F%20custom":
					updates.Add(1)
					if tc.status == http.StatusNoContent {
						ruleCount.Store(2)
					}
					w.WriteHeader(tc.status)
					if tc.status != http.StatusNoContent {
						_, _ = w.Write([]byte("private upstream detail"))
					}
				case r.Method == http.MethodGet && r.URL.Path == "/providers/rules":
					if err := json.NewEncoder(w).Encode(mihomo.RuleProviders{Providers: map[string]mihomo.RuleProvider{name: {Name: name, RuleCount: int(ruleCount.Load())}}}); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected controller request: %s %s", r.Method, r.URL.EscapedPath())
					http.NotFound(w, r)
				}
			}))
			defer controller.Close()
			store := state.NewStore(state.Snapshot{Revision: 11})
			manager := runtimeapi.New(runtimeapi.Options{Store: store, Coordinator: state.NewCoordinator(store), Controller: mihomo.NewClient(controller.URL, secret, controller.Client())})
			operation := runtimeapi.Operation{ID: "refresh-provider", Source: "cli", IfRevision: &tc.revision}
			for attempt := 0; attempt < 2; attempt++ {
				err := manager.UpdateRuleProvider(context.Background(), operation, name)
				if tc.wantCode == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var apiError protocol.APIError
					if !errors.As(err, &apiError) || apiError.Code != tc.wantCode {
						t.Fatalf("attempt %d: err=%v want %s", attempt, err, tc.wantCode)
					}
					if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private upstream detail") {
						t.Fatal("provider error exposed upstream details")
					}
				}
			}
			if got := manager.Snapshot().Revision; got != tc.wantRevision {
				t.Fatalf("revision=%d want %d", got, tc.wantRevision)
			}
			if got := updates.Load(); got != tc.wantCalls {
				t.Fatalf("updates=%d want %d", got, tc.wantCalls)
			}
			providers, err := manager.RuleProviders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if tc.wantCode == "" {
				wantCount = 2
			}
			if got := providers.Providers[name].RuleCount; got != wantCount {
				t.Fatalf("visible provider rule count=%d want %d", got, wantCount)
			}
		})
	}
}
