package setup

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

func TestSetupMutations_PreserveCommittedWarnings(t *testing.T) {
	warning := protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "committed", Diagnostic: &protocol.Diagnostic{ID: "fixture:setup", Detail: "token=fixture-setup", State: protocol.DiagnosticAvailable}}}}
	for _, kind := range []string{"core", "subscription", "geoip"} {
		client := &fakeClient{status: defaultStatus(false), installResult: protocol.CoreInstallResult{WarningOutcome: warning}, subscriptionResult: protocol.SubscriptionResult{WarningOutcome: warning}, geoIPUpdateResult: protocol.GeoIPUpdateResult{WarningOutcome: warning}}
		m := loadedModel(client)
		var result any
		switch kind {
		case "core":
			result = m.installCore()()
		case "subscription":
			result = m.addSubscription("fixture", "https://example.invalid")()
		case "geoip":
			result = m.updateGeoIP()()
		}
		outcome, ok := result.(interface {
			Warnings() protocol.WarningOutcome
		})
		if !ok || len(outcome.Warnings().Warnings) != 1 || outcome.Warnings().Warnings[0].Diagnostic.Detail != "token=fixture-setup" {
			t.Fatalf("%s successful warning lost", kind)
		}
		if result.(actionResultMsg).Err() != nil {
			t.Fatal("warning changed success")
		}
		m.Stop()
	}
}
