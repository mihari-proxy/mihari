package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// ValidationDaemonOptions supplies process observations and a trusted journal.
// Native assembly captures identities from the kernel, never from CLI input.
type ValidationDaemonOptions struct {
	Lease                        ValidationLease
	Store                        *InstallJournalStore
	TransactionID                string
	EUID                         uint32
	ParentIdentity, SelfIdentity ProcessStartIdentity
	BinaryHash, LayoutIdentity   string
	// Run owns and closes all data/endpoint/log/IPC resources before it returns.
	// Ready is called only after all read-only validation and IPC initialization.
	Run func(context.Context, InstallJournal, func(bool) error) error
}

// RunValidationDaemon consumes Lease, authenticates before daemon data IO, and
// closes the pipe exactly once after joining its workers on every return path.
func RunValidationDaemon(ctx context.Context, options ValidationDaemonOptions) (resultErr error) {
	if options.Lease == nil {
		return errMissingValidationPipe
	}
	options.Lease = &ownedValidationLease{ValidationLease: options.Lease}
	defer func() { resultErr = errors.Join(resultErr, options.Lease.Close()) }()
	if options.EUID != 0 {
		return errValidationNotRoot
	}
	if options.Store == nil || options.Run == nil {
		return errValidationHandshake
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Closing the pipe cancels even an incomplete handshake; this worker is joined.
	watchDone := make(chan struct{})
	watchExit := make(chan struct{})
	go func() {
		defer close(watchExit)
		select {
		case <-ctx.Done():
			// Final cleanup returns the cached close error after joining this worker.
			_ = options.Lease.Close()
		case <-watchDone:
		}
	}()
	defer func() { close(watchDone); <-watchExit }()
	// The parent publishes the exact spawned identity before sending this frame.
	// An early child must wait here before examining the preliminary launch.
	msg, err := readValidationJSON(options.Lease)
	if err != nil {
		return err
	}
	j, err := options.Store.Load(ctx)
	if err != nil {
		return err
	}
	if j.TransactionID != options.TransactionID || j.Phase != InstallPhaseDefinitionCommitted || options.BinaryHash != j.CandidateHash || options.LayoutIdentity != layoutIdentityOf(j) || !validProcessStart(options.SelfIdentity) || !validProcessStart(options.ParentIdentity) {
		return errValidationHandshake
	}
	var nonceHash string
	for _, action := range j.Actions {
		if action.Kind == JournalActionValidationStart {
			nonceHash = action.CandidateRef
		}
		if action.Kind == JournalActionValidationStop {
			nonceHash = ""
		}
	}
	if !validSHA256(nonceHash) {
		return errValidationHandshake
	}
	launch, present, err := options.Store.loadValidationLaunch(ctx, j)
	if err != nil {
		return err
	}
	if !present || launch.NonceHash != nonceHash || !SameProcessStart(launch.Parent, options.ParentIdentity) || !SameProcessStart(launch.Child, options.SelfIdentity) {
		return errValidationHandshake
	}
	hs := ValidationHandshake{TransactionID: j.TransactionID, NonceHash: nonceHash, CandidateHash: j.CandidateHash, ParentIdentity: options.ParentIdentity, EUID: options.EUID, LayoutIdentity: layoutIdentityOf(j)}
	if err := validateValidationHandshake(msg, hs, nil); err != nil {
		return err
	}
	pipeDone := make(chan error, 1)
	go func() {
		msg, err := readValidationJSON(options.Lease)
		if err == nil && !msg.Stop {
			err = errValidationHandshake
		}
		pipeDone <- err
		cancel()
	}()
	var once sync.Once
	published := false
	ready := func(setup bool) (err error) {
		once.Do(func() {
			raw, _ := json.Marshal(ValidationReady{TransactionID: j.TransactionID, DaemonIdentity: options.SelfIdentity, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j), Validation: ValidationOK, SetupRequired: setup})
			_, err = options.Store.files.write(ctx, validationReadyPath(j.TransactionID), raw, JournalObject{})
			if err == nil {
				err = writeValidationJSON(options.Lease, validationPipeMessage{OK: true})
			}
			published = err == nil
		})
		if !published && err == nil {
			return errValidationFailed
		}
		return err
	}
	runErr := options.Run(ctx, j, ready)
	cancel()
	_ = options.Lease.Close()
	pipeErr := <-pipeDone
	if runErr != nil && !published {
		raw, _ := json.Marshal(ValidationReady{TransactionID: j.TransactionID, DaemonIdentity: options.SelfIdentity, BinaryHash: j.CandidateHash, LayoutIdentity: layoutIdentityOf(j), Validation: ValidationFailed})
		_, writeErr := options.Store.files.write(context.WithoutCancel(ctx), validationReadyPath(j.TransactionID), raw, JournalObject{})
		runErr = errors.Join(runErr, writeErr)
	}
	return errors.Join(runErr, pipeErr)
}

func validProcessStart(id ProcessStartIdentity) bool {
	return id.PID > 0 && id.BootID != "" && id.StartUnix > 0
}
