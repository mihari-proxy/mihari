package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
)

const maxObservedOperations = 256

type observedOperation struct {
	active   int
	order    uint64
	progress *observedProgress
}

type observedProgress struct {
	phase    string
	received int64
	total    int64
	started  time.Time
}

// Observation is independent of mutation caching. Concurrent requests with one
// ID keep it running until every handler (including the execution owner) returns.
type operationObservation struct {
	mu        sync.Mutex
	entries   map[string]*observedOperation
	sequence  uint64
	untracked int
	now       func() time.Time
}

// begin tracks an active handler and returns its release callback; saturated records stay unknown.
func (o *operationObservation) begin(id string) func() {
	id = observationKey(id)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.entries == nil {
		o.entries = make(map[string]*observedOperation)
	}
	entry := o.entries[id]
	if entry == nil {
		if len(o.entries) >= maxObservedOperations {
			oldest := ""
			var order uint64
			for key, candidate := range o.entries {
				if candidate.active == 0 && (oldest == "" || candidate.order < order) {
					oldest, order = key, candidate.order
				}
			}
			if oldest == "" {
				o.untracked++
				return func() { o.mu.Lock(); o.untracked--; o.mu.Unlock() }
			} // Saturated observations remain unknown.
			delete(o.entries, oldest)
		}
		entry = &observedOperation{}
		o.entries[id] = entry
	}
	if entry.active == 0 {
		entry.progress = nil
	}
	entry.active++
	o.sequence++
	entry.order = o.sequence
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		entry.active--
	}
}

// state reports process-local settlement, conservatively returning unknown for untracked work.
func (o *operationObservation) state(id string) string {
	return o.snapshot(id).State
}

// noteProgress records the current core-install phase for a still-running operation.
func (o *operationObservation) noteProgress(id string, progress core.Progress) {
	switch progress.Phase {
	case protocol.ProgressPhaseDownloading, protocol.ProgressPhaseExtracting, protocol.ProgressPhaseChecking:
	default:
		return
	}
	id = observationKey(id)
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[id]
	if entry == nil || entry.active == 0 {
		return
	}
	if entry.progress == nil || entry.progress.phase != progress.Phase {
		entry.progress = &observedProgress{phase: progress.Phase, started: o.clockLocked()}
	}
	entry.progress.received = progress.Received
	entry.progress.total = progress.Total
}

// snapshot reports settlement and, while running, the latest core-install progress.
func (o *operationObservation) snapshot(id string) protocol.OperationStatus {
	key := observationKey(id)
	o.mu.Lock()
	defer o.mu.Unlock()
	status := protocol.OperationStatus{Schema: "mihari/v1", OperationID: id, State: "unknown"}
	// A saturated request may share an ID with a later tracked request. Never
	// claim settlement until every untracked handler has also returned.
	if o.untracked > 0 {
		return status
	}
	entry := o.entries[key]
	if entry == nil {
		return status
	}
	if entry.active > 0 {
		status.State = "running"
	} else {
		status.State = "finished"
	}
	if status.State != "running" || entry.progress == nil {
		return status
	}
	elapsed := o.clockLocked().Sub(entry.progress.started)
	if elapsed < 0 {
		elapsed = 0
	}
	reported := &protocol.OperationProgress{Phase: entry.progress.phase, ElapsedMilliseconds: elapsed.Milliseconds()}
	if entry.progress.phase == protocol.ProgressPhaseDownloading {
		received := entry.progress.received
		reported.ReceivedBytes = &received
		if entry.progress.total > 0 {
			total := entry.progress.total
			reported.TotalBytes = &total
		}
	}
	status.Progress = reported
	return status
}

func (o *operationObservation) clockLocked() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

// observationKey bounds retained identifiers without storing their original text.
func observationKey(id string) string {
	digest := sha256.Sum256([]byte(id))
	return hex.EncodeToString(digest[:])
}

// operationStatus validates an operation ID and observes it without executing or replaying work.
func (s *Server) operationStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("operation_id")
	if !s.requireOperationID(r.Context(), w, id) {
		return
	}
	writeJSON(w, http.StatusOK, s.operations.snapshot(id))
}
