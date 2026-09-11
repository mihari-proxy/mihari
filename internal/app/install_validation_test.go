package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestInstallValidation_HandshakeRequiresRootPipeAndMatchingIntent(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	journal := mustPreparedJournal(t, h)
	nonce := bytes.Repeat([]byte{0x11}, 32)
	expected := ValidationHandshake{
		TransactionID:  journal.TransactionID,
		NonceHash:      nonceHashOf(nonce),
		CandidateHash:  journal.CandidateHash,
		ParentIdentity: ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100},
		EUID:           0,
		LayoutIdentity: layoutIdentityOf(journal),
	}
	parent, child := newMemoryValidationPipes()
	t.Cleanup(func() { _ = parent.Close(); _ = child.Close() })

	t.Run("missing-pipe", func(t *testing.T) {
		err := AcceptValidationHandshake(nil, expected, nonce)
		if apiCode(err) != protocol.CodeInvalidState {
			t.Fatalf("missing pipe: %v", err)
		}
	})
	t.Run("not-root", func(t *testing.T) {
		bad := expected
		bad.EUID = 1000
		err := AcceptValidationHandshake(child, bad, nonce)
		if apiCode(err) != protocol.CodePermissionDenied {
			t.Fatalf("non-root: %v", err)
		}
	})
	t.Run("forged-parent", func(t *testing.T) {
		go writeHandshake(t, parent, expected, nonce)
		forged := expected
		forged.ParentIdentity.PID = 99
		err := AcceptValidationHandshake(child, forged, nonce)
		if apiCode(err) != protocol.CodeInvalidState {
			t.Fatalf("forged parent: %v", err)
		}
	})
	t.Run("eof", func(t *testing.T) {
		p, c := newMemoryValidationPipes()
		_ = p.Close()
		err := AcceptValidationHandshake(c, expected, nonce)
		if apiCode(err) != protocol.CodeInvalidState {
			t.Fatalf("eof: %v", err)
		}
		_ = c.Close()
	})
	t.Run("match", func(t *testing.T) {
		p, c := newMemoryValidationPipes()
		t.Cleanup(func() { _ = p.Close(); _ = c.Close() })
		go writeHandshake(t, p, expected, nonce)
		if err := AcceptValidationHandshake(c, expected, nonce); err != nil {
			t.Fatalf("matching handshake: %v", err)
		}
	})
}

func TestInstallValidation_ReadyJSONNotOrdinaryStatus(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	journal := mustPreparedJournal(t, h)
	status, err := json.Marshal(protocol.Status{Schema: "mihari/v1", ProtocolVersion: "v1", Health: "ok", SetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeValidationReady(status); err == nil {
		t.Fatal("ordinary /v1 status decoded as ready.json")
	}
	if err := MatchValidationReady(journal, ValidationReady{
		TransactionID:  journal.TransactionID,
		DaemonIdentity: ProcessStartIdentity{PID: 8, BootID: testBootID, StartUnix: 200},
		BinaryHash:     journal.CandidateHash,
		LayoutIdentity: layoutIdentityOf(journal),
		Validation:     ValidationOK,
		SetupRequired:  true,
	}); err != nil {
		t.Fatalf("matching ready.json rejected: %v", err)
	}
	if err := MatchValidationReady(journal, ValidationReady{
		TransactionID:  journal.TransactionID,
		DaemonIdentity: ProcessStartIdentity{PID: 8, BootID: testBootID, StartUnix: 200},
		BinaryHash:     journal.CandidateHash,
		LayoutIdentity: layoutIdentityOf(journal),
		Validation:     ValidationFailed,
	}); apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("failed validation: %v", err)
	}
}

func TestInstallValidation_NilChildDoesNotActivate(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Validation = nil
	_, err := h.tx.Apply(context.Background(), h.req)
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("nil validation child: %v", err)
	}
	journal := h.loadedJournalAllowMissing(t)
	if journal.RecoveryAuthority == InstallAuthorityTarget || journal.Phase == InstallPhaseActivationCommitted || journal.Phase == InstallPhaseComplete {
		t.Fatalf("nil child persisted activation: phase=%s authority=%s", journal.Phase, journal.RecoveryAuthority)
	}
}

