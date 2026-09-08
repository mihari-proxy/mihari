//go:build unix_security && (linux || darwin)

package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/securitytest"
)

func TestSecurityPrivateServiceActivation(t *testing.T) {
	system, _, _ := securitytest.Parent(t)
	rotateSecurityBase(t)
	ctx := context.Background()
	anchor := os.Getenv("MIHARI_SECURITY_ROOT")
	layout, err := platform.ResolveLayout(platform.LayoutInput{Data: filepath.Join(anchor, "private-service"), EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	base, err := platform.OpenTrustedRoot(ctx, system.BaseDir, platform.RootPolicy{Mode: 0711, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := base.Close(); err != nil {
			t.Error(err)
		}
	}()
	raw, err := os.ReadFile(filepath.Join("testdata", "install", "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := DecodeJournal(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := trustedValidationBinaryHash(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	journal.Mode = InstallLayoutPrivate
	journal.Phase = InstallPhaseActivationCommitted
	journal.RecoveryAuthority = InstallAuthorityTarget
	journal.DataRoot = layout.Data.Root
	journal.TargetPath = layout.Data.Root
	journal.InstallPath = layout.InstallRoot
	journal.EndpointPath = layout.ControlEndpoint
	journal.CredentialPath = layout.CredentialPath
	journal.CandidateHash = hash
	journal.Actions = nil
	calls := 0
	run := func(context.Context, string) error { calls++; return nil }
	if err := RunUnixSystemService(ctx, layout, run); err == nil || calls != 0 {
		t.Fatal("missing B journal did not fail before data IO")
	}
	for _, row := range []struct {
		name string
		edit func(*InstallJournal)
	}{
		{"pending", func(j *InstallJournal) { j.Phase = InstallPhasePrepared; j.RecoveryAuthority = InstallAuthoritySource }},
		{"wrong-P", func(j *InstallJournal) { j.DataRoot += "-wrong"; j.TargetPath = j.DataRoot }},
		{"wrong-I", func(j *InstallJournal) { j.InstallPath += "-wrong" }},
		{"wrong-binary", func(j *InstallJournal) { j.CandidateHash = strings.Repeat("e", 64) }},
	} {
		t.Run(row.name, func(t *testing.T) {
			value := journal
			row.edit(&value)
			encoded, err := EncodeJournal(value)
			if err != nil {
				t.Fatal(err)
			}
			writeSecurityActivationJournal(t, ctx, base, encoded)
			if err := RunUnixSystemService(ctx, layout, run); err == nil || calls != 0 {
				t.Fatal("invalid activation invoked business callback")
			}
			if _, err := os.Lstat(layout.Data.Root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("early refusal created P")
			}
		})
	}
	encoded, err := EncodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	writeSecurityActivationJournal(t, ctx, base, encoded)
	lease, err := platform.AcquireInstallLease(ctx, layout)
	if err != nil {
		t.Fatal(err)
	}
	// B→P is really held. Installed startup must never acquire either again.
	if err := RunUnixSystemService(ctx, layout, func(ctx context.Context, phase string) error {
		if phase != InstallPhaseActivationCommitted {
			t.Fatal("wrong phase")
		}
		daemonLease, err := platform.AcquireDaemonLease(ctx, layout)
		if err != nil {
			return err
		}
		defer func() {
			if err := daemonLease.Close(); err != nil {
				t.Error(err)
			}
		}()
		actual, present, err := InspectUnixServiceActivation(ctx, layout)
		if err != nil || !present || actual != phase {
			t.Fatal("post-data lease activation check failed")
		}
		calls++
		return nil
	}); err != nil || calls != 1 {
		t.Fatalf("installed private service reentered install lease: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	private, err := platform.OpenTrustedRoot(ctx, layout.BaseDir, platform.RootPolicy{Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := private.Close(); err != nil {
			t.Error(err)
		}
	}()
	// Unmarked P foreground consumes only its own complete journal, even when B
	// carries an unrelated/pending service transaction. It does not bootstrap.
	local := journal
	local.Phase = InstallPhaseComplete
	local.CandidateHash = strings.Repeat("e", 64)
	encoded, err = EncodeJournal(local)
	if err != nil {
		t.Fatal(err)
	}
	writeSecurityActivationJournal(t, ctx, private, encoded)
	priorCalls := calls
	if err := RunUnixStartup(ctx, layout, func(context.Context) (bool, error) { t.Fatal("activated P tried to bootstrap"); return false, nil }, run); err == nil || calls != priorCalls {
		t.Fatal("foreground startup accepted a different executable hash")
	}
	local.CandidateHash = hash
	encoded, err = EncodeJournal(local)
	if err != nil {
		t.Fatal(err)
	}
	writeSecurityActivationJournal(t, ctx, private, encoded)
	pending := journal
	pending.Phase = InstallPhasePrepared
	pending.RecoveryAuthority = InstallAuthoritySource
	encoded, err = EncodeJournal(pending)
	if err != nil {
		t.Fatal(err)
	}
	writeSecurityActivationJournal(t, ctx, base, encoded)
	if err := RunUnixStartup(ctx, layout, func(context.Context) (bool, error) { t.Fatal("unmarked P inspected service"); return false, nil }, run); err != nil {
		t.Fatal(err)
	}
	installer, err := NewUnixInstaller(layout, binary, "native")
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.SetChannel(ctx, "dev"); err == nil {
		t.Fatal("same-P incomplete B journal permitted channel maintenance")
	}
	if _, err := os.Lstat(layout.ChannelPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("denied channel wrote metadata")
	}
	pending.DataRoot += "-unrelated"
	pending.TargetPath = pending.DataRoot
	encoded, err = EncodeJournal(pending)
	if err != nil {
		t.Fatal(err)
	}
	writeSecurityActivationJournal(t, ctx, base, encoded)
	if err := installer.SetChannel(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if got, err := installer.QueryChannel(ctx); err != nil || got != "dev" {
		t.Fatal("unrelated B channel positive failed", err)
	}
}

func writeSecurityActivationJournal(t *testing.T, ctx context.Context, root *platform.TrustedRoot, raw []byte) {
	t.Helper()
	var expected *platform.FileIdentity
	file, identity, err := root.OpenFile(ctx, installJournalFileName, 0600)
	if err == nil {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		expected = &identity
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := root.WriteFile(ctx, installJournalFileName, raw, 0600, expected); err != nil {
		t.Fatal(err)
	}
}
