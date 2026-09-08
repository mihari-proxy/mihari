package app

import (
	"context"
	"errors"
)

// ForegroundBootstrap owns the root foreground install lease through validation
// and durable activation. Non-root portable instances use their ordinary run.
type ForegroundBootstrap struct {
	Root        bool
	Transaction *InstallTransaction
	// DiscoverSource must inspect legacy service/source state before CreateData.
	DiscoverSource func(context.Context) (bool, error)
	CreateData     func(context.Context) error
	// InitializeJournal prepares only immutable recovery metadata. Staging must
	// wait for PrepareJournal, after this metadata has a durable journal.
	InitializeJournal func(context.Context, string) error
	PrepareJournal    func(context.Context, string) error
	Run               func(context.Context, string) error
}

// Start validates and activates a greenfield root instance, then enters its
// normal daemon after releasing the install lock. It never migrates a source.
func (b ForegroundBootstrap) Start(ctx context.Context) (resultErr error) {
	if b.Run == nil {
		return installBusy("foreground daemon is unavailable")
	}
	if !b.Root {
		return b.Run(ctx, "")
	}
	x := b.Transaction
	if x == nil || x.Acquire == nil {
		return installBusy("bootstrap install lock is unavailable")
	}
	lease, err := x.Acquire(ctx)
	if err != nil {
		return err
	}
	released := false
	release := func() error {
		if released {
			return nil
		}
		released = true
		if c, ok := lease.(interface{ Close() error }); ok {
			return c.Close()
		}
		return nil
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	if err := lease.Validate(ctx); err != nil {
		return err
	}
	if x.Store == nil || x.Store.files == nil {
		return invalidInstallJournal()
	}
	object, err := x.Store.files.inspect(ctx, installJournalFileName)
	if err != nil {
		return err
	}
	if object.Present {
		j, err := x.Store.Load(ctx)
		if err != nil {
			return err
		}
		if InstallPending(j) {
			return installBusy("install recovery required")
		}
		if j.RecoveryAuthority != InstallAuthorityTarget || (j.Phase != InstallPhaseActivationCommitted && j.Phase != InstallPhaseComplete) {
			return installBusy("install recovery required")
		}
		mode := InstallLayoutSystem
		if x.Private {
			mode = InstallLayoutPrivate
		}
		if j.Mode != mode || j.TargetPath != x.Artifacts.Target || j.DataRoot != x.Artifacts.DataRoot || j.InstallPath != x.Artifacts.Install || j.EndpointPath != x.Artifacts.Endpoint || j.CredentialPath != x.Artifacts.Credential {
			return installBusy("install recovery required")
		}
		if j.CandidateHash != x.Artifacts.CandidateHash {
			return installBusy("foreground binary does not match activation")
		}
		if err := release(); err != nil {
			return err
		}
		return b.Run(ctx, j.Phase)
	}
	if b.DiscoverSource == nil || b.CreateData == nil {
		return installBusy("bootstrap discovery is unavailable")
	}
	source, err := b.DiscoverSource(ctx)
	if err != nil {
		return err
	}
	if source {
		return installBusy("legacy source requires migration")
	}
	id := x.newTransactionID()
	marker, err := x.Store.CreateTransactionMarker(ctx, id)
	if err != nil {
		return err
	}
	layout := InstallLayoutSystem
	if x.Private {
		layout = InstallLayoutPrivate
	}
	request := InstallRequest{Operation: InstallOperationInstall, Layout: layout}
	if b.InitializeJournal != nil {
		if err := b.InitializeJournal(ctx, id); err != nil {
			return err
		}
	}
	saveJournal := func() error {
		art := x.preparedArtifacts(request)
		j, err := x.buildJournal(request, id, marker, art)
		if err != nil {
			return err
		}
		durable, err := x.Store.Save(ctx, j)
		if err != nil {
			return err
		}
		if !durable.Durable {
			return installBusy("install journal is not durable")
		}
		x.journal = j
		return nil
	}
	if err := saveJournal(); err != nil {
		return err
	}
	if b.PrepareJournal != nil {
		if err := b.PrepareJournal(ctx, id); err != nil {
			return err
		}
		if err := saveJournal(); err != nil {
			return err
		}
	}
	if publisher, ok := x.Effects.(interface {
		PublishDataLocked(context.Context, *InstallTransaction) error
	}); ok {
		if err := publisher.PublishDataLocked(ctx, x); err != nil {
			return err
		}
	} else {
		action := JournalAction{Kind: JournalActionDataPublish, TargetRole: JournalRoleData, OldState: "absent", NewState: "created"}
		if err := x.step(ctx, action, b.CreateData); err != nil {
			return err
		}
	}
	if x.AfterDataCommitted != nil {
		if err := x.AfterDataCommitted(ctx); err != nil {
			return err
		}
	}
	if err := x.setPhase(ctx, InstallPhaseDefinitionCommitted); err != nil {
		return err
	}
	if err := x.runValidation(ctx); err != nil {
		return err
	}
	if err := x.activate(ctx); err != nil {
		return err
	}
	if err := release(); err != nil {
		return err
	}
	return b.Run(ctx, InstallPhaseActivationCommitted)
}
