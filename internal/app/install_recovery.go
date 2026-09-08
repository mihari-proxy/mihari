package app

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
)

func recoveryStopAuthority(journal InstallJournal, old, target service.Definition, recordedBoot, currentBoot string) (service.Definition, string) {
	final := old
	if journal.RecoveryAuthority == InstallAuthorityTarget {
		final = target
	}
	// The backup describes the generation before stop. A completed installed
	// state, or a possibly executed bootstrap, cannot prove a later vanished
	// generation exited. Keep it explicitly unknown in the current boot.
	unknown := journal.Phase == InstallPhaseComplete && final.Status != service.StatusNotInstalled
	for _, action := range journal.Actions {
		if journal.RecoveryAuthority == InstallAuthorityTarget && action.Kind == JournalActionStart || journal.RecoveryAuthority == InstallAuthoritySource && action.Kind == JournalActionRestoreStart {
			unknown = true
		}
	}
	if unknown {
		final.Process = service.ProcessIdentity{}
		if final.Status == service.StatusNotInstalled {
			final.Status = service.StatusUnknown
		}
		return final, currentBoot
	}
	return old, recordedBoot
}

func (x *InstallTransaction) bindRecoveryStopAuthority() {
	if x.preparedAuthority == nil {
		return
	}
	if binder, ok := x.Service.(interface {
		BindStopAuthority(service.Definition, string)
	}); ok {
		def, boot := recoveryStopAuthority(x.journal, x.preparedAuthority.OldDefinition, x.preparedAuthority.TargetDefinition, x.journal.BootID, x.Artifacts.BootID)
		binder.BindStopAuthority(def, boot)
	}
}

// RecoverLocked restores a durable journal under a borrowed install lease.
func (x *InstallTransaction) RecoverLocked(ctx context.Context, lease InstallLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease == nil {
		return installBusy("install lock is unavailable")
	}
	if err := lease.Validate(ctx); err != nil {
		return installClosed(err)
	}
	if x.Store == nil || x.Store.files == nil {
		return nil
	}
	object, err := x.Store.files.inspect(ctx, installJournalFileName)
	if err != nil {
		return err
	}
	if !object.Present {
		return nil
	}
	journal, err := x.Store.Load(ctx)
	if err != nil {
		return err
	}
	x.journal = journal
	x.stepN = 0
	if x.preparedAuthority != nil && journal.Phase == InstallPhaseComplete {
		return nil
	}
	if err := x.recoverValidation(ctx); err != nil {
		return err
	}
	if journal.RecoveryAuthority == InstallAuthorityTarget {
		return x.rollforward(ctx)
	}
	return x.rollback(ctx)
}

// InstallPending is true for prepared through definition_committed.
// Ordinary daemons must not take the install lock when this is true.
func InstallPending(journal InstallJournal) bool {
	switch journal.Phase {
	case InstallPhasePrepared, InstallPhaseStopped, InstallPhaseDataCommitted, InstallPhaseBinariesCommitted, InstallPhaseDefinitionCommitted:
		return true
	default:
		return false
	}
}

// CheckDaemonInstallJournal is the ordinary-daemon journal gate. It never
// acquires the install lock and returns immediately.
func CheckDaemonInstallJournal(journal InstallJournal) error {
	if InstallPending(journal) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install recovery required"}
	}
	return nil
}

func (x *InstallTransaction) rollback(ctx context.Context) error {
	x.bindRecoveryStopAuthority()
	if x.serviceEffects != nil {
		x.recoveringSource = true
		defer func() { x.recoveringSource = false }()
	}
	if x.BeforeRollback != nil {
		if err := x.BeforeRollback(ctx); err != nil {
			return err
		}
	}
	actions := append([]JournalAction(nil), x.journal.Actions...)
	reversed := false
	for i := len(actions) - 1; i >= 0; i-- {
		action := actions[i]
		if x.serviceEffects != nil && (action.Kind == JournalActionStop || action.Kind == JournalActionStart || action.Kind == JournalActionRestoreStop || action.Kind == JournalActionRestoreStart) {
			continue
		}
		if action.Kind == JournalActionReload || action.Kind == JournalActionRestoreReload {
			continue
		}
		if isReverseAction(action.Kind) {
			if err := x.finishAction(ctx, action); err != nil {
				return err
			}
			continue
		}
		if x.alreadyReversed(action) {
			continue
		}
		actual, err := x.observe(ctx, action)
		if err != nil {
			return err
		}
		progress, err := classifyInstallAction(action, actual)
		if err != nil {
			return err
		}
		if progress == ActionNotStarted || reverseKind(action.Kind) == "" {
			continue
		}
		if err := x.reverse(ctx, action); err != nil {
			return err
		}
		reversed = true
	}
	if reversed {
		n := x.currentReload(ctx)
		restore := JournalAction{Kind: JournalActionRestoreReload, TargetRole: JournalRoleDefinition, OldState: reloadToken(n), NewState: reloadToken(n + 1)}
		if err := x.step(ctx, restore, func(ctx context.Context) error {
			return x.applyEffect(ctx, restore)
		}); err != nil {
			return err
		}
	}
	if x.serviceEffects != nil && x.preparedAuthority != nil {
		old := x.preparedAuthority.OldDefinition
		if err := x.Service.RestoreDefinition(ctx, old); err != nil {
			return err
		}
		if old.Running {
			if err := x.Service.Start(ctx); err != nil {
				return err
			}
		}
	}
	x.journal.RecoveryAuthority = InstallAuthoritySource
	return x.setPhase(ctx, InstallPhaseComplete)
}

