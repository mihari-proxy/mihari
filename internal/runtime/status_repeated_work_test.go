package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/tundetect"
)

func TestTunStatus_UsesOneLiveConfigObservation(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		calls := 0
		controller := &fakeController{configsFunc: func(context.Context) (map[string]any, error) {
			calls++
			if unavailable {
				return nil, errors.New("offline")
			}
			return map[string]any{"tun": map[string]any{"enable": true, "device": "own-tun"}}, nil
		}}
		m := newTunManagerWithDetect(t, controller, defaultTunSettings(nil), &tundetect.FakeBackend{Detection: tundetect.Detection{TunInterfaces: []string{"own-tun"}}})
		got, err := m.TunStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("unavailable=%v Configs calls=%d, want 1", unavailable, calls)
		}
		if unavailable {
			if got.LiveEnable != nil || got.Conflict == nil {
				t.Fatalf("unavailable status=%+v", got)
			}
		} else if got.LiveEnable == nil || !*got.LiveEnable || got.Conflict != nil {
			t.Fatalf("status=%+v", got)
		}
	}
}

func TestEgressStatus_ReadsPreviousConfigOncePerQuery(t *testing.T) {
	for _, failed := range []bool{false, true} {
		m, _ := onlineEgressManager(t)
		reads := 0
		read := func(context.Context) ([]byte, error) {
			reads++
			if failed {
				return nil, errors.New("unreadable config")
			}
			return []byte("interface-name: previous\ntun: {device: Ethernet}\n"), nil
		}
		for round := 1; round <= 2; round++ {
			got, err := m.egressStatusWithConfig(t.Context(), read)
			if err != nil {
				t.Fatal(err)
			}
			if reads != round {
				t.Fatalf("failed=%v round=%d reads=%d", failed, round, reads)
			}
			if !failed && (got.State != "applied" || got.Interfaces[0].Selectable) {
				t.Fatalf("status=%+v", got)
			}
			if failed && got.State != "unknown" {
				t.Fatalf("status=%+v", got)
			}
		}
	}
}

func TestEgressStatus_DoesNotReadUnusedPreviousConfig(t *testing.T) {
	m, _ := onlineEgressManager(t)
	m.settings.EgressInterface = "Ethernet"
	m.controller = &fakeController{configs: map[string]any{"interface-name": "Ethernet", "tun": map[string]any{"device": "own-tun"}}}
	got, err := m.egressStatusWithConfig(t.Context(), func(context.Context) ([]byte, error) {
		t.Fatal("read unused previous config")
		return nil, nil
	})
	if err != nil || got.State != "applied" || !got.Interfaces[0].Selectable {
		t.Fatalf("status=%+v err=%v", got, err)
	}
}
