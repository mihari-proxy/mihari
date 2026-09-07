package daemon

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
)

func TestActivation_BusinessMutationGate(t *testing.T) {
	cases := []struct {
		phase   string
		allowed bool
	}{
		{"prepared", false},
		{"definition_committed", false},
		{"activation_committed", true},
		{"complete", true},
	}
	for _, test := range cases {
		if got := BusinessMutationAllowed(test.phase, false); got != test.allowed {
			t.Fatalf("phase %s allowed=%v want %v", test.phase, got, test.allowed)
		}
		if BusinessMutationAllowed(test.phase, true) {
			t.Fatalf("validation mode allowed mutation at phase %s", test.phase)
		}
	}
	if !BusinessMutationAllowed("", false) {
		t.Fatal("empty phase (no journal) must allow ordinary daemon business")
	}
	if BusinessMutationAllowed("", true) {
		t.Fatal("validation mode allowed mutation without a journal")
	}
}

func TestUnixBootstrap_ForegroundDecisions(t *testing.T) {
	journal := app.InstallJournal{Phase: app.InstallPhasePrepared, RecoveryAuthority: app.InstallAuthoritySource}
	got, err := DecideForegroundBootstrap(journal, true, false, true, true)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if !got.RecoverRequired || got.AllowDaemon {
		t.Fatalf("pending started daemon: %+v", got)
	}

	journal.Phase = app.InstallPhaseActivationCommitted
	journal.RecoveryAuthority = app.InstallAuthorityTarget
	got, err = DecideForegroundBootstrap(journal, true, false, true, true)
	if err != nil {
		t.Fatalf("activated: %v", err)
	}
	if !got.AllowDaemon || !got.TargetAuthority || got.RunValidation {
		t.Fatalf("activated: %+v", got)
	}

	got, err = DecideForegroundBootstrap(app.InstallJournal{}, false, false, true, true)
	if err != nil {
		t.Fatalf("greenfield: %v", err)
	}
	if !got.CreateData || !got.RunValidation || got.MigrateRequired {
		t.Fatalf("greenfield: %+v", got)
	}

	got, err = DecideForegroundBootstrap(app.InstallJournal{}, false, true, true, true)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if !got.MigrateRequired || got.CreateData {
		t.Fatalf("source must migrate without creating D first: %+v", got)
	}

	got, err = DecideForegroundBootstrap(app.InstallJournal{}, false, false, false, false)
	if err != nil {
		t.Fatalf("non-root: %v", err)
	}
	if !got.OldInit || !got.AllowDaemon || got.RunValidation {
		t.Fatalf("non-root old init: %+v", got)
	}
}

func TestActivation_OrdinaryDaemonPending(t *testing.T) {
	err := CheckOrdinaryDaemonStart(app.InstallJournal{Phase: app.InstallPhasePrepared}, true)
	if err == nil {
		t.Fatal("pending journal started the ordinary daemon")
	}
	if err := CheckOrdinaryDaemonStart(app.InstallJournal{Phase: app.InstallPhaseActivationCommitted}, true); err != nil {
		t.Fatalf("activation_committed: %v", err)
	}
	if err := CheckOrdinaryDaemonStart(app.InstallJournal{}, false); err != nil {
		t.Fatalf("missing journal: %v", err)
	}
}

func TestUnixBootstrap_RootPrivateRequiresActivation(t *testing.T) {
	got, err := DecideForegroundBootstrap(app.InstallJournal{Phase: app.InstallPhasePrepared}, true, false, true, false)
	if err != nil || !got.RecoverRequired || got.OldInit {
		t.Fatalf("root private bypassed journal: %+v %v", got, err)
	}
}
