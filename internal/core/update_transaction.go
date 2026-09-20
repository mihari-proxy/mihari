package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const (
	UpdateJournal     ProvenanceRole = "update_journal"
	UpdateCandidate   ProvenanceRole = "update_candidate"
	UpdateBackup      ProvenanceRole = "update_backup"
	UpdateRestore     ProvenanceRole = "update_restore"
	UpdateMarker      ProvenanceRole = "update_marker"
	UpdateInterrupted ProvenanceRole = "update_interrupted"
)

// CoreSelection is the core-owned portion of settings and observed identity.
// Bundle is the consumed installer sidecar stamp, not an online release tag.
type CoreSelection struct {
	Channel  string `json:"channel"`
	Bundle   string `json:"bundle,omitempty"`
	Version  string `json:"version,omitempty"`
	AlphaSHA string `json:"alpha_sha,omitempty"`
}

// UpdateIntent captures the settings and running intent reserved by Manager.
type UpdateIntent struct {
	Previous   CoreSelection `json:"previous"`
	Next       CoreSelection `json:"next"`
	WasRunning bool          `json:"was_running"`
	StartNew   bool          `json:"start_new"`
	Reinstall  bool          `json:"reinstall,omitempty"`
}

// UpdateTransaction keeps recovery objects until the whole update is accepted.
type UpdateTransaction struct {
	store     ProvenanceStore
	journal   updateJournal
	warnings  []error
	uncertain bool
}

type updateJournal struct {
	Schema      string           `json:"schema"`
	Transaction string           `json:"transaction"`
	Phase       string           `json:"phase"`
	Intent      UpdateIntent     `json:"intent"`
	Old         ProvenanceObject `json:"old"`
	New         ProvenanceObject `json:"new"`
	Backup      ProvenanceObject `json:"backup"`
	Restore     ProvenanceObject `json:"restore"`
	Marker      ProvenanceObject `json:"marker"`
}

const updateSchema = "mihari.core-update/v1"

type updateExecutionKey struct{}
type updateExecution struct {
	store       ProvenanceStore
	transaction string
	newCore     bool
}

// ExecutionContext authorizes only this transaction's internal lifecycle work.
func (u *UpdateTransaction) ExecutionContext(ctx context.Context) context.Context {
	newCore := u.journal.Phase == "verifying" || u.journal.Phase == "committing" || u.journal.Phase == "committed"
	return context.WithValue(ctx, updateExecutionKey{}, updateExecution{store: u.store, transaction: u.journal.Transaction, newCore: newCore})
}

func authorizePendingUpdate(ctx context.Context, store ProvenanceStore) error {
	update, err := OpenUpdate(ctx, store)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if update.journal.Phase == "committed" || update.journal.Phase == "rolled_back" {
		// Cleanup may be deferred after a durable decision. Execution still
		// verifies that exact final object; it never admits a replacement.
		return update.verifyFinalCore(ctx, update.journal.Phase == "committed")
	}
	authority, ok := ctx.Value(updateExecutionKey{}).(updateExecution)
	if !ok || authority.store != store || authority.transaction != update.journal.Transaction {
		return dataFailure("core update recovery or transaction ownership required")
	}
	phase := update.journal.Phase
	if authority.newCore && (phase == "verifying" || phase == "committing" || phase == "committed") {
		return nil
	}
	if !authority.newCore && (phase == "rolling_back" || phase == "rolled_back") {
		return nil
	}
	return dataFailure("core update phase does not allow this execution")
}

