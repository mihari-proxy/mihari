package preferences

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestService_ConnectionsUpdatePreservesSavedLogLevels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	if err := os.WriteFile(path, []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"log_levels":["debug","warn"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := Open(path)
	if err != nil {
		t.Fatalf("open saved log selection: %v", err)
	}
	if _, err := service.Update(context.Background(), Update{ConnectionsColumns: []string{"host", "chain"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		LogLevels []string `json:"log_levels"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved.LogLevels, []string{"debug", "warn"}) {
		t.Fatalf("column update lost log selection: %s", raw)
	}
}

func TestService_LogLevelsPersistWithoutReplacingColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	service, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	columns := []string{"host", "chain"}
	if _, err := service.Update(context.Background(), Update{ConnectionsColumns: columns}); err != nil {
		t.Fatal(err)
	}
	for mask := 1; mask < 16; mask++ {
		var levels []string
		for i, level := range []string{"debug", "info", "warn", "error"} {
			if mask&(1<<i) != 0 {
				levels = append(levels, level)
			}
		}
		want := slices.Clone(levels)
		got, err := service.Update(context.Background(), Update{LogLevels: levels})
		if err != nil {
			t.Fatal(err)
		}
		got.LogLevels[0] = "mutated-return"
		levels[0] = "mutated-input"
		snapshot := service.Snapshot()
		snapshot.LogLevels[0] = "mutated-snapshot"
		reopened, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(reopened.Snapshot().LogLevels, want) || !slices.Equal(service.Snapshot().LogLevels, want) || !slices.Equal(reopened.Snapshot().ConnectionsColumns, columns) {
			t.Fatalf("mask %d: state lost or aliased: %+v", mask, reopened.Snapshot())
		}
	}
}

func TestService_InvalidPersistedLogLevelsAreNotRecovered(t *testing.T) {
	for _, levels := range []string{`[]`, `["silent"]`, `["warn","warn"]`} {
		path := filepath.Join(t.TempDir(), "tui.json")
		raw := []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"log_levels":` + levels + `}`)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); !errors.Is(err, ErrInvalidLogLevels) {
			t.Fatalf("levels=%s err=%v", levels, err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(raw) {
			t.Fatal("invalid data was silently rewritten")
		}
	}
}

func TestService_LegacyPreferencesDefaultToAllLevels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	if err := os.WriteFile(path, []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(service.Snapshot().LogLevels, []string{"debug", "info", "warn", "error"}) {
		t.Fatal("legacy preferences did not default to all")
	}
}

func TestService_InvalidLogLevelsLeaveCommittedPreferencesUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	service, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), Update{LogLevels: []string{"warn"}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, levels := range [][]string{{}, {"debug", "debug"}, {"warning"}, {"silent"}, {""}} {
		if _, err := service.Update(context.Background(), Update{ConnectionsColumns: []string{"chain"}, LogLevels: levels}); !errors.Is(err, ErrInvalidLogLevels) {
			t.Fatalf("levels=%v err=%v", levels, err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) || !slices.Equal(service.Snapshot().LogLevels, []string{"warn"}) {
			t.Fatal("invalid update changed committed state")
		}
	}
}

func TestService_LogSaveFailureKeepsPublishedSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	service, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), Update{LogLevels: []string{"warn"}}); err != nil {
		t.Fatal(err)
	}
	// A directory at the target deterministically prevents atomic replacement.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), Update{LogLevels: []string{"error"}}); err == nil {
		t.Fatal("expected write failure")
	}
	if !slices.Equal(service.Snapshot().LogLevels, []string{"warn"}) {
		t.Fatal("write failure published uncommitted choice")
	}
}
