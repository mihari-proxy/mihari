package app

import (
	"bytes"
	"context"
	"encoding/json"
)

type validationLaunch struct {
	TransactionID  string               `json:"transaction_id"`
	NonceHash      string               `json:"nonce_hash"`
	Parent         ProcessStartIdentity `json:"parent"`
	Child          ProcessStartIdentity `json:"child"`
	BinaryHash     string               `json:"binary_hash"`
	LayoutIdentity string               `json:"layout_identity"`
}

// ValidationRecovery reaps only the recorded process and waits for its locks.
type ValidationRecovery interface {
	ReapValidation(context.Context, ProcessStartIdentity, InstallJournal) error
}

func validationLaunchPath(id string) string { return "transactions/" + id + "/validation-launch.json" }
func (s *InstallJournalStore) validationRoot() string {
	if files, ok := s.files.(interface{ validationRoot() string }); ok {
		return files.validationRoot()
	}
	return ""
}
func (s *InstallJournalStore) saveValidationLaunch(ctx context.Context, launch validationLaunch) error {
	raw, err := json.Marshal(launch)
	if err != nil {
		return err
	}
	old, err := s.files.inspect(ctx, validationLaunchPath(launch.TransactionID))
	if err != nil {
		return err
	}
	_, err = s.files.write(ctx, validationLaunchPath(launch.TransactionID), raw, old)
	return err
}
func (s *InstallJournalStore) loadValidationLaunch(ctx context.Context, j InstallJournal) (validationLaunch, bool, error) {
	name := validationLaunchPath(j.TransactionID)
	obj, err := s.files.inspect(ctx, name)
	if err != nil || !obj.Present {
		return validationLaunch{}, false, err
	}
	raw, err := s.files.read(ctx, name, maxValidationReadyBytes)
	if err != nil {
		return validationLaunch{}, false, err
	}
	var launch validationLaunch
	if _, err := decodeStrictJSON(bytes.NewReader(raw), maxValidationReadyBytes, &launch); err != nil {
		return launch, false, errValidationHandshake
	}
	if launch.TransactionID != j.TransactionID || launch.BinaryHash != j.CandidateHash || launch.LayoutIdentity != layoutIdentityOf(j) || !validSHA256(launch.NonceHash) || !validProcessStart(launch.Parent) {
		return launch, false, errValidationHandshake
	}
	return launch, true, nil
}
func (x *InstallTransaction) recoverValidation(ctx context.Context) error {
	if x.hasDoneKind(JournalActionValidationStop) || x.hasDoneKind(JournalActionRestoreValidation) {
		return nil
	}
	launch, present, err := x.Store.loadValidationLaunch(ctx, x.journal)
	if err != nil || !present {
		return err
	}
	if launch.Child == (ProcessStartIdentity{}) {
		return nil
	} // no handshake released: no data IO possible
	if !validProcessStart(launch.Child) {
		return errValidationHandshake
	}
	reaper, ok := x.Validation.(ValidationRecovery)
	if !ok {
		return installBusy("validation process recovery is unavailable")
	}
	return reaper.ReapValidation(ctx, launch.Child, x.journal)
}

type validationProcessRecovery interface {
	Identify(context.Context, int) (ProcessStartIdentity, error)
	StopAndWait(context.Context, ProcessStartIdentity) error
	WaitLocks(context.Context, InstallJournal) error
}

func recoverValidationProcess(ctx context.Context, id ProcessStartIdentity, j InstallJournal, p validationProcessRecovery) error {
	live, err := p.Identify(ctx, id.PID)
	if err != nil {
		return err
	}
	if SameProcessStart(id, live) {
		if err := p.StopAndWait(ctx, id); err != nil {
			return err
		}
	}
	return p.WaitLocks(ctx, j)
}