// OpenUpdate reads a pending transaction without running a core or changing it.
func OpenUpdate(ctx context.Context, store ProvenanceStore) (*UpdateTransaction, error) {
	if store == nil {
		return nil, dataFailure("core update store unavailable")
	}
	raw, err := store.Load(ctx, UpdateJournal, "")
	if err != nil {
		return nil, err
	}
	if _, err := store.Load(ctx, PairJournal, ""); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(dataFailure("legacy and current core recovery journals coexist"), err)
	}
	var journal updateJournal
	if err := decodeStrict(raw, &journal); err != nil {
		return nil, err
	}
	if journal.Schema != updateSchema || !validTransaction(journal.Transaction) || !validSelection(journal.Intent.Previous) || !validSelection(journal.Intent.Next) {
		return nil, dataFailure("invalid core update journal")
	}
	switch journal.Phase {
	case "prepared", "publishing", "verifying", "committing", "committed", "rolling_back", "rolled_back", "recovery_required":
	default:
		return nil, dataFailure("invalid core update phase")
	}
	for _, object := range []ProvenanceObject{journal.Old, journal.New, journal.Backup, journal.Restore, journal.Marker} {
		if !validProvenanceObject(object) {
			return nil, dataFailure("invalid core update file observation")
		}
	}
	if !journal.New.Present || !journal.Marker.Present || journal.Marker.SHA256 != digest([]byte(journal.Transaction)) || journal.Old.Present != journal.Backup.Present || journal.Old.Present != journal.Restore.Present {
		return nil, dataFailure("incomplete core update journal")
	}
	if journal.Old.Present && (journal.Old.SHA256 != journal.Backup.SHA256 || journal.Old.SHA256 != journal.Restore.SHA256) {
		return nil, dataFailure("core update recovery hashes disagree")
	}
	u := &UpdateTransaction{store: store, journal: journal}
	if err := u.verifyMarker(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

// BeginUpdate snapshots a prepared fixed-role candidate and the old local core.
// The caller must reserve the mutation and keep the execution lifecycle stopped
// before Publish; taking the snapshot itself does not stop or replace a core.
func BeginUpdate(ctx context.Context, store ProvenanceStore, transaction string, expected ProvenanceObject, intent UpdateIntent) (*UpdateTransaction, error) {
	return beginUpdate(ctx, store, transaction, expected, intent, false)
}

func beginUpdate(ctx context.Context, store ProvenanceStore, transaction string, expected ProvenanceObject, intent UpdateIntent, reinstall bool) (*UpdateTransaction, error) {
	if store == nil || !validTransaction(transaction) || !validSelection(intent.Previous) || !validSelection(intent.Next) {
		return nil, dataFailure("invalid core update intent")
	}
	release, err := store.coreStore().execution().acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if _, err := store.Load(ctx, PairJournal, ""); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(dataFailure("legacy core recovery required before update"), err)
	}
	previous, pendingErr := store.Load(ctx, UpdateJournal, "")
	pending := pendingErr == nil
	if pendingErr != nil && !errors.Is(pendingErr, os.ErrNotExist) {
		return nil, pendingErr
	}
	if pending && !reinstall {
		return nil, dataFailure("core recovery required before update")
	}
	if reinstall {
		if pending {
			interrupted, err := OpenUpdate(ctx, store)
			if err != nil {
				return nil, err
			}
			selection, start := interrupted.ReinstallSelection()
			if intent.Next.Channel != selection.Channel || intent.Previous != selection {
				return nil, dataFailure("reinstall must use the original core selection")
			}
			intent.WasRunning, intent.StartNew = interrupted.Intent().WasRunning, start
			// Retain the exact previous record beside its backups before superseding
			// the active marker. A retry accepts only the same archived bytes.
			tx := interrupted.journal.Transaction
			archived, err := store.Load(ctx, UpdateInterrupted, tx)
			if errors.Is(err, os.ErrNotExist) {
				err = store.Save(ctx, UpdateInterrupted, tx, previous)
			} else if err == nil && digest(archived) != digest(previous) {
				err = dataFailure("interrupted update record changed")
			}
			if err != nil {
				return nil, err
			}
		}
		if intent.Next.Channel != intent.Previous.Channel {
			return nil, dataFailure("reinstall cannot switch the core channel")
		}
	}
	intent.Reinstall = reinstall
	u := &UpdateTransaction{store: store, journal: updateJournal{Schema: updateSchema, Transaction: transaction, Intent: intent}}
	j := &u.journal
	if j.New, err = store.Inspect(ctx, UpdateCandidate, transaction); err != nil {
		return nil, err
	}
	if j.Marker, err = store.Inspect(ctx, UpdateMarker, transaction); err != nil {
		return nil, err
	}
	if !j.New.Present || !sameObject(j.New, expected) || !j.Marker.Present || j.Marker.SHA256 != digest([]byte(transaction)) {
		return nil, dataFailure("core update candidate or marker missing")
	}
	if j.Old, err = store.Inspect(ctx, InstalledBinary, ""); err != nil {
		return nil, err
	}
	if err := store.coreStore().execution().admitInstalled(j.Old); err != nil && !reinstall {
		return nil, err
	}
	// A first installation can carry daemon startup intent without an old
	// process. Updating an existing stopped core must still leave it stopped.
	if !reinstall || !pending {
		j.Intent.StartNew = intent.WasRunning || (!j.Old.Present && intent.StartNew)
	}
	if j.Old.Present {
		content, err := store.Load(ctx, InstalledBinary, "")
		if err != nil {
			return nil, err
		}
		current, err := store.Inspect(ctx, InstalledBinary, "")
		if err != nil {
			return nil, err
		}
		if !sameObject(j.Old, current) || digest(content) != j.Old.SHA256 {
			return nil, dataFailure("installed core changed while preparing backup")
		}
		for _, role := range []ProvenanceRole{UpdateBackup, UpdateRestore} {
			if err := store.Save(ctx, role, transaction, content); err != nil {
				return nil, err
			}
		}
		if j.Backup, err = store.Inspect(ctx, UpdateBackup, transaction); err != nil {
			return nil, err
		}
		if j.Restore, err = store.Inspect(ctx, UpdateRestore, transaction); err != nil {
			return nil, err
		}
		if j.Backup.SHA256 != j.Old.SHA256 || j.Restore.SHA256 != j.Old.SHA256 {
			return nil, dataFailure("core update backup changed")
		}
	}
	if err := u.writePhase(ctx, "prepared"); err != nil {
		return nil, err
	}
	return u, nil
}