func (x *InstallTransaction) rollforward(ctx context.Context) error {
	x.bindRecoveryStopAuthority()
	if verifier, ok := x.Service.(interface{ WaitRecordedTreeExit(context.Context) error }); ok {
		if err := verifier.WaitRecordedTreeExit(ctx); err != nil {
			return err
		}
	}
	actions := append([]JournalAction(nil), x.journal.Actions...)
	for _, action := range actions {
		if err := x.finishAction(ctx, action); err != nil {
			return err
		}
	}
	if err := x.ensureTarget(ctx); err != nil {
		return err
	}
	x.journal.RecoveryAuthority = InstallAuthorityTarget
	return x.setPhase(ctx, InstallPhaseComplete)
}

func (x *InstallTransaction) finishAction(ctx context.Context, action JournalAction) error {
	current, ok := x.actionBySeq(action.Seq)
	if !ok {
		return nil
	}
	if current.Status == JournalActionDone {
		return nil
	}
	actual, err := x.observe(ctx, current)
	if err != nil {
		return err
	}
	progress, err := classifyInstallAction(current, actual)
	if err != nil {
		return err
	}
	if progress == ActionNotStarted {
		if err := x.applyEffect(ctx, current); err != nil {
			return err
		}
	}
	done := current
	done.Status = JournalActionDone
	_, err = x.Store.RecordAction(ctx, &x.journal, done)
	return err
}

func (x *InstallTransaction) reverse(ctx context.Context, forward JournalAction) error {
	kind := reverseKind(forward.Kind)
	if kind == "" {
		return nil
	}
	if forward.Kind == JournalActionDataPublish && x.journal.DataAction == InstallDataRetain {
		return nil
	}
	oldState, newState := forward.NewState, forward.OldState
	switch kind {
	case JournalActionRestoreReload:
		n := x.currentReload(ctx)
		oldState, newState = reloadToken(n), reloadToken(n+1)
	case JournalActionDataIsolate, JournalActionRestoreData:
		observed := forward
		observed.Kind = JournalActionDataPublish
		actual, err := x.observe(ctx, observed)
		if err != nil {
			return err
		}
		oldState, newState = actual, "absent"
		if actual == "absent" {
			return nil
		}
	}
	restore := JournalAction{
		Kind:         kind,
		TargetRole:   forward.TargetRole,
		OldState:     oldState,
		NewState:     newState,
		BackupRef:    forward.BackupRef,
		CandidateRef: forward.CandidateRef,
	}
	return x.step(ctx, restore, func(ctx context.Context) error {
		return x.applyEffect(ctx, restore)
	})
}

func (x *InstallTransaction) ensureTarget(ctx context.Context) error {
	if x.preparedAuthority != nil && x.serviceEffects == nil && x.preparedAuthority.TargetDefinition.Status == service.StatusNotInstalled {
		return nil
	}
	if x.serviceEffects != nil && x.preparedAuthority != nil {
		target := x.preparedAuthority.TargetDefinition
		if err := x.removeObsoleteDefinitions(ctx); err != nil {
			return err
		}
		if target.Status == service.StatusNotInstalled {
			return x.reloadStep(ctx)
		}
		if err := x.Service.WriteDefinition(ctx, target); err != nil {
			return err
		}
		if target.Running {
			return x.Service.Start(ctx)
		}
		return nil
	}
	art := x.Artifacts
	if art.TargetEnabled || art.OldEnabled {
		if !art.TargetEnabled && reqKeepsEnabled(x.journal) {
			art.TargetEnabled = true
		}
	}
	if x.journal.Operation == InstallOperationUpdate {
		art.TargetEnabled = x.journal.OldEnabled
		art.TargetRunning = x.journal.OldRunning
	}
	if x.journal.Operation == InstallOperationReinstall {
		art.TargetEnabled, art.TargetRunning = true, true
	}
	if x.journal.Operation == InstallOperationInstall {
		art.TargetEnabled, art.TargetRunning = true, false
	}
	if err := x.ensureAction(ctx, JournalAction{Kind: JournalActionUnmask, TargetRole: JournalRoleDefinition, OldState: "masked", NewState: "unmasked"}); err != nil {
		return err
	}
	if art.TargetEnabled {
		if err := x.ensureAction(ctx, JournalAction{Kind: JournalActionEnable, TargetRole: JournalRoleDefinition, OldState: "disabled", NewState: "enabled"}); err != nil {
			return err
		}
	}
	if err := x.ensureReload(ctx); err != nil {
		return err
	}
	if art.TargetRunning {
		if err := x.ensureAction(ctx, JournalAction{Kind: JournalActionStart, TargetRole: JournalRoleDefinition, OldState: "stopped", NewState: "running"}); err != nil {
			return err
		}
	}
	return nil
}