func TestInstallValidation_ApplyLockedUsesChildLifecycleAndReadyJSON(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	data := &FakeDataLease{held: true}
	child := &FakeValidationChild{
		Result:        ValidationOK,
		SetupRequired: true,
	}
	h.tx.Validation = child
	h.tx.DataLease = data
	h.tx.EUID = 0
	h.tx.ParentIdentity = ProcessStartIdentity{PID: 11, BootID: testBootID, StartUnix: 50}
	got, err := h.tx.Apply(context.Background(), h.req)
	if err != nil {
		t.Fatal(err)
	}
	if got.TransactionID != testTxnID {
		t.Fatalf("result: %+v", got)
	}
	if data.Held() {
		t.Fatal("data/endpoint lock was not released before activation")
	}
	journal := h.loadedJournal(t, h.disk)
	if journal.Phase != InstallPhaseComplete || journal.RecoveryAuthority != InstallAuthorityTarget {
		t.Fatalf("phase=%s authority=%s", journal.Phase, journal.RecoveryAuthority)
	}
	raw, err := h.disk.read(context.Background(), validationReadyPath(testTxnID), maxValidationReadyBytes)
	if err != nil {
		t.Fatalf("ready.json missing: %v", err)
	}
	ready, err := DecodeValidationReady(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Validation != ValidationOK || !ready.SetupRequired || ready.BinaryHash != journal.CandidateHash {
		t.Fatalf("ready=%+v", ready)
	}
	if containsKind(actionKinds(journal), JournalActionValidationStart) == false || containsKind(actionKinds(journal), JournalActionValidationStop) == false {
		t.Fatalf("validation actions missing: %v", actionKinds(journal))
	}
}

func TestInstallValidation_StatusReadyDoesNotActivate(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Validation = &FakeValidationChild{
		WriteStatusReady: true,
	}
	h.tx.DataLease = &FakeDataLease{held: true}
	h.tx.EUID = 0
	h.tx.ParentIdentity = ProcessStartIdentity{PID: 11, BootID: testBootID, StartUnix: 50}
	_, err := h.tx.Apply(context.Background(), h.req)
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("status ready authorized activation: %v", err)
	}
	journal := h.loadedJournalAllowMissing(t)
	if journal.RecoveryAuthority == InstallAuthorityTarget || journal.Phase == InstallPhaseActivationCommitted || journal.Phase == InstallPhaseComplete {
		t.Fatalf("activation persisted from status: %+v", journal)
	}
}

func TestInstallValidation_MissingPipeAndEOFReject(t *testing.T) {
	for _, test := range []struct {
		name  string
		child *FakeValidationChild
	}{
		{name: "missing-pipe", child: &FakeValidationChild{MissingPipe: true}},
		{name: "eof", child: &FakeValidationChild{ClosePipe: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newInstallHarness(t, InstallDataCreate)
			h.tx.Validation = test.child
			h.tx.DataLease = &FakeDataLease{held: true}
			h.tx.EUID = 0
			h.tx.ParentIdentity = ProcessStartIdentity{PID: 1, BootID: testBootID, StartUnix: 1}
			_, err := h.tx.Apply(context.Background(), h.req)
			if apiCode(err) != protocol.CodeInvalidState {
				t.Fatalf("%s: %v", test.name, err)
			}
		})
	}
}

func TestInstallValidation_NoBypassEnv(t *testing.T) {
	t.Setenv("MIHARI_SKIP_INSTALL_VALIDATION", "1")
	t.Setenv("MIHARI_SKIP_VALIDATION", "1")
	t.Setenv("MIHARI_INSTALL_VALIDATION_BYPASS", "1")
	err := AcceptValidationHandshake(nil, ValidationHandshake{EUID: 0, TransactionID: testTxnID}, bytes.Repeat([]byte{1}, 32))
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("bypass env skipped pipe: %v", err)
	}
}

func TestInstallValidation_StalePIDNotReapedAcrossBoot(t *testing.T) {
	recorded := ProcessStartIdentity{PID: 4242, BootID: "boot-a", StartUnix: 10}
	live := ProcessStartIdentity{PID: 4242, BootID: "boot-b", StartUnix: 99}
	if SameProcessStart(recorded, live) {
		t.Fatal("cross-boot PID was treated as the validation child")
	}
	live.BootID = "boot-a"
	live.StartUnix = 10
	if !SameProcessStart(recorded, live) {
		t.Fatal("same-boot identity did not match")
	}
}

func TestUnixBootstrap_PendingAndGreenfield(t *testing.T) {
	pending := InstallJournal{Phase: InstallPhaseDefinitionCommitted}
	if err := CheckDaemonInstallJournal(pending); apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("pending: %v", err)
	}
	if err := CheckDaemonInstallJournal(InstallJournal{Phase: InstallPhaseActivationCommitted}); err != nil {
		t.Fatalf("activation_committed: %v", err)
	}
}

func writeHandshake(t *testing.T, w io.Writer, expected ValidationHandshake, nonce []byte) {
	t.Helper()
	_ = writeValidationJSON(w, validationPipeMessage{
		Nonce:          hexOf(nonce),
		NonceHash:      expected.NonceHash,
		TransactionID:  expected.TransactionID,
		CandidateHash:  expected.CandidateHash,
		ParentIdentity: expected.ParentIdentity,
		LayoutIdentity: expected.LayoutIdentity,
	})
}

func hexOf(raw []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, b := range raw {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}

func mustPreparedJournal(t *testing.T, h *installHarness) InstallJournal {
	t.Helper()
	id := testTxnID
	marker, err := h.tx.Store.CreateTransactionMarker(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := h.tx.buildJournal(h.req, id, marker, h.art)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

var _ ValidationChild = (*FakeValidationChild)(nil)
var _ ValidationLease = (*memoryPipe)(nil)
var _ DataEndpointLease = (*FakeDataLease)(nil)