// Publish installs the prepared binary while retaining recovery material.
func (u *UpdateTransaction) Publish(ctx context.Context) error {
	if u == nil || u.journal.Phase != "prepared" {
		return dataFailure("core update is not prepared")
	}
	release, err := u.store.coreStore().execution().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := u.verifyMarker(ctx); err != nil {
		return err
	}
	if err := u.writePhase(ctx, "publishing"); err != nil {
		return err
	}
	j := u.journal
	if err := u.move(ctx, InstalledBinary, UpdateCandidate, j.Old, j.New); err != nil {
		return err
	}
	u.store.coreStore().execution().acceptPublished(j.New)
	return u.writePhase(ctx, "verifying")
}

// Rollback restores old bytes and calls the owner to restore only core settings.
func (u *UpdateTransaction) Rollback(ctx context.Context, restore func(CoreSelection) error) error {
	if u == nil || u.store == nil || restore == nil || u.journal.Phase == "committed" || u.journal.Intent.Reinstall {
		return dataFailure("core update cannot be rolled back")
	}
	if u.uncertain {
		return coreUpdateUncertain(nil)
	}
	release, err := u.store.coreStore().execution().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := u.verifyMarker(ctx); err != nil {
		return u.recoveryFailure(ctx, err)
	}
	if err := u.writePhase(ctx, "rolling_back"); err != nil {
		return u.recoveryFailure(ctx, err)
	}
	j := u.journal
	current, err := u.store.Inspect(ctx, InstalledBinary, "")
	if err != nil {
		return u.recoveryFailure(ctx, err)
	}
	switch {
	case sameObject(current, j.Old), j.Old.Present && sameObject(current, j.Restore):
		// Unpublished, or a prior recovery already restored the held backup.
	case sameObject(current, j.New):
		source := UpdateRestore
		if !j.Old.Present {
			source = ""
		}
		if err := u.move(ctx, InstalledBinary, source, current, j.Restore); err != nil {
			return u.recoveryFailure(ctx, err)
		}
	default:
		return u.recoveryFailure(ctx, dataFailure("unknown core identity prevents rollback"))
	}
	if err := restore(j.Intent.Previous); err != nil {
		return u.recoveryFailure(ctx, err)
	}
	current, err = u.store.Inspect(ctx, InstalledBinary, "")
	if err != nil {
		return u.recoveryFailure(ctx, err)
	}
	u.store.coreStore().execution().acceptPublished(current)
	return nil
}

