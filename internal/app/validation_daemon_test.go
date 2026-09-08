package app

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type observedValidationRead struct {
	ValidationLease
	read chan struct{}
	once sync.Once
}

func (p *observedValidationRead) Read(b []byte) (int, error) {
	p.once.Do(func() { close(p.read) })
	return p.ValidationLease.Read(b)
}

func TestInstallValidation_EarlyChildWaitsForDurableIdentity(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	j := mustPreparedJournal(t, h)
	j.Phase = InstallPhaseDefinitionCommitted
	nonce := bytes.Repeat([]byte{2}, 32)
	j.Actions = []JournalAction{{Seq: 1, Kind: JournalActionValidationStart, TargetRole: JournalRoleValidation, OldState: "idle", NewState: "running", CandidateRef: nonceHashOf(nonce), Status: JournalActionIntent}}
	if _, err := h.tx.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	parent := ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100}
	child := ProcessStartIdentity{PID: 8, BootID: testBootID, StartUnix: 200}
	launch := validationLaunch{TransactionID: j.TransactionID, NonceHash: nonceHashOf(nonce), Parent: parent, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j)}
	if err := h.tx.Store.saveValidationLaunch(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	p, c := newMemoryValidationPipes()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { _ = p.Close(); _ = c.Close() }() // In-memory pipes have no close failures.
	observed := &observedValidationRead{ValidationLease: c, read: make(chan struct{})}
	done := make(chan error, 1)
	opened := make(chan struct{})
	go func() {
		done <- RunValidationDaemon(ctx, ValidationDaemonOptions{Lease: observed, Store: h.tx.Store, TransactionID: j.TransactionID, ParentIdentity: parent, SelfIdentity: child, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j), Run: func(ctx context.Context, _ InstallJournal, ready func(bool) error) error {
			close(opened)
			if err := ready(false); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		}})
	}()
	// Run the child before the parent can publish Child or write its handshake.
	select {
	case err := <-done:
		t.Fatalf("child rejected preliminary launch instead of waiting: %v", err)
	case <-observed.read:
	case <-time.After(time.Second):
		t.Fatal("child did not reach handshake read")
	}
	select {
	case <-opened:
		t.Fatal("data opened before durable child identity")
	default:
	}
	launch.Child = child
	if err := h.tx.Store.saveValidationLaunch(ctx, launch); err != nil {
		t.Fatal(err)
	}
	hs := ValidationHandshake{TransactionID: j.TransactionID, NonceHash: launch.NonceHash, CandidateHash: j.CandidateHash, ParentIdentity: parent, LayoutIdentity: layoutIdentityOf(j)}
	writeHandshake(t, p, hs, nonce)
	if msg, err := readValidationJSON(p); err != nil || !msg.OK {
		t.Fatalf("ready=%+v err=%v", msg, err)
	}
	if err := writeValidationJSON(p, validationPipeMessage{Stop: true}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("requested stop failed: %v", err)
	}
}

func TestInstallValidation_ProductionChildAuthenticatesBeforeIO(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	j := mustPreparedJournal(t, h)
	j.Phase = InstallPhaseDefinitionCommitted
	nonce := bytes.Repeat([]byte{2}, 32)
	j.Actions = []JournalAction{{Seq: 1, Kind: JournalActionValidationStart, TargetRole: JournalRoleValidation, OldState: "idle", NewState: "running", CandidateRef: nonceHashOf(nonce), Status: JournalActionIntent}}
	if _, err := h.tx.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	parentID := ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100}
	self := ProcessStartIdentity{PID: 8, BootID: testBootID, StartUnix: 200}
	launch, _ := json.Marshal(map[string]any{"transaction_id": j.TransactionID, "nonce_hash": nonceHashOf(nonce), "parent": parentID, "child": self, "binary_hash": j.CandidateHash, "layout_identity": layoutIdentityOf(j)})
	if _, err := h.disk.write(context.Background(), "transactions/"+j.TransactionID+"/validation-launch.json", launch, JournalObject{}); err != nil {
		t.Fatal(err)
	}
	p, c := newMemoryValidationPipes()
	defer func() { _ = p.Close() }() // Memory pipe cleanup cannot fail.
	// RunValidationDaemon owns c after the call starts.
	hs := ValidationHandshake{TransactionID: j.TransactionID, NonceHash: nonceHashOf(nonce), CandidateHash: j.CandidateHash, ParentIdentity: parentID, LayoutIdentity: layoutIdentityOf(j)}
	entered := make(chan struct{})
	exited := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RunValidationDaemon(context.Background(), ValidationDaemonOptions{Lease: c, Store: h.tx.Store, TransactionID: j.TransactionID, ParentIdentity: parentID, SelfIdentity: self, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j), Run: func(ctx context.Context, _ InstallJournal, ready func(bool) error) error {
			close(entered)
			defer close(exited)
			if err := ready(true); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		}})
	}()
	go writeHandshake(t, p, hs, nonce)
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("authenticated child never initialized: %v", err)
	case <-time.After(time.Second):
		t.Fatal("child stalled")
	}
	if _, err := readValidationJSON(p); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("child survived parent EOF after readiness")
	}
	select {
	case <-exited:
	default:
		t.Fatal("child returned before owned runtime joined")
	}
	raw, err := h.disk.read(context.Background(), validationReadyPath(j.TransactionID), maxValidationReadyBytes)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := DecodeValidationReady(raw)
	if err != nil || !ready.SetupRequired || !SameProcessStart(ready.DaemonIdentity, self) {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
}

func TestInstallValidation_ProductionChildRequiresDurableLaunch(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	j := mustPreparedJournal(t, h)
	j.Phase = InstallPhaseDefinitionCommitted
	nonce := bytes.Repeat([]byte{2}, 32)
	j.Actions = []JournalAction{{Seq: 1, Kind: JournalActionValidationStart, TargetRole: JournalRoleValidation, OldState: "idle", NewState: "running", CandidateRef: nonceHashOf(nonce), Status: JournalActionIntent}}
	if _, err := h.tx.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	parentID := ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100}
	self := ProcessStartIdentity{PID: 8, BootID: testBootID, StartUnix: 200}
	p, c := newMemoryValidationPipes()
	defer func() { _ = p.Close() }() // Memory pipe cleanup cannot fail.
	// RunValidationDaemon owns c after the call starts.
	hs := ValidationHandshake{TransactionID: j.TransactionID, NonceHash: nonceHashOf(nonce), CandidateHash: j.CandidateHash, ParentIdentity: parentID, LayoutIdentity: layoutIdentityOf(j)}
	writer := make(chan struct{})
	go func() { defer close(writer); writeHandshake(t, p, hs, nonce) }()
	opened := false
	err := RunValidationDaemon(context.Background(), ValidationDaemonOptions{Lease: c, Store: h.tx.Store, TransactionID: j.TransactionID, ParentIdentity: parentID, SelfIdentity: self, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j), Run: func(context.Context, InstallJournal, func(bool) error) error { opened = true; return nil }})
	_ = p.Close()
	<-writer
	if opened || err == nil {
		t.Fatalf("missing durable parent/child launch opened data: opened=%v err=%v", opened, err)
	}
}
