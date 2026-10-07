package preferences

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLatencyConcurrency_LegacyDefaults(t *testing.T) {
	for _, block := range []string{"", `,"proxies":{"extra_latency":false,"auto_latency_test":false}`} {
		path := filepath.Join(t.TempDir(), "tui.json")
		if err := os.WriteFile(path, []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"]`+block+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		svc, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := svc.Snapshot().Proxies.LatencyTestConcurrency; got != 5 {
			t.Fatalf("legacy default=%d", got)
		}
	}
}

func TestLatencyConcurrency_PersistsAndLegacyPatchPreservesValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	svc, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 50, 5} {
		prefs := DefaultProxyPreferences()
		prefs.LatencyTestConcurrency = limit
		if _, err := svc.Update(t.Context(), Update{Proxies: &prefs}); err != nil {
			t.Fatal(err)
		}
		// Simulate an older client which knows only the two boolean fields.
		if _, err := svc.Update(t.Context(), Update{Proxies: &ProxyPreferences{}}); err != nil {
			t.Fatal(err)
		}
		svc, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := svc.Snapshot().Proxies; got.LatencyTestConcurrency != limit || got.ExtraLatency || got.AutoLatencyTest {
			t.Fatalf("legacy patch lost limit: %+v", got)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("latency_test_concurrency")) != (limit != 5) {
			t.Fatalf("default should keep old on-disk shape: %s", raw)
		}
	}
	defaults := DefaultProxyPreferences()
	if _, err := svc.Update(t.Context(), Update{Proxies: &defaults}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"proxies"`)) {
		t.Fatalf("default block not omitted: %s", raw)
	}
}

func TestLatencyConcurrency_InvalidUpdateLeavesSnapshotAndFileUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	svc, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := svc.Update(t.Context(), Update{ConnectionsColumns: []string{"host"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{-1, 51} {
		_, err := svc.Update(t.Context(), Update{ConnectionsColumns: []string{"chain"}, Proxies: &ProxyPreferences{LatencyTestConcurrency: limit}})
		if !errors.Is(err, ErrInvalidLatencyConcurrency) {
			t.Fatalf("limit=%d err=%v", limit, err)
		}
		current, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, svc.Snapshot()) || !bytes.Equal(raw, current) {
			t.Fatal("invalid update changed committed preferences")
		}
	}
}

func TestLatencyConcurrency_RejectsInvalidPersistedValue(t *testing.T) {
	for _, limit := range []int{-1, 0, 51} {
		path := filepath.Join(t.TempDir(), "tui.json")
		raw := fmt.Sprintf(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"proxies":{"latency_test_concurrency":%d}}`, limit)
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); !errors.Is(err, ErrInvalidLatencyConcurrency) {
			t.Fatalf("limit=%d err=%v", limit, err)
		}
	}
}
