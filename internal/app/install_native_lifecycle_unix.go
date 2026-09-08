//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/service"
)

func (s *nativeInstallSession) prepareLifecycle(ctx context.Context, operation string, old service.Definition) error {
	target := old
	switch operation {
	case "start", "restart":
		target.Running = true
		target.Status = service.StatusRunning
	case "stop":
		target.Running = false
		target.Status = service.StatusStopped
	case "uninstall":
		target = service.Definition{Status: service.StatusNotInstalled}
	default:
		return invalidInstallRequest()
	}
	candidateHash, err := s.lifecycleCandidateHash(operation, old)
	if err != nil {
		return err
	}
	data, dataMarker, err := s.observeRetainedData(ctx)
	if err != nil {
		return err
	}
	// A loaded completed transaction already binds D. Never replace that proof
	// with a fresh observation of a different inode or changed private marker.
	if s.state.TransactionID != "" && s.state.Layout.Data.Root == s.layout.Data.Root {
		expected, marker := s.retainedDataExpected()
		if !retainedDataMatches(expected, marker, data, dataMarker, s.tx.Artifacts.BootID) {
			return unknownInstallState()
		}
	}
	id := (&InstallTransaction{}).newTransactionID()
	s.state = nativeInstallState{TransactionID: id, BootID: s.tx.Artifacts.BootID, Layout: s.layout, DataAction: InstallDataRetain, TargetObject: data, DataMarkerHash: dataMarker, Source: s.state.Source, SourceObject: s.state.SourceObject, OldDefinition: old, TargetDefinition: target}
	if err := s.prepareServiceFiles(ctx); err != nil {
		return err
	}
	if err := s.saveState(ctx); err != nil {
		return err
	}
	s.tx.Artifacts.CandidateHash = candidateHash
	marker, err := s.tx.Store.CreateTransactionMarker(ctx, id)
	if err != nil {
		return err
	}
	req := InstallRequest{Operation: InstallOperationRecover, Layout: string(s.layout.Mode)}
	journal, err := s.tx.buildJournal(req, id, marker, s.tx.Artifacts)
	if err != nil {
		return err
	}
	durable, err := s.tx.Store.Save(ctx, journal)
	if err != nil {
		return err
	}
	if !durable.Durable {
		return installBusy("install journal is not durable")
	}
	s.tx.journal = journal
	return nil
}

func (s *nativeInstallSession) lifecycleCandidateHash(operation string, old service.Definition) (string, error) {
	// Stop/uninstall do not execute a binary or require a readable ExecStart;
	// retain only the completed target authority loaded for this exact instance.
	j := s.tx.journal
	if (operation == "stop" || operation == "uninstall") && j.Phase == InstallPhaseComplete && j.RecoveryAuthority == InstallAuthorityTarget && j.TransactionID != "" && j.TransactionID == s.state.TransactionID && s.state.Layout == s.layout && j.Mode == string(s.layout.Mode) && j.TargetPath == s.layout.Data.Root && j.DataRoot == s.layout.Data.Root && j.InstallPath == s.layout.InstallRoot && j.EndpointPath == s.layout.ControlEndpoint && j.CredentialPath == s.layout.CredentialPath && validSHA256(j.CandidateHash) {
		return j.CandidateHash, nil
	}
	if old.Masked {
		return "", unknownInstallState()
	}
	raw, err := readHostFile(old.Binary, migrationBinaryMax)
	if err != nil {
		return "", err
	}
	return sha256HexBytes(raw), nil
}

func (s *nativeInstallSession) runLifecycle(ctx context.Context, operation string) error {
	old, err := s.tx.Service.InspectDefinition(ctx)
	if err != nil {
		return err
	}
	if old.Status == service.StatusNotInstalled {
		if operation == "stop" || operation == "uninstall" {
			return nil
		}
		return installBusy("service is not installed")
	}
	if operation == "start" && old.Running {
		return nil
	}
	if err := s.prepareLifecycle(ctx, operation, old); err != nil {
		return err
	}
	if err := s.tx.lifecycleActions(ctx, operation); err != nil {
		return errors.Join(err, s.tx.RecoverLocked(context.WithoutCancel(ctx), s))
	}
	return nil
}
