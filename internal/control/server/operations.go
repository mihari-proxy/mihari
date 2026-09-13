package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const maxObservedOperations = 256

type observedOperation struct {
	active int
	order  uint64
}

// Observation is independent of mutation caching. Concurrent requests with one
// ID keep it running until every handler (including the execution owner) returns.
type operationObservation struct {
	mu        sync.Mutex
	entries   map[string]*observedOperation
	sequence  uint64
	untracked int
}

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
	entry.active++
	o.sequence++
	entry.order = o.sequence
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		entry.active--
	}
}

func (o *operationObservation) state(id string) string {
	id = observationKey(id)
	o.mu.Lock()
	defer o.mu.Unlock()
	// A saturated request may share an ID with a later tracked request. Never
	// claim settlement until every untracked handler has also returned.
	if o.untracked > 0 {
		return "unknown"
	}
	if entry := o.entries[id]; entry != nil {
		if entry.active > 0 {
			return "running"
		}
		return "finished"
	}
	return "unknown"
}

func observationKey(id string) string {
	digest := sha256.Sum256([]byte(id))
	return hex.EncodeToString(digest[:])
}

func (s *Server) operationStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("operation_id")
	if !requireOperationID(w, id) {
		return
	}
	writeJSON(w, http.StatusOK, protocol.OperationStatus{Schema: "mihari/v1", OperationID: id, State: s.operations.state(id)})
}
