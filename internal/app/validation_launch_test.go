package app

import (
	"context"
	"encoding/json"
	"testing"
)

type validationReaper struct {
	FakeValidationChild
	ids []ProcessStartIdentity
}

func (r *validationReaper) ReapValidation(_ context.Context, id ProcessStartIdentity, _ InstallJournal) error {
	r.ids = append(r.ids, id)
	return nil
}

func TestInstallValidation_RecoverReapsDurableLaunchBeforeReady(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	j := mustPreparedJournal(t, h)
	j.Phase = InstallPhaseDefinitionCommitted
	id := ProcessStartIdentity{PID: 18, BootID: testBootID, StartUnix: 200}
	launch := validationLaunch{TransactionID: j.TransactionID, NonceHash: sha256Hex("nonce"), Parent: ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100}, Child: id, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j)}
	raw, _ := json.Marshal(launch)
	if _, err := h.disk.write(context.Background(), "transactions/"+j.TransactionID+"/validation-launch.json", raw, JournalObject{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tx.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	r := &validationReaper{}
	h.tx.Validation = r
	lease, err := h.lease.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeInstallLease(lease)
	if err := h.tx.RecoverLocked(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if len(r.ids) != 1 || !SameProcessStart(r.ids[0], id) {
		t.Fatalf("recovery lost child before ready: %v", r.ids)
	}
}

type validationProcessProbe struct {
	live         ProcessStartIdentity
	stops, locks int
}

func (p *validationProcessProbe) Identify(context.Context, int) (ProcessStartIdentity, error) {
	return p.live, nil
}
func (p *validationProcessProbe) StopAndWait(context.Context, ProcessStartIdentity) error {
	p.stops++
	return nil
}
func (p *validationProcessProbe) WaitLocks(context.Context, InstallJournal) error {
	p.locks++
	return nil
}
func TestInstallValidation_ProcessRecoveryDoesNotSignalReusedPID(t *testing.T) {
	recorded := ProcessStartIdentity{PID: 18, BootID: testBootID, StartUnix: 200}
	for _, tc := range []struct {
		name  string
		live  ProcessStartIdentity
		stops int
	}{{"matching", recorded, 1}, {"new-start", ProcessStartIdentity{PID: 18, BootID: testBootID, StartUnix: 201}, 0}, {"new-boot", ProcessStartIdentity{PID: 18, BootID: "new-boot", StartUnix: 200}, 0}, {"gone", ProcessStartIdentity{}, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &validationProcessProbe{live: tc.live}
			if err := recoverValidationProcess(context.Background(), recorded, InstallJournal{}, p); err != nil {
				t.Fatal(err)
			}
			if p.stops != tc.stops || p.locks != 1 {
				t.Fatalf("identity-aware recovery: stop=%d locks=%d", p.stops, p.locks)
			}
		})
	}
}
