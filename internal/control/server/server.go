package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/state"
)

type Options struct {
	Onboarding         OnboardingAPI
	Token              string
	Store              *state.Store
	Runtime            RuntimeAPI
	Now                func() time.Time
	SnapshotSource     logging.MachineSnapshotSource
	SnapshotID         func() string
	DiagnosticReporter diagnostics.Reporter
}

type Server struct {
	onboarding         OnboardingAPI
	operations         operationObservation
	token              string
	store              *state.Store
	runtime            RuntimeAPI
	now                func() time.Time
	snapshotSource     logging.MachineSnapshotSource
	snapshotID         func() string
	diagnosticReporter diagnostics.Reporter
	snapshotCtx        context.Context
	snapshotCancel     context.CancelFunc
	snapshotWG         sync.WaitGroup
	snapshotHandlers   sync.WaitGroup
	handlers           sync.WaitGroup
	snapshotLifecycle  sync.Mutex
	snapshotClosing    bool
	snapshotGate       snapshotGate
	shutdownTimeout    time.Duration
	http               *http.Server
}

// New assembles local control handlers and selects the injected or runtime onboarding surface.
func New(options Options) *Server {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	snapshotCtx, snapshotCancel := context.WithCancel(context.Background())
	server := &Server{
		onboarding:         options.Onboarding,
		token:              options.Token,
		store:              options.Store,
		runtime:            options.Runtime,
		now:                now,
		snapshotSource:     options.SnapshotSource,
		snapshotID:         options.SnapshotID,
		diagnosticReporter: options.DiagnosticReporter,
		snapshotCtx:        snapshotCtx,
		snapshotCancel:     snapshotCancel,
		shutdownTimeout:    5 * time.Second,
	}
	server.http = &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if server.onboarding == nil {
		server.onboarding, _ = options.Runtime.(onboardingAPI)
	}
	return server
}

// Handler authenticates local control requests and ties their lifetime to server shutdown.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("GET /v1/operations/{operation_id}", s.operationStatus)
	s.runtimeRoutes(mux)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		s.snapshotLifecycle.Lock()
		if s.snapshotClosing {
			s.snapshotLifecycle.Unlock()
			writeJSON(writer, http.StatusServiceUnavailable, protocol.NewError(protocol.CodeInvalidState, "local control is stopping", nil))
			return
		}
		s.handlers.Add(1)
		s.snapshotLifecycle.Unlock()
		defer s.handlers.Done()
		requestCtx, cancel := context.WithCancel(request.Context())
		stop := context.AfterFunc(s.snapshotCtx, cancel)
		defer stop()
		defer cancel()
		request = request.WithContext(requestCtx)
		want := "Bearer " + s.token
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte(want)) != 1 {
			writeJSON(writer, http.StatusUnauthorized, protocol.NewError(
				protocol.CodePermissionDenied,
				"control authentication failed",
				nil,
			))
			return
		}
		mux.ServeHTTP(writer, request)
	})
}

// status publishes capabilities and confirmed readiness without inferring setup from a failed read.
func (s *Server) status(writer http.ResponseWriter, request *http.Request) {
	snapshot := s.store.Load()
	status := protocol.Status{
		Schema:          "mihari/v1",
		ProtocolVersion: "v1",
		DaemonVersion:   snapshot.Version,
		Revision:        snapshot.Revision,
		Health:          snapshot.Health,
		LastError:       snapshot.LastError,
		StartedAt:       snapshot.StartedAt,
		PID:             os.Getpid(),
	}
	if s.runtime != nil {
		status.Capabilities = append(status.Capabilities, protocol.OperationStatusCapability)
		for _, capability := range s.runtime.Capabilities() {
			if capability != protocol.MachineLogSnapshotCapability {
				status.Capabilities = append(status.Capabilities, capability)
			}
		}
		status.Capabilities = sortedUnique(status.Capabilities)
		// A failed read does not prove setup is required. Keep the zero value;
		// only runtimes without a readiness probe use the historical marker.
		switch runtime := s.runtime.(type) {
		case setupRequiredAPI:
			if required, err := runtime.SetupRequired(request.Context()); err == nil {
				status.SetupRequired = required
			}
		case onboardingAPI:
			if onboardingStatus, err := runtime.OnboardingStatus(request.Context()); err == nil {
				status.SetupRequired = !onboardingStatus.Status.Complete
			}
		}
	}
	if s.snapshotSource != nil {
		status.Capabilities = sortedUnique(append(status.Capabilities, protocol.MachineLogSnapshotCapability))
	}
	if s.runtime == nil && s.onboarding != nil {
		status.Capabilities = sortedUnique(append(status.Capabilities, protocol.CapabilityOnboarding, protocol.OperationStatusCapability))
		status.SetupRequired = true
	}
	if snapshot.Config.Status != "" {
		status.Config = &protocol.ConfigStatus{
			Status: snapshot.Config.Status, DesiredRevision: snapshot.Config.DesiredRevision,
			ObservedRevision: snapshot.Config.ObservedRevision, LastError: snapshot.Config.LastError,
		}
	}
	writeJSON(writer, http.StatusOK, status)
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.Serve(listener)
	}()

	var serveErr, shutdownErr error
	select {
	case serveErr = <-errCh:
	case <-ctx.Done():
		s.cancelSnapshots()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		shutdownErr = s.http.Shutdown(shutdownCtx)
		cancel()
		if errors.Is(shutdownErr, context.DeadlineExceeded) {
			shutdownErr = nil
		}
		// Request-context cancellation must precede every snapshot-owner join.
		shutdownErr = errors.Join(shutdownErr, s.http.Close())
		serveErr = <-errCh
	}
	s.cancelSnapshots()
	closeErr := s.http.Close()
	s.handlers.Wait()
	s.snapshotHandlers.Wait()
	s.snapshotWG.Wait()
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr, closeErr)
}

func (s *Server) cancelSnapshots() {
	s.snapshotLifecycle.Lock()
	s.snapshotClosing = true
	if s.snapshotCancel != nil {
		s.snapshotCancel()
	}
	s.snapshotLifecycle.Unlock()
}
func (s *Server) beginSnapshot() bool {
	s.snapshotLifecycle.Lock()
	defer s.snapshotLifecycle.Unlock()
	if s.snapshotClosing {
		return false
	}
	s.snapshotHandlers.Add(1)
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
