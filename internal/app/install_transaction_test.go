package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
)

func TestInstallTransaction_ApplyAcquiresOnceThenRecoversThenApplies(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	ctx := context.Background()
	got, err := h.tx.Apply(ctx, h.req)
	if err != nil {
		t.Fatal(err)
	}
	if h.lease.acquires != 1 {
		t.Fatalf("install lock acquired %d times", h.lease.acquires)
	}
	if h.lease.validates < 2 {
		t.Fatalf("borrowed lease was not validated: %d", h.lease.validates)
	}
	if h.lease.closes != 1 {
		t.Fatalf("outer apply did not release lease: %d", h.lease.closes)
	}
	if got.Schema != InstallResultSchema || got.TransactionID != testTxnID || !got.Changed {
		t.Fatalf("result: %+v", got)
	}
	if got.ServiceStatus != InstallServiceRunning || !got.SourceRetained {
		t.Fatalf("service/source: %+v", got)
	}
	journal := h.loadedJournal(t, h.disk)
	if journal.Phase != InstallPhaseComplete {
		t.Fatalf("phase=%s", journal.Phase)
	}
	if journal.RecoveryAuthority != InstallAuthorityTarget {
		t.Fatalf("authority=%s", journal.RecoveryAuthority)
	}
	if h.service.publicEntrypoint != 0 {
		t.Fatal("adapter called a public service entrypoint")
	}
	kinds := actionKinds(journal)
	if !containsKind(kinds, JournalActionReload) {
		t.Fatalf("reload was not journaled: %v", kinds)
	}
	if !containsKind(kinds, JournalActionMask) || !containsKind(kinds, JournalActionActivation) {
		t.Fatalf("missing required kinds: %v", kinds)
	}
}

func TestInstallTransaction_PhasesPersistInOrder(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	journal := h.loadedJournal(t, h.disk)
	if journal.Phase != InstallPhaseComplete {
		t.Fatalf("final phase=%s", journal.Phase)
	}
	want := []string{
		JournalActionDisable, JournalActionMask, JournalActionReload, JournalActionStop,
		JournalActionDataPublish, JournalActionManagedBinary, JournalActionPathBinary, JournalActionChannel,
		JournalActionDefinition, JournalActionReload,
		JournalActionValidationStart, JournalActionValidationStop, JournalActionActivation,
		JournalActionUnmask, JournalActionEnable, JournalActionReload, JournalActionStart,
	}
	got := actionKinds(journal)
	if len(got) != len(want) {
		t.Fatalf("actions=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("action[%d]=%s want %s (%v)", i, got[i], want[i], got)
		}
	}
	for _, action := range journal.Actions {
		if action.Status != JournalActionDone {
			t.Fatalf("incomplete action: %+v", action)
		}
	}
}

func TestInstallTransaction_OmitsNoOpPathBinary(t *testing.T) {
	same := []byte("same-path")
	cases := []struct {
		name string
		old  []byte
		new  []byte
	}{
		{name: "omitted", old: nil, new: nil},
		{name: "identical", old: same, new: append([]byte(nil), same...)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h := newInstallHarness(t, InstallDataCreate)
			h.art.PathOld = test.old
			h.art.PathNew = test.new
			h.tx.Artifacts = h.art
			got, err := h.tx.Apply(context.Background(), h.req)
			if err != nil {
				t.Fatal(err)
			}
			if got.TransactionID != testTxnID {
				t.Fatalf("result: %+v", got)
			}
			journal := h.loadedJournal(t, h.disk)
			if journal.Phase != InstallPhaseComplete {
				t.Fatalf("phase=%s", journal.Phase)
			}
			if containsKind(actionKinds(journal), JournalActionPathBinary) || containsKind(actionKinds(journal), JournalActionRestorePathBinary) {
				t.Fatalf("no-op path_binary journaled: %v", actionKinds(journal))
			}
		})
	}
}

func TestInstallTransaction_RetainNeverMovesData(t *testing.T) {
	h := newInstallHarness(t, InstallDataRetain)
	before := append([]byte(nil), h.disk.Object(roleData)...)
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(h.disk.Object(roleData), before) {
		t.Fatal("retain moved data")
	}
	if len(h.disk.Object(roleIsolated)) != 0 {
		t.Fatal("retain isolated data")
	}
	journal := h.loadedJournal(t, h.disk)
	if containsKind(actionKinds(journal), JournalActionDataPublish) || containsKind(actionKinds(journal), JournalActionRestoreData) {
		t.Fatalf("retain journaled data rollback: %v", actionKinds(journal))
	}
}

