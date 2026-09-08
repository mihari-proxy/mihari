//go:build linux || darwin

package app

import (
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/service"
)

func TestNativeInstallBoundary_SourceRecoveryRetriesAfterRestore(t *testing.T) {
	ctx, s, manager, _ := nativeBoundarySession(t)
	req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Channel: InstallChannelMain, Layout: InstallLayoutPrivate, Data: s.layout.Data.Root}
	nativeBoundaryApply(t, ctx, s, req, service.Definition{Status: service.StatusNotInstalled}, &nativeReleaseInputs{binary: []byte("verified original"), resources: map[string][]byte{}})
	manager.running = true // Fake manager only; no operating-system process exists.
	old, err := s.tx.Service.InspectDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req.Operation = InstallOperationUpdate
	if err := s.prepare(ctx, req, old, &nativeReleaseInputs{binary: []byte("verified replacement"), resources: map[string][]byte{}}, false); err != nil {
		t.Fatal(err)
	}
	s.tx.AfterDataCommitted = nil
	child := harnessValidationChild(s.tx.Artifacts)
	child.Result = ValidationFailed
	s.tx.Validation = child
	lease := &fakeInstallLease{global: true, held: true}
	if _, err := s.tx.ApplyLocked(ctx, lease, req); err == nil {
		t.Fatal("fixture validation failure not reached")
	}
	t.Cleanup(func() {
		if s.dataLease != nil {
			if err := s.dataLease.Release(ctx); err != nil {
				t.Error(err)
			}
		}
	})
	for attempt := 0; attempt < 3; attempt++ {
		manager.failStarts = 1
		if err := s.tx.RecoverLocked(ctx, lease); err == nil {
			t.Fatal("restart interruption not reached", attempt)
		}
		current, err := manager.files.Read(ctx, old.Files[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		if string(current.Bytes) != string(old.Files[0].Bytes) {
			t.Fatal("recovery failed before restoring the old definition", attempt)
		}
		if s.tx.journal.Phase == InstallPhaseComplete {
			t.Fatal("failed restart marked recovery complete")
		}
		if attempt == 0 {
			// A legitimate restored candidate is still identity bound on the next retry.
			held := current.Path + ".held-original"
			if err := os.Rename(current.Path, held); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(current.Path, current.Bytes, os.FileMode(current.Mode)); err != nil {
				t.Fatal(err)
			}
			if err := s.tx.RecoverLocked(ctx, lease); err == nil {
				t.Fatal("foreign equal-byte inode accepted during retry")
			}
			if err := os.Rename(held, current.Path); err != nil {
				t.Fatal(err)
			}
		}
	}
	manager.failStarts = 0
	if err := s.tx.RecoverLocked(ctx, lease); err != nil {
		t.Fatal("legitimate repeated recovery cannot finish", err)
	}
	if s.tx.journal.Phase != InstallPhaseComplete || s.tx.journal.RecoveryAuthority != InstallAuthoritySource || !manager.running {
		t.Fatal("source recovery did not finish in original running state")
	}
	if err := s.cleanupCompletedCandidates(ctx); err != nil {
		t.Fatal(err)
	}
}
