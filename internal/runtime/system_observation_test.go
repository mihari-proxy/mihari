package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/observation"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/preferences"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/tundetect"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSystemObservation_ReusesSuccessfulTUNAndSkipsDefaultOwner(t *testing.T) {
	calls, owners := 0, 0
	ctrl := &fakeController{configsFunc: func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"tun": map[string]any{"enable": false}}, nil
	}}
	detect := &tundetect.FakeBackend{}
	m := newTunManagerWithDetect(t, ctrl, defaultTunSettings(nil), detect)
	prefs, err := preferences.Open(filepath.Join(t.TempDir(), "tui.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.preferences = prefs
	m.lookupOccupant = func(string) (int, bool) { owners++; return 42, true }
	for range 2 {
		if _, err := m.TunStatus(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || detect.DetectCalls != 1 || owners != 0 {
		t.Fatalf("configs=%d detection=%d owner=%d", calls, detect.DetectCalls, owners)
	}
	if _, err := m.TunStatus(observation.WithFresh(t.Context())); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || detect.DetectCalls != 2 {
		t.Fatal("explicit refresh reused completed observation")
	}
	policy := preferences.PerformancePreferences{ControllerOwnerDetection: true, ObservationReuseSeconds: 3}
	if _, err := prefs.Update(t.Context(), preferences.Update{Performance: &policy}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.TunStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatal("changed policy did not apply")
	}
}

func setObservationPolicy(t *testing.T, m *Manager, policy preferences.PerformancePreferences) {
	t.Helper()
	prefs, err := preferences.Open(filepath.Join(t.TempDir(), "tui.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = prefs.Update(t.Context(), preferences.Update{Performance: &policy}); err != nil {
		t.Fatal(err)
	}
	m.preferences = prefs
}

func TestSystemObservation_ReusesEachProbeAndKeepsOperationFresh(t *testing.T) {
	calls, adapters := 0, 0
	ctrl := &fakeController{configsFunc: func(context.Context) (map[string]any, error) {
		calls++
		return map[string]any{"interface-name": "eth0", "tun": map[string]any{"enable": false, "device": "own"}}, nil
	}}
	m := newTunManager(t, ctrl, defaultTunSettings(nil))
	setObservationPolicy(t, m, preferences.DefaultPerformancePreferences())
	m.store.Store(state.Snapshot{Core: state.CoreState{Status: "running", PID: 42}})
	m.runtimeConfig = filepath.Join(t.TempDir(), "runtime.yaml")
	if err := os.WriteFile(m.runtimeConfig, []byte("interface-name: eth0\ntun: {device: own}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m.listInterfaces = func(context.Context) ([]platform.NetworkInterface, error) {
		adapters++
		return []platform.NetworkInterface{{Name: "eth0", Addresses: []string{"127.0.0.1"}}}, nil
	}
	if _, err := m.TunStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := m.EgressStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first.Interfaces[0].Addresses[0] = "caller mutation"
	next, err := m.EgressStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || adapters != 1 || next.Interfaces[0].Addresses[0] != "127.0.0.1" {
		t.Fatalf("configs=%d adapters=%d status=%+v", calls, adapters, next)
	}
	if err := m.lockMutation(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.egressStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.unlock()
	if calls != 3 || adapters != 2 {
		t.Fatal("operation consumed display cache")
	}
	if _, err := m.EgressStatus(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || adapters != 3 {
		t.Fatal("mutation did not invalidate display")
	}
}
func TestSystemObservation_DetectionFailureNotCached(t *testing.T) {
	backend := &tundetect.FakeBackend{Err: errors.New("scan failed")}
	m := newTunManagerWithDetect(t, &fakeController{}, defaultTunSettings(nil), backend)
	setObservationPolicy(t, m, preferences.DefaultPerformancePreferences())
	for range 2 {
		if _, err := m.TunStatus(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if backend.DetectCalls != 2 {
		t.Fatal("failed detection cached as no conflict")
	}
}
func TestSystemObservation_SysProxyAndServiceReuseAndRefresh(t *testing.T) {
	backend := &hookSysProxyBackend{}
	m := newTunManager(t, &fakeController{}, defaultTunSettings(nil))
	setObservationPolicy(t, m, preferences.DefaultPerformancePreferences())
	m.sysProxy = backend
	for range 2 {
		if _, err := m.SystemProxyStatus(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if backend.getCalls != 1 {
		t.Fatalf("proxy reads=%d", backend.getCalls)
	}
	if _, err := m.SystemProxyStatus(observation.WithFresh(t.Context())); err != nil {
		t.Fatal(err)
	}
	if backend.getCalls != 2 {
		t.Fatal("proxy refresh was cached")
	}
	calls := 0
	m.serviceStatus = func() (string, error) { calls++; return "running", nil }
	for range 2 {
		if got, err := m.ServiceStatus(t.Context()); err != nil || got.Status != "running" {
			t.Fatal(got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("service reads=%d", calls)
	}
}

func TestSystemObservation_UnconfirmedOwnerIsNotCached(t *testing.T) {
	detect := &tundetect.FakeBackend{}
	m := newTunManagerWithDetect(t, &fakeController{}, defaultTunSettings(nil), detect)
	setObservationPolicy(t, m, preferences.PerformancePreferences{ControllerOwnerDetection: true, ObservationReuseSeconds: 3})
	m.lookupOccupant = func(string) (int, bool) { return 0, false }
	for range 2 {
		if _, err := m.TunStatus(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if detect.DetectCalls != 2 {
		t.Fatal("unconfirmed owner was cached as successful")
	}
}

func TestSystemObservation_ReturnedConflictDoesNotAliasCache(t *testing.T) {
	backend := &tundetect.FakeBackend{Detection: tundetect.Detection{TunInterfaces: []string{"foreign"}}}
	m := newTunManagerWithDetect(t, &fakeController{}, defaultTunSettings(nil), backend)
	setObservationPolicy(t, m, preferences.DefaultPerformancePreferences())
	first, err := m.TunStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first.Conflict.OtherTunInterfaces[0] = "caller mutation"
	next, err := m.TunStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if next.Conflict.OtherTunInterfaces[0] != "foreign" {
		t.Fatal("caller mutated cached conflict")
	}
}
func TestSystemObservation_TunErrorConcurrentRead(t *testing.T) {
	m := newTunManager(t, &fakeController{}, defaultTunSettings(nil))
	setObservationPolicy(t, m, preferences.DefaultPerformancePreferences())
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		for range 2000 {
			m.setTunLastError("changing diagnostic")
		}
	}()
	close(start)
	for range 30 {
		if _, err := m.TunStatus(t.Context()); err != nil {
			t.Error(err)
			break
		}
	}
	group.Wait()
}
