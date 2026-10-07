package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// DeferCleanup records a terminal transaction for a later daemon startup.
// It does not remove the old binary, candidate, restoration copy or marker.
func (u *UpdateTransaction) DeferCleanup(ctx context.Context) error {
	if u == nil || u.store == nil {
		return dataFailure("core update is unavailable")
	}
	release, err := u.store.coreStore().execution().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return u.deferCleanupLocked(ctx)
}

// deferCleanupLocked releases the active journal only after preserving durable
// cleanup authority. The caller holds the store's execution gate.
func (u *UpdateTransaction) deferCleanupLocked(ctx context.Context) error {
	if u.uncertain || (u.Phase() != "committed" && u.Phase() != "rolled_back") {
		return dataFailure("core recovery required before update")
	}
	if err := u.verifyFinalCore(ctx, u.Phase() == "committed"); err != nil {
		return err
	}
	if err := u.verifyMarker(ctx); err != nil {
		return err
	}
	raw, err := json.Marshal(u.journal)
	if err != nil {
		return err
	}
	current, err := u.store.Inspect(ctx, UpdateJournal, "")
	if err != nil {
		return err
	}
	if !current.Present || current.SHA256 != digest(raw) {
		return dataFailure("completed core journal changed")
	}
	archived, err := u.store.Load(ctx, UpdateCleanup, u.journal.Transaction)
	if errors.Is(err, os.ErrNotExist) {
		if err := u.store.Save(ctx, UpdateCleanup, u.journal.Transaction, raw); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if digest(archived) != digest(raw) {
		return dataFailure("core pending cleanup record changed")
	}
	// Also covers a retry after Save published its record but failed to sync.
	syncer, ok := u.store.coreStore().(interface {
		syncCleanupRecord(context.Context, string) error
	})
	if !ok {
		return dataFailure("core cleanup durability confirmation unavailable")
	}
	if err := syncer.syncCleanupRecord(ctx, u.journal.Transaction); err != nil {
		return err
	}
	return u.store.Apply(ctx, ProvenanceMutation{Role: UpdateJournal, Expected: current})
}

// CleanupCompletedUpdates retries completed transactions at daemon startup.
func CleanupCompletedUpdates(ctx context.Context, store ProvenanceStore) (resultErr error) {
	if store == nil {
		return nil
	}
	release, err := store.coreStore().execution().acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if _, err := store.Load(ctx, PairJournal, ""); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(dataFailure("legacy core recovery journal retained during cleanup"), err)
	}
	active := ""
	current, err := OpenUpdate(ctx, store)
	if err == nil {
		active = current.journal.Transaction
		if current.Phase() == "committed" || current.Phase() == "rolled_back" {
			if err := current.deferCleanupLocked(ctx); err != nil {
				resultErr = fmt.Errorf("defer completed core transaction %s: %w", active, err)
			} else {
				active = ""
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lister, ok := store.coreStore().(interface {
		cleanupTransactions(context.Context) ([]string, error)
	})
	if !ok {
		return errors.Join(resultErr, dataFailure("core cleanup enumeration unavailable"))
	}
	transactions, err := lister.cleanupTransactions(ctx)
	if err != nil {
		return errors.Join(resultErr, err)
	}
	for _, tx := range transactions {
		if err := ctx.Err(); err != nil {
			return errors.Join(resultErr, err)
		}
		if !validTransaction(tx) || tx == active {
			continue
		}
		if err := cleanupArchivedUpdate(ctx, store, tx); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("cleanup core transaction %s: %w", tx, err))
		}
	}
	return resultErr
}

func cleanupArchivedUpdate(ctx context.Context, store ProvenanceStore, tx string) error {
	record, err := store.Inspect(ctx, UpdateCleanup, tx)
	if err != nil {
		return err
	}
	if !record.Present {
		return removeEmptyCleanupDirectory(ctx, store, tx)
	}
	raw, err := store.Load(ctx, UpdateCleanup, tx)
	if err != nil {
		return err
	}
	if digest(raw) != record.SHA256 {
		return dataFailure("core cleanup record changed")
	}
	j, err := decodeUpdateJournal(raw)
	if err != nil {
		return err
	}
	if j.Transaction != tx || (j.Phase != "committed" && j.Phase != "rolled_back") {
		return dataFailure("core cleanup record is not a completed transaction")
	}
	marker, err := store.Inspect(ctx, UpdateMarker, tx)
	if err != nil {
		return err
	}
	if marker.Present && !sameObject(marker, j.Marker) {
		return dataFailure("core cleanup marker changed")
	}
	// Preflight all objects before deleting any. Missing objects are allowed only
	// as consumed publication inputs or completed steps of an earlier cleanup.
	var mutations []ProvenanceMutation
	for _, item := range []struct {
		role     ProvenanceRole
		expected ProvenanceObject
	}{
		{UpdateCandidate, j.New}, {UpdateBackup, j.Backup}, {UpdateRestore, j.Restore},
	} {
		object, err := store.Inspect(ctx, item.role, tx)
		if err != nil {
			return err
		}
		if !object.Present {
			continue
		}
		if !marker.Present || !sameObject(object, item.expected) {
			return dataFailure("core cleanup object changed")
		}
		mutations = append(mutations, ProvenanceMutation{Role: item.role, Transaction: tx, Expected: object})
	}
	// Recheck the authority after reading all objects. Store Apply binds each
	// removal to the observed identity, including the final authority record.
	current, err := store.Inspect(ctx, UpdateCleanup, tx)
	if err != nil {
		return err
	}
	if !sameObject(current, record) {
		return dataFailure("core cleanup record changed")
	}
	if marker.Present {
		mutations = append(mutations, ProvenanceMutation{Role: UpdateMarker, Transaction: tx, Expected: marker})
	}
	mutations = append(mutations, ProvenanceMutation{Role: UpdateCleanup, Transaction: tx, Expected: record})
	for _, mutation := range mutations {
		if err := store.Apply(ctx, mutation); err != nil {
			return fmt.Errorf("remove %s: %w", mutation.Role, err)
		}
	}
	return removeEmptyCleanupDirectory(ctx, store, tx)
}

func removeEmptyCleanupDirectory(ctx context.Context, store ProvenanceStore, tx string) error {
	cleaner, ok := store.coreStore().(interface {
		removeEmptyCleanupDirectory(context.Context, string) error
	})
	if !ok {
		return dataFailure("core cleanup directory removal unavailable")
	}
	return cleaner.removeEmptyCleanupDirectory(ctx, tx)
}
