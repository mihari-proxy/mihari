package preferences

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestService_ResetRestoresDefaults(t *testing.T) {
	baseline, err := Open(filepath.Join(t.TempDir(), "defaults.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "preferences.json")
	service, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := DefaultProxyPreferences()
	changed.LatencyTestConcurrency = 27
	if _, err := service.Update(t.Context(), Update{ConnectionsColumns: []string{"process", "host"}, Proxies: &changed, LogLevels: []string{"error"}}); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(service.Snapshot(), baseline.Snapshot()) {
		t.Fatal("fixture did not change preferences")
	}
	got, err := service.Reset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, baseline.Snapshot()) || !reflect.DeepEqual(service.Snapshot(), got) {
		t.Fatal("reset did not publish the complete defaults")
	}
	reopened, err := Open(path)
	if err != nil || !reflect.DeepEqual(reopened.Snapshot(), baseline.Snapshot()) {
		t.Fatalf("reset defaults were not persisted: %v", err)
	}
}
