package runtime

import (
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboarding_AllowLANValidatesBeforePersistence(t *testing.T) {
	service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	saves := 0
	m := newTestManager(Options{Settings: settings, SettingsPath: "injected-settings", Onboarding: service,
		SaveSettings: func(_ string, _ config.Settings) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true}, nil
		},
	})
	wildcard, enable, disable := "0.0.0.0:9190", true, false
	if _, err := m.UpdateOnboarding(t.Context(), Operation{ID: "invalid"}, onboarding.Update{MixedAddr: &wildcard}); err == nil {
		t.Fatal("accepted wildcard with disabled LAN")
	}
	if saves != 0 || m.settingsSnapshot().MixedAddr != settings.MixedAddr {
		t.Fatal("invalid edit persisted")
	}
	result, err := m.UpdateOnboarding(t.Context(), Operation{ID: "enable"}, onboarding.Update{MixedAddr: &wildcard, AllowLAN: &enable})
	if err != nil || !result.Status.AllowLAN || !result.Status.RestartRequired || saves != 1 {
		t.Fatalf("result=%+v saves=%d err=%v", result, saves, err)
	}
	if _, err := m.UpdateOnboarding(t.Context(), Operation{ID: "disable-invalid"}, onboarding.Update{AllowLAN: &disable}); err == nil {
		t.Fatal("disabled LAN with wildcard still saved")
	}
	if saves != 1 || !m.settingsSnapshot().AllowLAN {
		t.Fatal("invalid disable persisted")
	}
	loopback := settings.MixedAddr
	result, err = m.UpdateOnboarding(t.Context(), Operation{ID: "disable"}, onboarding.Update{MixedAddr: &loopback, AllowLAN: &disable})
	if err != nil || result.Status.AllowLAN || saves != 2 {
		t.Fatalf("result=%+v saves=%d err=%v", result, saves, err)
	}
}