// CompleteRollback records that the owner has also restored running intent.
// Rollback alone restores files and settings, not the health of the old process.
func (u *UpdateTransaction) CompleteRollback(ctx context.Context) error {
	if u == nil || u.journal.Phase != "rolling_back" || u.uncertain {
		return dataFailure("core rollback has not been verified")
	}
	release, err := u.store.coreStore().execution().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := u.verifyMarker(ctx); err != nil {
		return u.recoveryFailure(ctx, err)
	}
	if err := u.verifyFinalCore(ctx, false); err != nil {
		return u.recoveryFailure(ctx, err)
	}
	return u.writePhase(ctx, "rolled_back")
}

// Intent returns the original and target selections recorded before publication.
func (u *UpdateTransaction) Intent() UpdateIntent { return u.journal.Intent }

// Phase reports the internal durable phase for local recovery orchestration.
func (u *UpdateTransaction) Phase() string { return u.journal.Phase }

// HasPreviousCore distinguishes first installation from a stopped existing core.
func (u *UpdateTransaction) HasPreviousCore() bool { return u.journal.Old.Present }

// Warnings returns successful operations whose final sync or cleanup failed.
func (u *UpdateTransaction) Warnings() []error { return append([]error(nil), u.warnings...) }

// RequireRecovery retains recovery material and blocks ordinary core execution.
func (u *UpdateTransaction) RequireRecovery(ctx context.Context, cause error) error {
	return u.recoveryFailure(ctx, cause)
}

func (u *UpdateTransaction) verifyFinalCore(ctx context.Context, committed bool) error {
	current, err := u.store.Inspect(ctx, InstalledBinary, "")
	if err != nil {
		return err
	}
	j := u.journal
	if committed && sameObject(current, j.New) || !committed && (sameObject(current, j.Old) || j.Old.Present && sameObject(current, j.Restore)) {
		return nil
	}
	return dataFailure("core identity disagrees with completed update")
}

// Finish retires only an acknowledged commit or health-confirmed rollback.
// Each removal is identity-bound and can be repeated after a crash.
func (u *UpdateTransaction) Finish(ctx context.Context) error {
	if u == nil || u.uncertain || (u.journal.Phase != "committed" && u.journal.Phase != "rolled_back") {
		return dataFailure("core update has not completed")
	}
	release, err := u.store.coreStore().execution().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := u.verifyFinalCore(ctx, u.journal.Phase == "committed"); err != nil {
		return err
	}
	journal, err := u.store.Inspect(ctx, UpdateJournal, "")
	if err != nil {
		return err
	}
	if journal.Present {
		raw, err := json.Marshal(u.journal)
		if err != nil {
			return err
		}
		if journal.SHA256 != digest(raw) {
			return dataFailure("core cleanup journal changed")
		}
		if err := u.verifyMarker(ctx); err != nil {
			return err
		}
		for _, item := range []struct {
			role     ProvenanceRole
			expected ProvenanceObject
		}{
			{UpdateCandidate, u.journal.New}, {UpdateBackup, u.journal.Backup}, {UpdateRestore, u.journal.Restore},
		} {
			current, err := u.store.Inspect(ctx, item.role, u.journal.Transaction)
			if err != nil {
				return err
			}
			if !current.Present {
				continue
			} // Already consumed or removed by a previous cleanup.
			if !sameObject(current, item.expected) {
				return dataFailure("core cleanup object changed")
			}
			if err := u.move(ctx, item.role, "", current, ProvenanceObject{}); err != nil {
				return err
			}
		}
		if err := u.move(ctx, UpdateJournal, "", journal, ProvenanceObject{}); err != nil {
			return err
		}
	}
	marker, err := u.store.Inspect(ctx, UpdateMarker, u.journal.Transaction)
	if err != nil || !marker.Present {
		return err
	}
	if !sameObject(marker, u.journal.Marker) {
		return dataFailure("core cleanup marker changed")
	}
	return u.move(ctx, UpdateMarker, "", marker, ProvenanceObject{})
}

// Commit records intent before saving settings and then the logical commit point.
func (u *UpdateTransaction) Commit(ctx context.Context, save func(CoreSelection) error) error {
	if u == nil || save == nil || u.journal.Phase != "verifying" {
		return dataFailure("core update has not been verified")
	}
	if err := u.verifyMarker(ctx); err != nil {
		return err
	}
	current, err := u.store.Inspect(ctx, InstalledBinary, "")
	if err != nil {
		return err
	}
	if !sameObject(current, u.journal.New) {
		return dataFailure("published core changed before commit")
	}
	if err := u.writePhase(ctx, "committing"); err != nil {
		return err
	}
	if err := save(u.journal.Intent.Next); err != nil {
		return err
	}
	return u.writePhase(ctx, "committed")
}

