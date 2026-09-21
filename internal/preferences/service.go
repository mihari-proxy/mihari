package preferences

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync/atomic"

	"github.com/mihari-proxy/mihari/internal/config"
)

const (
	fileSchema                = "mihari.tui-preferences/v1"
	maxFileSizeBytes          = 1 << 20
	defaultLatencyConcurrency = 5
	maxLatencyConcurrency     = 50
)

var (
	ErrInvalidColumns = errors.New("invalid connections columns")
	// ErrInvalidLogLevels identifies an empty, unknown, or duplicate display-level selection.
	ErrInvalidLogLevels = errors.New("invalid log display levels")
	// ErrInvalidLatencyConcurrency identifies an out-of-range latency test limit.
	ErrInvalidLatencyConcurrency = errors.New("latency test concurrency must be between 1 and 50")
	defaultLogLevels             = []string{"debug", "info", "warn", "error"}
	defaultColumns               = []string{"host", "network", "source", "destination", "chain", "rule", "traffic"}
	connectionColumnIDs          = map[string]struct{}{
		"host": {}, "network": {}, "source": {}, "destination": {}, "chain": {},
		"rule": {}, "process": {}, "upload": {}, "download": {}, "traffic": {}, "start": {},
	}
)

type Preferences struct {
	ConnectionsColumns []string
	Proxies            ProxyPreferences
	// LogLevels is the committed set of canonical levels restored by new TUI sessions.
	LogLevels []string
}

// ProxyPreferences controls Proxies presentation and automatic latency tests.
type ProxyPreferences struct {
	ExtraLatency           bool `json:"extra_latency"`
	AutoLatencyTest        bool `json:"auto_latency_test"`
	LatencyTestConcurrency int  `json:"latency_test_concurrency,omitempty"`
}

// DefaultProxyPreferences enables both switches and keeps five concurrent tests.
func DefaultProxyPreferences() ProxyPreferences {
	return ProxyPreferences{ExtraLatency: true, AutoLatencyTest: true, LatencyTestConcurrency: defaultLatencyConcurrency}
}

type Update struct {
	ConnectionsColumns []string
	Proxies            *ProxyPreferences
	// LogLevels replaces the saved set when non-nil; an empty set is invalid.
	LogLevels []string
}

type Service struct {
	path     string
	snapshot atomic.Value
}

type document struct {
	Schema             string            `json:"schema"`
	ConnectionsColumns []string          `json:"connections_columns"`
	Proxies            *ProxyPreferences `json:"proxies,omitempty"`
	LogLevels          []string          `json:"log_levels"`
}

func Open(path string) (*Service, error) {
	preferences := Preferences{ConnectionsColumns: append([]string(nil), defaultColumns...), Proxies: DefaultProxyPreferences(), LogLevels: append([]string(nil), defaultLogLevels...)}
	raw, err := readFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read TUI preferences: %w", err)
	}
	if err == nil {
		defaults := DefaultProxyPreferences()
		persisted := document{Proxies: &defaults}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decodeErr := decoder.Decode(&persisted); decodeErr != nil {
			return nil, fmt.Errorf("decode TUI preferences: %w", decodeErr)
		}
		if decodeErr := decoder.Decode(&struct{}{}); !errors.Is(decodeErr, io.EOF) {
			return nil, errors.New("decode TUI preferences: expected one JSON object")
		}
		if persisted.Schema != fileSchema {
			return nil, errors.New("decode TUI preferences: unsupported schema")
		}
		if validateErr := validateColumns(persisted.ConnectionsColumns); validateErr != nil {
			return nil, fmt.Errorf("decode TUI preferences: %w", validateErr)
		}
		preferences.ConnectionsColumns = append([]string(nil), persisted.ConnectionsColumns...)
		if persisted.Proxies != nil {
			if err := ValidateLatencyConcurrency(persisted.Proxies.LatencyTestConcurrency); err != nil {
				return nil, fmt.Errorf("decode TUI preferences: %w", err)
			}
			preferences.Proxies = *persisted.Proxies
		}
		if persisted.LogLevels != nil {
			if err := ValidateLogLevels(persisted.LogLevels); err != nil {
				return nil, fmt.Errorf("decode TUI preferences: %w", err)
			}
			preferences.LogLevels = append([]string(nil), persisted.LogLevels...)
		}
	}
	service := &Service{path: path}
	service.snapshot.Store(preferences)
	return service, nil
}

