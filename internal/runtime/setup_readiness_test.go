package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/onboarding"
)

func TestSetupReadiness_UnusableCoreIsNotComplete(t *testing.T) {
	service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(Options{Onboarding: service, BinaryExists: func() bool { return true }, Installer: &fakeInstaller{detectVersion: func(context.Context, string) (string, error) {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid binary"}
	}}})
	required, err := m.SetupRequired(context.Background())
	if err != nil || !required {
		t.Fatal("unusable binary was treated as completed setup")
	}
}

func TestSetupReadiness_RequiredResourcesOverrideHistoricalMarker(t *testing.T) {
	for _, complete := range []bool{false, true} {
		for _, exists := range []bool{false, true} {
			service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: !complete})
			if err != nil {
				t.Fatal(err)
			}
			m := newTestManager(Options{Onboarding: service, BinaryExists: func() bool { return exists }})
			required, err := m.SetupRequired(context.Background())
			if err != nil || required == exists {
				t.Fatalf("complete=%v exists=%v required=%v err=%v", complete, exists, required, err)
			}
			m.onboardingRestartRequired = true
			required, err = m.SetupRequired(context.Background())
			if err != nil || !required {
				t.Fatal("saved but ineffective ports were treated as ready")
			}
		}
	}
}
