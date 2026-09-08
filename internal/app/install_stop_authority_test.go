package app

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/service"
)

type unresolvedRecoveryService struct {
	service.DefinitionAdapter
	checked   bool
	bound     bool
	authority service.Definition
	boot      string
}

func (s *unresolvedRecoveryService) BindStopAuthority(def service.Definition, boot string) {
	s.bound = true
	s.authority = def
	s.boot = boot
}

func (s *unresolvedRecoveryService) WaitRecordedTreeExit(context.Context) error {
	s.checked = true
	return errors.New("recorded service group remains unresolved")
}

func TestInstallRollforward_RequiresRecordedGroupExitBeforeEffects(t *testing.T) {
	h := newInstallHarness(t, InstallDataRetain)
	svc := &unresolvedRecoveryService{}
	h.tx.Service = svc
	h.tx.journal = InstallJournal{RecoveryAuthority: InstallAuthorityTarget, Actions: []JournalAction{{Seq: 1, Kind: JournalActionStop, Status: JournalActionDone}}}
	if err := h.tx.rollforward(context.Background()); err == nil || !svc.checked {
		t.Fatal("rollforward skipped unresolved service group authority")
	}
}

func TestRecoveryStopAuthority_DoesNotReuseOldGroupForLaterGeneration(t *testing.T) {
	old := service.Definition{Status: service.StatusRunning, Running: true, Process: service.ProcessIdentity{PID: 77, BootID: "old-boot", StartUnix: 100, Group: "old-group"}}
	target := old
	for _, tc := range []struct {
		name, authority, phase, action string
		targetStopped                  bool
	}{
		{"target complete", InstallAuthorityTarget, InstallPhaseComplete, "", false},
		{"source restored", InstallAuthoritySource, InstallPhaseComplete, "", false},
		{"target cold", InstallAuthorityTarget, InstallPhaseComplete, "", true},
		{"bootstrap intent", InstallAuthorityTarget, InstallPhaseActivationCommitted, JournalActionStart, false},
		{"restore intent", InstallAuthoritySource, InstallPhasePrepared, JournalActionRestoreStart, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final := target
			if tc.targetStopped {
				final.Running = false
				final.Status = service.StatusStopped
			}
			j := InstallJournal{RecoveryAuthority: tc.authority, Phase: tc.phase}
			if tc.action != "" {
				j.Actions = []JournalAction{{Kind: tc.action, Status: JournalActionIntent}}
			}
			def, boot := recoveryStopAuthority(j, old, final, "old-boot", "current-boot")
			if def.Process != (service.ProcessIdentity{}) || boot != "current-boot" || def.Status == service.StatusNotInstalled {
				t.Fatal("old empty group supplied authority for an unrecorded later generation")
			}
		})
	}
}

func TestInstallRollforward_RebindsAfterSameSessionBootstrapIntent(t *testing.T) {
	h := newInstallHarness(t, InstallDataRetain)
	svc := &unresolvedRecoveryService{}
	h.tx.Service = svc
	old := service.Definition{Status: service.StatusRunning, Running: true, Process: service.ProcessIdentity{PID: 77, BootID: testBootID, StartUnix: 100, Group: "old-group"}}
	h.tx.preparedAuthority = &installPreparedAuthority{OldDefinition: old, TargetDefinition: old}
	h.tx.journal = InstallJournal{RecoveryAuthority: InstallAuthorityTarget, BootID: testBootID, Phase: InstallPhaseActivationCommitted, Actions: []JournalAction{{Kind: JournalActionStart, Status: JournalActionIntent}}}
	if err := h.tx.rollforward(context.Background()); err == nil {
		t.Fatal("unresolved bootstrap was allowed to recover")
	}
	if !svc.bound || svc.authority.Process != (service.ProcessIdentity{}) || svc.boot != testBootID {
		t.Fatal("same-session recovery kept stale pre-bootstrap group authority")
	}
}