func (s *Service) Snapshot() Preferences {
	return clone(s.snapshot.Load().(Preferences))
}

func (s *Service) Update(ctx context.Context, update Update) (Preferences, error) {
	if err := ctx.Err(); err != nil {
		return Preferences{}, err
	}
	// An omitted field preserves its committed value. An empty update retains
	// the former invalid-columns error instead of silently writing a no-op.
	if update.ConnectionsColumns != nil || (update.Proxies == nil && update.LogLevels == nil) {
		if err := validateColumns(update.ConnectionsColumns); err != nil {
			return Preferences{}, err
		}
	}
	next := s.Snapshot()
	if update.ConnectionsColumns != nil {
		next.ConnectionsColumns = append([]string(nil), update.ConnectionsColumns...)
	}
	if update.Proxies != nil {
		previousConcurrency := next.Proxies.LatencyTestConcurrency
		next.Proxies = *update.Proxies
		// Older clients replace the two switches without supplying this field.
		if next.Proxies.LatencyTestConcurrency == 0 {
			next.Proxies.LatencyTestConcurrency = previousConcurrency
		}
		if err := ValidateLatencyConcurrency(next.Proxies.LatencyTestConcurrency); err != nil {
			return Preferences{}, err
		}
	}
	if update.LogLevels != nil {
		if err := ValidateLogLevels(update.LogLevels); err != nil {
			return Preferences{}, err
		}
		next.LogLevels = append([]string(nil), update.LogLevels...)
	}
	var proxyOverrides *ProxyPreferences
	if next.Proxies != DefaultProxyPreferences() {
		overrides := next.Proxies
		// Keep the old file shape when only the existing switches differ.
		if overrides.LatencyTestConcurrency == DefaultProxyPreferences().LatencyTestConcurrency {
			overrides.LatencyTestConcurrency = 0
		}
		proxyOverrides = &overrides
	}
	raw, err := json.MarshalIndent(document{Schema: fileSchema, ConnectionsColumns: next.ConnectionsColumns, Proxies: proxyOverrides, LogLevels: next.LogLevels}, "", "  ")
	if err != nil {
		return Preferences{}, fmt.Errorf("encode TUI preferences: %w", err)
	}
	raw = append(raw, '\n')
	if err := config.AtomicWrite(s.path, raw, 0o600); err != nil {
		return Preferences{}, fmt.Errorf("persist TUI preferences: %w", err)
	}
	s.snapshot.Store(next)
	return clone(next), nil
}

// ValidateLatencyConcurrency shares the stored-value bounds with installer migration.
func ValidateLatencyConcurrency(value int) error {
	if value < 1 || value > maxLatencyConcurrency {
		return ErrInvalidLatencyConcurrency
	}
	return nil
}

func validateColumns(columns []string) error {
	if len(columns) == 0 {
		return fmt.Errorf("%w: at least one column is required", ErrInvalidColumns)
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if _, known := connectionColumnIDs[column]; !known {
			return fmt.Errorf("%w: unknown column %q", ErrInvalidColumns, column)
		}
		if _, duplicate := seen[column]; duplicate {
			return fmt.Errorf("%w: duplicate column %q", ErrInvalidColumns, column)
		}
		seen[column] = struct{}{}
	}
	return nil
}

func clone(value Preferences) Preferences {
	value.ConnectionsColumns = append([]string(nil), value.ConnectionsColumns...)
	value.LogLevels = append([]string(nil), value.LogLevels...)
	return value
}

// ValidateLogLevels validates a nonempty set of canonical display level names.
func ValidateLogLevels(levels []string) error {
	if len(levels) == 0 {
		return fmt.Errorf("%w: at least one level is required", ErrInvalidLogLevels)
	}
	seen := make(map[string]bool, len(levels))
	for _, level := range levels {
		if !slices.Contains(defaultLogLevels, level) {
			return fmt.Errorf("%w: unknown level %q", ErrInvalidLogLevels, level)
		}
		if seen[level] {
			return fmt.Errorf("%w: duplicate level %q", ErrInvalidLogLevels, level)
		}
		seen[level] = true
	}
	return nil
}

func readFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxFileSizeBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxFileSizeBytes {
		return nil, errors.New("preferences file is too large")
	}
	return raw, nil
}
