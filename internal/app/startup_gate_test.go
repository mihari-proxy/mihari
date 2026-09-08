package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"testing"
)

func TestUnixStartup_ActivatedRunsWithoutInstallLease(t *testing.T) {
	ran := false
	err := runUnixStartup(context.Background(), true, func(context.Context) (string, bool, error) { return InstallPhaseActivationCommitted, true, nil }, func(context.Context) error { return errors.New("install lease is already held by installer") }, func(_ context.Context, phase string) error {
		ran = true
		if phase != InstallPhaseActivationCommitted {
			t.Fatalf("phase=%s", phase)
		}
		return nil
	})
	if err != nil || !ran {
		t.Fatalf("activated target could not start while installer holds its lease: run=%v err=%v", ran, err)
	}
}
func TestUnixStartup_PendingAndUnknownNeverBootstrap(t *testing.T) {
	sentinel := errors.New("pending journal")
	calls := 0
	err := runUnixStartup(context.Background(), true, func(context.Context) (string, bool, error) { return "", true, sentinel }, func(context.Context) error { calls++; return nil }, func(context.Context, string) error { calls++; return nil })
	if !errors.Is(err, sentinel) || calls != 0 {
		t.Fatalf("pending startup bypass: calls=%d err=%v", calls, err)
	}
}

func TestUnixStartup_ActivationMatchesInstallRoot(t *testing.T) {
	journal := mustDecodeJournal(t)
	journal.Phase = InstallPhaseComplete
	journal.RecoveryAuthority = InstallAuthorityTarget
	layout := platform.ResolvedLayout{Mode: platform.LayoutMode(journal.Mode), Data: platform.Paths{Root: journal.DataRoot}, ControlEndpoint: journal.EndpointPath, CredentialPath: journal.CredentialPath, InstallRoot: journal.InstallPath}
	if _, err := startupJournalPhase(journal, layout); err != nil {
		t.Fatal(err)
	}
	layout.InstallRoot += "-different"
	if _, err := startupJournalPhase(journal, layout); err == nil {
		t.Fatal("activation admitted a different install root")
	}
}

func TestUnixStartup_ActivationRejectsDifferentTargetPath(t *testing.T) {
	journal := mustDecodeJournal(t)
	layout := platform.ResolvedLayout{Mode: platform.LayoutMode(journal.Mode), Data: platform.Paths{Root: journal.DataRoot}, ControlEndpoint: journal.EndpointPath, CredentialPath: journal.CredentialPath, InstallRoot: journal.InstallPath}
	journal.RecoveryAuthority = InstallAuthorityTarget
	journal.TargetPath += "-other"
	for _, phase := range []string{InstallPhaseActivationCommitted, InstallPhaseComplete} {
		journal.Phase = phase
		if _, err := startupJournalPhase(journal, layout); err == nil {
			t.Fatalf("activation accepted a different target path at %s", phase)
		}
	}
}

func TestUnixStartup_PrivateServiceScopeAndForegroundIsolation(t *testing.T) {
	layout := platform.ResolvedLayout{Mode: platform.PrivateMode, BaseDir: "/portable", Data: platform.Paths{Root: "/portable"}}
	defaults := platform.LayoutDefaults{BaseDir: "/system"}
	root, mode, err := startupJournalScope(layout, true, 0, defaults)
	if err != nil || root != "/system" || mode != 0711 {
		t.Fatalf("private service gate scope=%s mode=%o err=%v", root, mode, err)
	}
	root, mode, err = startupJournalScope(layout, false, 0, platform.LayoutDefaults{})
	if err != nil || root != "/portable" || mode != 0700 {
		t.Fatalf("private foreground consulted machine scope: %s %o %v", root, mode, err)
	}
	if _, _, err := startupJournalScope(layout, true, 1000, defaults); err == nil {
		t.Fatal("ordinary UID selected system service gate")
	}
}

func TestUnixStartup_MarkedServiceRequiresActivation(t *testing.T) {
	for _, tc := range []struct {
		name, phase string
		present     bool
		err         error
		wantRun     bool
	}{
		{name: "missing"}, {name: "pending", present: true, err: errors.New("pending")}, {name: "wrong-P", present: true, err: errors.New("mismatched private root")}, {name: "activation", phase: InstallPhaseActivationCommitted, present: true, wantRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			err := runUnixServiceStartup(context.Background(), func(context.Context) (string, bool, error) { return tc.phase, tc.present, tc.err }, func(context.Context, string) error { ran = true; return nil })
			if ran != tc.wantRun || (err == nil) != tc.wantRun {
				t.Fatalf("service startup ran=%v err=%v", ran, err)
			}
		})
	}
}

func TestUnixStartup_ActivationRejectsDifferentInstance(t *testing.T) {
	journal := mustDecodeJournal(t)
	journal.Mode = string(platform.PrivateMode)
	journal.Phase = InstallPhaseActivationCommitted
	journal.RecoveryAuthority = InstallAuthorityTarget
	original := platform.ResolvedLayout{Mode: platform.PrivateMode, Data: platform.Paths{Root: journal.DataRoot}, ControlEndpoint: journal.EndpointPath, CredentialPath: journal.CredentialPath, InstallRoot: journal.InstallPath}
	for _, tc := range []struct {
		name   string
		change func(*platform.ResolvedLayout)
	}{
		{"P", func(l *platform.ResolvedLayout) { l.Data.Root += "-other" }},
		{"endpoint", func(l *platform.ResolvedLayout) { l.ControlEndpoint += "-other" }},
		{"credential", func(l *platform.ResolvedLayout) { l.CredentialPath += "-other" }},
		{"mode", func(l *platform.ResolvedLayout) { l.Mode = platform.SystemMode }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout := original
			tc.change(&layout)
			if _, err := startupJournalPhase(journal, layout); err == nil {
				t.Fatal("activation accepted a different instance")
			}
		})
	}
}

func TestUnixStartup_SourceAuthorityCannotActivateDaemon(t *testing.T) {
	j := mustDecodeJournal(t)
	layout := platform.ResolvedLayout{Mode: platform.LayoutMode(j.Mode), Data: platform.Paths{Root: j.DataRoot}, InstallRoot: j.InstallPath, ControlEndpoint: j.EndpointPath, CredentialPath: j.CredentialPath}
	for _, phase := range []string{InstallPhaseActivationCommitted, InstallPhaseComplete} {
		j.Phase, j.RecoveryAuthority = phase, InstallAuthoritySource
		if _, err := startupJournalPhase(j, layout); err == nil {
			t.Fatalf("production startup accepted source authority at %s", phase)
		}
	}
}
