package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

type Options struct {
	Token   string
	Store   *state.Store
	Runtime RuntimeAPI
	Now     func() time.Time
}

type Server struct {
	token           string
	store           *state.Store
	runtime         RuntimeAPI
	now             func() time.Time
	shutdownTimeout time.Duration
	http            *http.Server
}

func New(options Options) *Server {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	server := &Server{token: options.Token, store: options.Store, runtime: options.Runtime, now: now, shutdownTimeout: 5 * time.Second}
	server.http = &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.status)
	s.runtimeRoutes(mux)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
	}
	if s.runtime != nil {
		status.Capabilities = sortedUnique(s.runtime.Capabilities())
		if runtime, ok := s.runtime.(onboardingAPI); ok {
			if onboardingStatus, err := runtime.OnboardingStatus(request.Context()); err == nil {
				status.SetupRequired = !onboardingStatus.Status.Complete
			}
		}
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

	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		// Graceful shutdown is best-effort: if in-flight connections prevent
		// draining within the budget, http.Shutdown returns DeadlineExceeded.
		// On the cancellation path that timeout is expected and must not be
		// surfaced as a daemon error (it flaked the integration suite under
		// -race on slow CI). Only propagate genuine, non-deadline errors.
		if err := s.http.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return nil
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
