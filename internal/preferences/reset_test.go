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
	service, err := Open(filepath.Join(t.TempDir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	changed := DefaultProxyPreferences()
	changed.LatencyTestConcurrency = 27
	if _, err := service.Update(t.Context(), Update{Proxies: &changed, LogLevels: []string{"error"}}); err != nil {
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
}
