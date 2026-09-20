package preferences

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestService_ConnectionsUpdatePreservesProxyPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	if err := os.WriteFile(path, []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"proxies":{"extra_latency":false,"auto_latency_test":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	svc, err := Open(path)
	if err != nil {
		t.Fatalf("open saved proxy preferences: %v", err)
	}
	if _, err := svc.Update(context.Background(), Update{ConnectionsColumns: []string{"chain"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Proxies map[string]bool `json:"proxies"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"extra_latency", "auto_latency_test"} {
		if value, exists := saved.Proxies[key]; !exists || value {
			t.Fatalf("saved %s=%v, exists=%v; explicit false lost: %s", key, value, exists, raw)
		}
	}
}

func TestService_ProxyDefaultsAndRoundTripPreserveColumns(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "legacy"}[legacy], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tui.json")
			if legacy {
				if err := os.WriteFile(path, []byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if p := s.Snapshot().Proxies; !p.ExtraLatency || !p.AutoLatencyTest {
				t.Fatalf("defaults=%+v", p)
			}
			columns := s.Snapshot().ConnectionsColumns
			for _, want := range []ProxyPreferences{{false, false}, {true, false}, {false, true}, {true, true}} {
				if _, err = s.Update(context.Background(), Update{Proxies: &want}); err != nil {
					t.Fatal(err)
				}
				s, err = Open(path)
				if err != nil {
					t.Fatal(err)
				}
				got := s.Snapshot()
				if got.Proxies != want || !reflect.DeepEqual(got.ConnectionsColumns, columns) {
					t.Fatalf("got=%+v want=%+v columns=%v", got, want, columns)
				}
			}
		})
	}
}
