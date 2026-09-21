package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type applicationUpdateAPI interface {
	PrepareApplicationUpdate(context.Context, string) error
	ReleaseApplicationUpdate(string) error
}

type applicationUpdateLease struct {
	id, key string
	owner   transport.UpdateOwner
	cancel  context.CancelFunc
}

func (s *Server) prepareApplicationUpdate(w http.ResponseWriter, r *http.Request) {
	api, ok := s.runtime.(applicationUpdateAPI)
	if !ok || !transport.UpdatePreparationAvailable || s.updateRuntimeJob == "" {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "application update preparation unavailable"})
		return
	}
	var request struct {
		OperationID string `json:"operation_id"`
	}
	if !s.decodeControlJSON(w, r, &request) || !s.requireOperationID(r.Context(), w, request.OperationID) {
		return
	}
	if len(request.OperationID) > 128 {
		s.writeInvalidArgument(r.Context(), w, "operation_id is too long")
		return
	}
	owner, err := s.openUpdateOwner(r.Context())
	if err != nil {
		w.Header().Set("Connection", "close")
		var apiErr protocol.APIError
		if !errors.As(err, &apiErr) {
			err = diagnostics.Wrap(protocol.APIError{Code: protocol.CodePermissionDenied, Message: "authorize application updater"}, err)
		}
		s.writeControlError(r.Context(), w, err)
		return
	}
	retained := false
	defer func() {
		if !retained {
			s.reportUpdateClose(r.Context(), owner.Close())
		}
	}()
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if s.updateLease != nil && (s.updateLease.id != request.OperationID || s.updateLease.key != owner.Key()) {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "another updater owns preparation"})
		return
	}
	info, err := applicationUpdateIdentity(r.Context(), request.OperationID)
	if err != nil {
		s.writeControlError(r.Context(), w, err)
		return
	}
	info.RuntimeJob = s.updateRuntimeJob
	// Bound the drain, but never expire a permission already returned to its owner.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err = api.PrepareApplicationUpdate(ctx, request.OperationID); err != nil {
		s.writeControlError(ctx, w, err)
		return
	}
	if s.updateLease == nil {
		watchCtx, stop := context.WithCancel(s.snapshotCtx)
		lease := &applicationUpdateLease{id: request.OperationID, key: owner.Key(), owner: owner, cancel: stop}
		s.updateLease = lease
		retained = true
		s.updateWG.Add(1)
		go s.watchUpdateOwner(watchCtx, api, lease)
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) releaseApplicationUpdate(w http.ResponseWriter, r *http.Request) {
	api, ok := s.runtime.(applicationUpdateAPI)
	if !ok {
		s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodeInvalidState, Message: "application update preparation unavailable"})
		return
	}
	owner, err := s.openUpdateOwner(r.Context())
	if err != nil {
		s.writeControlError(r.Context(), w, err)
		return
	}
	defer func() { s.reportUpdateClose(r.Context(), owner.Close()) }()
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if lease := s.updateLease; lease != nil {
		if lease.id != r.PathValue("operation_id") || lease.key != owner.Key() {
			s.writeControlError(r.Context(), w, protocol.APIError{Code: protocol.CodePermissionDenied, Message: "update preparation belongs to another process"})
			return
		}
		if err := api.ReleaseApplicationUpdate(lease.id); err != nil {
			s.writeControlError(r.Context(), w, err)
			return
		}
		lease.cancel()
		s.updateLease = nil
	}
	writeJSON(w, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: r.PathValue("operation_id")})
}

func (s *Server) watchUpdateOwner(ctx context.Context, api applicationUpdateAPI, lease *applicationUpdateLease) {
	defer s.updateWG.Done()
	defer func() { s.reportUpdateClose(context.Background(), lease.owner.Close()) }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	reportedObservationFailure := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		exited, err := lease.owner.Exited(ctx)
		if err != nil {
			if !reportedObservationFailure && !diagnostics.NormalCancellation(ctx, err) {
				s.reportUpdateClose(ctx, err)
				reportedObservationFailure = true
			}
			continue
		} // An observation failure never proves that killing permission has expired.
		if !exited {
			continue
		}
		s.updateMu.Lock()
		if s.updateLease == lease {
			s.reportUpdateClose(ctx, api.ReleaseApplicationUpdate(lease.id))
			s.updateLease = nil
		}
		s.updateMu.Unlock()
		return
	}
}

func (s *Server) reportUpdateClose(ctx context.Context, err error) {
	if err != nil && s.diagnosticReporter != nil {
		s.diagnosticReporter(ctx, diagnostics.Record{Component: "control.update", Event: "owner.cleanup.failed", Level: slog.LevelWarn, Err: err})
	}
}
