package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// HistoryOptions bounds process-owned diagnostic history. Zero limits use daemon defaults.
type HistoryOptions struct {
	InstanceID string
	MaxRecords int
	MaxBytes   int
	Now        func() time.Time
}

// History retains immutable occurrence snapshots for one process lifetime.
type History struct {
	mu         sync.Mutex
	instanceID string
	maxRecords int
	maxBytes   int
	now        func() time.Time
	sequence   uint64
	records    []protocol.Diagnostic
	bytes      int
}

// NewHistory constructs an independent process history without touching disk.
func NewHistory(options HistoryOptions) (*History, error) {
	if options.InstanceID == "" {
		var instance [16]byte
		if _, err := rand.Read(instance[:]); err != nil {
			return nil, fmt.Errorf("create diagnostic instance: %w", err)
		}
		options.InstanceID = hex.EncodeToString(instance[:])
	}
	if options.MaxRecords <= 0 {
		options.MaxRecords = 256
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 32 << 20
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &History{instanceID: options.InstanceID, maxRecords: options.MaxRecords, maxBytes: options.MaxBytes, now: options.Now}, nil
}

// Add assigns a new occurrence identity even when another record has the same text.
func (h *History) Add(record protocol.Diagnostic) protocol.Diagnostic {
	if record.Time.IsZero() {
		record.Time = h.now()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence++
	record.InstanceID, record.Sequence = h.instanceID, h.sequence
	record.ID = h.instanceID + ":" + strconv.FormatUint(h.sequence, 10)
	if record.State == "" {
		record.State = protocol.DiagnosticAvailable
	}
	h.records = append(h.records, record)
	h.bytes += diagnosticSize(record)
	for len(h.records) > h.maxRecords || h.bytes > h.maxBytes {
		h.bytes -= diagnosticSize(h.records[0])
		h.records[0] = protocol.Diagnostic{}
		h.records = h.records[1:]
	}
	return record
}

// List returns metadata after a sequence; a foreign instance starts a new timeline.
func (h *History) List(instance string, after uint64, limit int) protocol.DiagnosticList {
	h.mu.Lock()
	defer h.mu.Unlock()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	result := protocol.DiagnosticList{Schema: "mihari.diagnostics/v1", InstanceID: h.instanceID, LatestSequence: h.sequence, NextSequence: after, Records: []protocol.Diagnostic{}, State: protocol.DiagnosticAvailable}
	if instance != "" && instance != h.instanceID {
		result.State = protocol.DiagnosticRestarted
		after, result.NextSequence = 0, 0
	}
	result.OldestSequence = h.sequence + 1
	if len(h.records) > 0 {
		result.OldestSequence = h.records[0].Sequence
	}
	result.LostBefore = result.OldestSequence > 0 && after < result.OldestSequence-1
	for _, record := range h.records {
		if record.Sequence <= after {
			continue
		}
		if len(result.Records) == limit {
			result.HasMore = true
			break
		}
		result.Records = append(result.Records, record.Reference())
		result.NextSequence = record.Sequence
	}
	return result
}

// Get returns a copy of one occurrence, distinguishing expiry from an unknown ID.
func (h *History) Get(id string) protocol.DiagnosticResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: protocol.DiagnosticUnknown}
	instance, number, ok := strings.Cut(id, ":")
	if !ok || instance == "" {
		return result
	}
	sequence, err := strconv.ParseUint(number, 10, 64)
	if err != nil || sequence == 0 {
		return result
	}
	if instance != h.instanceID {
		result.State = protocol.DiagnosticRestarted
		return result
	}
	for _, record := range h.records {
		if record.ID == id {
			result.State, result.Diagnostic = protocol.DiagnosticAvailable, &record
			return result
		}
	}
	if sequence <= h.sequence {
		result.State = protocol.DiagnosticExpired
	}
	return result
}

func diagnosticSize(record protocol.Diagnostic) int {
	return len(record.ID) + len(record.InstanceID) + len(record.Severity) + len(record.Component) + len(record.Event) + len(record.OperationID) + len(record.Operation) + len(record.Object) + len(record.Code) + len(record.Summary) + len(record.Detail) + len(record.State) + len(record.TruncationReason) + len(record.RetrievalError)
}