func (x *InstallTransaction) ensureAction(ctx context.Context, action JournalAction) error {
	if x.hasDoneKind(action.Kind) {
		actual, err := x.observe(ctx, action)
		if err != nil {
			return err
		}
		if actual == action.NewState {
			return nil
		}
	}
	actual, err := x.observe(ctx, action)
	if err != nil {
		return err
	}
	if actual == action.NewState {
		return nil
	}
	if actual != action.OldState {
		return unknownInstallState()
	}
	return x.step(ctx, action, func(ctx context.Context) error {
		return x.applyEffect(ctx, action)
	})
}

func (x *InstallTransaction) ensureReload(ctx context.Context) error {
	afterActivation := false
	for _, action := range x.journal.Actions {
		if action.Kind == JournalActionActivation && action.Status == JournalActionDone {
			afterActivation = true
			continue
		}
		if afterActivation && (action.Kind == JournalActionReload || action.Kind == JournalActionRestoreReload) && action.Status == JournalActionDone {
			return nil
		}
	}
	return x.reloadStep(ctx)
}

func (x *InstallTransaction) alreadyReversed(forward JournalAction) bool {
	want := reverseKind(forward.Kind)
	if want == "" {
		return false
	}
	for _, action := range x.journal.Actions {
		if action.Seq > forward.Seq && action.Kind == want && action.TargetRole == forward.TargetRole && action.BackupRef == forward.BackupRef && action.CandidateRef == forward.CandidateRef {
			return true
		}
	}
	return false
}

func classifyInstallAction(action JournalAction, actual string) (ActionProgress, error) {
	if action.Kind == JournalActionReload || action.Kind == JournalActionRestoreReload {
		if actual == action.OldState {
			return ActionNotStarted, nil
		}
		return ActionStarted, nil
	}
	return ClassifyObservedAction(action, actual)
}

func (x *InstallTransaction) observe(ctx context.Context, action JournalAction) (string, error) {
	if x.serviceEffects != nil && isConcreteServiceAction(action) {
		return x.serviceEffects.observe(ctx, action)
	}
	if x.Effects == nil {
		return "", unknownInstallState()
	}
	return x.Effects.Observe(ctx, action)
}

func (x *InstallTransaction) applyEffect(ctx context.Context, action JournalAction) error {
	if x.serviceEffects != nil && isConcreteServiceAction(action) {
		return x.serviceEffects.apply(ctx, action)
	}
	if x.Effects == nil {
		return nil
	}
	return x.Effects.Apply(ctx, action)
}

func (x *InstallTransaction) actionBySeq(seq int) (JournalAction, bool) {
	for _, action := range x.journal.Actions {
		if action.Seq == seq {
			return action, true
		}
	}
	return JournalAction{}, false
}

func (x *InstallTransaction) hasDoneKind(kind string) bool {
	for i := len(x.journal.Actions) - 1; i >= 0; i-- {
		action := x.journal.Actions[i]
		if action.Kind == kind {
			return action.Status == JournalActionDone
		}
	}
	return false
}

func reverseKind(kind string) string {
	switch kind {
	case JournalActionMask:
		return JournalActionRestoreMask
	case JournalActionUnmask:
		return JournalActionRestoreUnmask
	case JournalActionDisable:
		return JournalActionRestoreDisable
	case JournalActionEnable:
		return JournalActionRestoreEnable
	case JournalActionStop:
		return JournalActionRestoreStop
	case JournalActionStart:
		return JournalActionRestoreStart
	case JournalActionReload:
		return JournalActionRestoreReload
	case JournalActionDataPublish:
		return JournalActionDataIsolate
	case JournalActionManagedBinary:
		return JournalActionRestoreManagedBinary
	case JournalActionPathBinary:
		return JournalActionRestorePathBinary
	case JournalActionChannel:
		return JournalActionRestoreChannel
	case JournalActionDefinition:
		return JournalActionRestoreDefinition
	case JournalActionDropin:
		return JournalActionRestoreDropin
	case JournalActionDisabled:
		return JournalActionRestoreDisabled
	case JournalActionValidationStart:
		return JournalActionRestoreValidation
	default:
		return ""
	}
}

func isReverseAction(kind string) bool {
	switch kind {
	case JournalActionRestoreMask, JournalActionRestoreUnmask, JournalActionRestoreDisable, JournalActionRestoreEnable,
		JournalActionRestoreStop, JournalActionRestoreStart, JournalActionRestoreData, JournalActionDataIsolate,
		JournalActionRestoreManagedBinary, JournalActionRestorePathBinary, JournalActionRestoreChannel,
		JournalActionRestoreDefinition, JournalActionRestoreDropin, JournalActionRestoreDisabled,
		JournalActionRestoreReload, JournalActionRestoreValidation:
		return true
	default:
		return false
	}
}

func reqKeepsEnabled(journal InstallJournal) bool {
	return journal.OldEnabled
}
