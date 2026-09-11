//go:build linux || darwin

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"os"
	"path/filepath"
)

type replayValidationLease struct {
	ValidationLease
	reader io.Reader
}

func (p replayValidationLease) Read(b []byte) (int, error) { return p.reader.Read(b) }

// RunInheritedValidation is the root-only child entry. Journal routing comes
// from its inherited anonymous pipe; authentication completes before data IO.
// The callback borrows the daemon lease and must close its own runtime resources
// before returning. This entry releases the daemon lease after that return.
func RunInheritedValidation(ctx context.Context, id string, run func(context.Context, platform.ResolvedLayout, *platform.OwnedDaemonLease, func(bool) error) error) (resultErr error) {
	if os.Geteuid() != 0 {
		return errValidationNotRoot
	}
	if !validTransactionID(id) {
		return errValidationHandshake
	}
	inherited, err := InheritedValidationLease()
	if err != nil {
		return err
	}
	lease := &ownedValidationLease{ValidationLease: inherited}
	transferred := false
	defer func() {
		if !transferred {
			resultErr = errors.Join(resultErr, lease.Close())
		}
	}()
	watchExit := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchExit)
		select {
		case <-ctx.Done():
			// The current owner returns the cached close error.
			_ = lease.Close()
		case <-watchDone:
		}
	}()
	watchStopped := false
	stopWatch := func() {
		if !watchStopped {
			close(watchDone)
			<-watchExit
			watchStopped = true
		}
	}
	defer stopWatch()
	msg, err := readValidationJSON(lease)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(msg.JournalRoot) || filepath.Clean(msg.JournalRoot) != msg.JournalRoot || msg.TransactionID != id {
		return errValidationHandshake
	}
	mode := uint32(0700)
	if msg.JournalRoot == defaultValidationBase() {
		mode = 0711
	}
	root, err := platform.OpenTrustedRoot(ctx, msg.JournalRoot, platform.RootPolicy{Owner: 0, Mode: mode})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	store, err := NewInstallJournalStore(ctx, root)
	if err != nil {
		return err
	}
	j, err := store.Load(ctx)
	if err != nil {
		return err
	}
	layout, err := validationLayout(j)
	if err != nil {
		return err
	}
	own, err := identifyValidationProcess(ctx, os.Getpid())
	if err != nil {
		return err
	}
	parent, err := identifyValidationProcess(ctx, os.Getppid())
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Clean(executable) != filepath.Join(j.InstallPath, "mihari") {
		return errValidationHandshake
	}
	hash, err := trustedValidationBinaryHash(ctx, executable)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(msg)
	raw = append(raw, '\n')
	replay := replayValidationLease{ValidationLease: lease, reader: io.MultiReader(bytes.NewReader(raw), lease)}
	// RunValidationDaemon now owns the pipe and its cancellation watcher. The
	// routing entry retains the trusted journal root until the child has joined.
	stopWatch()
	transferred = true
	return RunValidationDaemon(ctx, ValidationDaemonOptions{Lease: replay, Store: store, TransactionID: id, EUID: uint32(os.Geteuid()), ParentIdentity: parent, SelfIdentity: own, BinaryHash: hash, LayoutIdentity: layoutIdentityOf(j), Run: func(ctx context.Context, _ InstallJournal, ready func(bool) error) (err error) {
		locks, err := platform.AcquireDaemonLease(ctx, layout)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, locks.Close()) }()
		// The callback borrows locks; this entry owns their final release.
		return run(ctx, layout, locks, ready)
	}})
}

func defaultValidationBase() string { return platform.SystemLayoutDefaults().BaseDir }
func validationLayout(j InstallJournal) (platform.ResolvedLayout, error) {
	defaults := platform.SystemLayoutDefaults()
	input := platform.LayoutInput{EUID: 0, Endpoint: j.EndpointPath, Credential: j.CredentialPath, InstallRoot: j.InstallPath}
	if j.Mode == InstallLayoutPrivate {
		input.Data = j.TargetPath
	} else if j.Mode != InstallLayoutSystem {
		return platform.ResolvedLayout{}, errValidationHandshake
	}
	layout, err := platform.ResolveLayout(input, defaults)
	if err != nil {
		return layout, err
	}
	if layout.Data.Root != j.TargetPath || j.DataRoot != j.TargetPath {
		return layout, errValidationHandshake
	}
	return layout, nil
}