func TestInstallTransaction_CreateIsolatesNewDataOnRollback(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.crash = &installCrashSpec{index: 5, point: "after-done"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("expected crash after data publish")
	}
	lease := &fakeInstallLease{global: true, held: true}
	disk := h.disk.clone()
	tx := h.reconstruct(disk, lease)
	if err := tx.RecoverLocked(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if !disk.SourcePresent() {
		t.Fatal("source was deleted")
	}
	if len(disk.Object(roleData)) != 0 {
		t.Fatal("create rollback left new data at target")
	}
	if len(disk.Object(roleIsolated)) == 0 {
		t.Fatal("create rollback did not isolate new data")
	}
}

func TestInstallTransaction_SourceNeverDeleted(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	if !h.disk.SourcePresent() {
		t.Fatal("source missing after apply")
	}
}

func TestInstallTransaction_UnknownIdentityRefused(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.crash = &installCrashSpec{index: 6, point: "after-effect"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("expected crash")
	}
	h.disk.put(roleManaged, []byte("neither-old-nor-new"))
	lease := &fakeInstallLease{global: true, held: true}
	err := h.reconstruct(h.disk, lease).RecoverLocked(context.Background(), lease)
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("want invalid_state, got %v", err)
	}
}

func TestInstallTransaction_RecoverOperationIgnoresCallerPaths(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationRecover}
	h.lease = &fakeInstallLease{global: true}
	h.tx = h.makeTxn(h.disk, h.lease)
	got, err := h.tx.Apply(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.TransactionID != testTxnID {
		t.Fatalf("recover result: %+v", got)
	}
}

func TestInstallTransaction_BinaryOnlyUpdateIsNotThisTask(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.disk.svc.installed = false
	h.req.Operation = InstallOperationUpdate
	lease, err := h.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.tx.ApplyLocked(context.Background(), lease, h.req)
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("binary-only must refuse service apply, got %v", err)
	}
	if containsKind(actionKinds(h.loadedJournalAllowMissing(t)), JournalActionManagedBinary) {
		t.Fatal("binary-only wrote a service journal")
	}
}

func (h *installHarness) loadedJournalAllowMissing(t *testing.T) InstallJournal {
	t.Helper()
	got, err := (&InstallJournalStore{files: h.disk}).Load(context.Background())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return InstallJournal{}
		}
		var api protocol.APIError
		if errors.As(err, &api) {
			return InstallJournal{}
		}
		t.Fatal(err)
	}
	return got
}

func actionKinds(journal InstallJournal) []string {
	out := make([]string, 0, len(journal.Actions))
	for _, action := range journal.Actions {
		out = append(out, action.Kind)
	}
	return out
}

func containsKind(kinds []string, want string) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

var _ service.DefinitionAdapter = (*fakeService)(nil)

func TestInstallTransaction_IntentMustBeDurableBeforeEffect(t *testing.T) {
	ctx := context.Background()
	h := newInstallHarness(t, InstallDataRetain)
	files := newRecordingJournalFiles()
	h.tx.Store = &InstallJournalStore{files: files}
	marker, err := h.tx.Store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	h.tx.journal, err = h.tx.buildJournal(h.req, testTxnID, marker, h.tx.preparedArtifacts(h.req))
	if err != nil {
		t.Fatal(err)
	}
	files.failAt = "parent-sync"
	applied := false
	err = h.tx.step(ctx, JournalAction{Kind: JournalActionManagedBinary, TargetRole: JournalRoleManagedBinary, OldState: "absent", NewState: "candidate"}, func(context.Context) error { applied = true; return nil })
	if err == nil || applied {
		t.Fatalf("undurable intent allowed effect: applied=%v err=%v", applied, err)
	}
	if len(h.tx.journal.Actions) != 1 || h.tx.journal.Actions[0].Status != JournalActionIntent {
		t.Fatal("published intent was lost for recovery")
	}
}
