package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type warningCoreCandidate struct{ fakeCandidate }

func (*warningCoreCandidate) Warnings() []error {
	return []error{errors.New("official asset has no SHA-256 digest")}
}

func TestInstallReturnsCandidateWarningsAndReplaysWithoutReportingAgain(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "core-warnings"})
	if err != nil {
		t.Fatal(err)
	}
	installer := &fakeInstaller{candidate: &warningCoreCandidate{fakeCandidate{version: "v1.20.0"}}}
	manager := newTestManager(Options{Installer: installer, Supervisor: &fakeSupervisor{}, DiagnosticReporter: diagnostics.NewOwner(history, nil).Report})
	var firstID string
	for attempt := 0; attempt < 2; attempt++ {
		ctx, receipt := diagnostics.WithResult(t.Context())
		result, err := manager.Install(ctx, Operation{ID: "core-missing-digest", Source: "test"})
		if err != nil || !result.Updated {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		warnings := receipt.Warnings().Warnings
		if len(warnings) != 1 || warnings[0].Diagnostic == nil || !strings.Contains(warnings[0].Message, "SHA-256") {
			t.Fatalf("missing candidate warning: %+v", warnings)
		}
		id := warnings[0].Diagnostic.ID
		if attempt == 0 {
			firstID = id
		} else if id != firstID {
			t.Fatal("replay recorded another warning occurrence")
		}
	}
	if installer.calls.Load() != 1 || len(history.List("", 0, 100).Records) != 1 {
		t.Fatal("replay repeated preparation or reporting")
	}
}
