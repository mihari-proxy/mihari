package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestOwnerScan_RuntimeClassifiersPreservePrivateCauses(t *testing.T) {
	cause := errors.New("private persistence cause")
	for name, err := range map[string]error{
		"preferences": preferenceMutationError(cause),
		"onboarding":  mapPersistError(cause),
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(err, cause) {
				t.Fatalf("cause was lost: %v", err)
			}
			if strings.Contains(err.Error(), cause.Error()) {
				t.Fatalf("public error exposed private cause: %v", err)
			}
		})
	}

	invalid := errors.Join(preferences.ErrInvalidColumns, cause)
	err := preferenceMutationError(invalid)
	var api protocol.APIError
	if !errors.Is(err, cause) || !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
		t.Fatalf("invalid preference classification/cause=%v", err)
	}
}

type ownerScanGeoIP struct{ closeErr error }

func (g ownerScanGeoIP) Status() geoip.Status                    { return geoip.Status{} }
func (g ownerScanGeoIP) Lookup(netip.Addr) (geoip.Record, error) { return geoip.Record{}, nil }
func (g ownerScanGeoIP) Close() error                            { return g.closeErr }

func TestOwnerScan_RuntimeReportsRecoverableLifecycleFailures(t *testing.T) {
	startupCause := errors.New("restore system proxy private cause")
	closeCause := errors.New("close GeoIP private cause")
	supervisorCause := errors.New("supervisor stopped")
	var mu sync.Mutex
	var records []diagnostics.Record
	manager := newTestManager(Options{
		Settings:   defaultSysProxySettings(true),
		SysProxy:   &sysproxy.FakeBackend{EnableErr: startupCause},
		GeoIP:      ownerScanGeoIP{closeErr: closeCause},
		Supervisor: &fakeSupervisor{run: func(context.Context) error { return supervisorCause }},
		DiagnosticReporter: func(_ context.Context, record diagnostics.Record) {
			mu.Lock()
			records = append(records, record)
			mu.Unlock()
		},
	})
	if err := manager.Run(context.Background()); !errors.Is(err, supervisorCause) {
		t.Fatalf("run result changed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []error{startupCause, closeCause} {
		found := false
		for _, record := range records {
			if errors.Is(record.Err, want) && record.Level == slog.LevelWarn {
				found = true
			}
		}
		if !found {
			t.Fatalf("recoverable failure %v was not reported at WARN: %#v", want, records)
		}
	}
}

func TestOwnerScan_RuntimeCleanupCancellationIsInfo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var records []diagnostics.Record
	manager := newTestManager(Options{
		GeoIP:    ownerScanGeoIP{closeErr: context.Canceled},
		SysProxy: &sysproxy.FakeBackend{},
		Supervisor: &fakeSupervisor{run: func(context.Context) error {
			cancel()
			return nil
		}},
		DiagnosticReporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) },
	})
	if err := manager.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Event != "cleanup.failed" || records[0].Level != slog.LevelInfo || !errors.Is(records[0].Err, context.Canceled) {
		t.Fatalf("cleanup cancellation record=%#v", records)
	}
}

func TestOwnerScan_RuntimePreservesActualSupervisorFailureDuringCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cause := errors.New("supervisor failed while shutdown began")
	manager := newTestManager(Options{
		SysProxy: &sysproxy.FakeBackend{},
		Supervisor: &fakeSupervisor{run: func(context.Context) error {
			cancel()
			return cause
		}},
	})
	if err := manager.Run(ctx); !errors.Is(err, cause) {
		t.Fatalf("simultaneous supervisor cause was masked: %v", err)
	}
}

func TestOwnerScan_SystemProxyStatusPreservesBackendCause(t *testing.T) {
	cause := errors.New("registry fixture private cause")
	manager := newTestManager(Options{
		Settings: config.Settings{MixedAddr: "127.0.0.1:7890"},
		SysProxy: &sysproxy.FakeBackend{GetErr: cause},
	})
	_, err := manager.SystemProxyStatus(context.Background())
	var api protocol.APIError
	if !errors.Is(err, cause) || !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure {
		t.Fatalf("system proxy classification/cause=%v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("public error exposed backend cause: %v", err)
	}
}
