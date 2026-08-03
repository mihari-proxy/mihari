package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/state"
)

type Options struct {
	Token string
	Store *state.Store
}

type Server struct {
	token string
	store *state.Store
	http  *http.Server
}

func New(options Options) *Server {
	server := &Server{token: options.Token, store: options.Store}
	server.http = &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.status)
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

func (s *Server) status(writer http.ResponseWriter, _ *http.Request) {
	snapshot := s.store.Load()
	writeJSON(writer, http.StatusOK, protocol.Status{
		Schema:          "mihari/v1",
		ProtocolVersion: "v1",
		DaemonVersion:   snapshot.Version,
		Revision:        snapshot.Revision,
		Health:          snapshot.Health,
		StartedAt:       snapshot.StartedAt,
	})
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