func validSelection(selection CoreSelection) bool {
	return selection.Channel == "stable" || selection.Channel == "alpha"
}

func (u *UpdateTransaction) verifyMarker(ctx context.Context) error {
	marker, err := u.store.Inspect(ctx, UpdateMarker, u.journal.Transaction)
	if err != nil {
		return err
	}
	if !sameObject(marker, u.journal.Marker) {
		return dataFailure("core update transaction identity changed")
	}
	return nil
}

func (u *UpdateTransaction) writePhase(ctx context.Context, phase string) error {
	if u.uncertain {
		return coreUpdateUncertain(nil)
	}
	next := u.journal
	next.Phase = phase
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = u.store.Save(ctx, UpdateJournal, "", raw); err != nil {
		actual, readErr := u.store.Load(context.WithoutCancel(ctx), UpdateJournal, "")
		if readErr != nil || digest(actual) != digest(raw) {
			var previous updateJournal
			if (u.journal.Phase == "" && errors.Is(readErr, os.ErrNotExist)) || (readErr == nil && decodeStrict(actual, &previous) == nil && previous == u.journal) {
				return err
			}
			u.uncertain = true
			return coreUpdateUncertain(errors.Join(err, readErr))
		}
		// Save may fail after atomic replacement. Read-back proves this phase
		// committed; a later sync/cleanup failure is a successful warning.
		u.warnings = append(u.warnings, err)
	}
	u.journal = next
	return nil
}

func coreUpdateUncertain(cause error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "core update commit state could not be confirmed; recovery required", Details: map[string]any{"degraded": true}}, cause)
}

func (u *UpdateTransaction) move(ctx context.Context, role, source ProvenanceRole, expected, sourceExpected ProvenanceObject) error {
	transaction := u.journal.Transaction
	if role == UpdateJournal {
		transaction = ""
	}
	err := u.store.Apply(ctx, ProvenanceMutation{Role: role, Source: source, Transaction: transaction, Expected: expected, SourceExpected: sourceExpected})
	if err == nil {
		return nil
	}
	actual, readErr := u.store.Inspect(context.WithoutCancel(ctx), role, transaction)
	wanted := sourceExpected
	if source == "" {
		wanted = ProvenanceObject{}
	}
	if readErr != nil || !sameObject(actual, wanted) {
		return errors.Join(err, readErr)
	}
	u.warnings = append(u.warnings, err)
	return nil
}

func (u *UpdateTransaction) recoveryFailure(ctx context.Context, cause error) error {
	if u.uncertain || u.journal.Phase == "committed" {
		// Preserve the irreversible decision, including an unreadable possible
		// committed marker. A diagnostic must not make reversal legal again.
		return coreUpdateUncertain(cause)
	}
	markerErr := u.writePhase(context.WithoutCancel(ctx), "recovery_required")
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "core update recovery failed; automatic execution prohibited", Details: map[string]any{"degraded": true}}, errors.Join(cause, markerErr))
}

// ReinstallSelection returns the accepted channel and saved running intent.
// A completed commit is already accepted even when cleanup was interrupted.
func (u *UpdateTransaction) ReinstallSelection() (CoreSelection, bool) {
	if u.journal.Phase == "committed" {
		return u.journal.Intent.Next, u.journal.Intent.StartNew
	}
	return u.journal.Intent.Previous, u.journal.Intent.StartNew
}

// InterruptedUpdate detects unfinished updates without attempting recovery.
func InterruptedUpdate(ctx context.Context, store ProvenanceStore) (bool, error) {
	pending, err := OpenUpdate(ctx, store)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if pending.Phase() == "committed" || pending.Phase() == "rolled_back" {
		// A valid completed record still identifies the accepted channel when
		// its core has since changed. Retain the repair owner without admitting
		// that file; candidate preparation and replacement enforce their checks.
		if err := authorizePendingUpdate(ctx, store); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return true, nil
		}
		return false, nil
	}
	return true, nil
}
