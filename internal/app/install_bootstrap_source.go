package app

import (
	"context"
	"strings"
)

// observeBootstrapSource recognizes only the metadata left before the first
// recovery journal or business file was published. A nil observation means the
// ordinary business migration rules must apply. Nothing is removed or copied.
func observeBootstrapSource(ctx context.Context, source migrationCapability) (map[string]sourceObservation, error) {
	top, err := source.List(ctx, ".")
	if err != nil {
		return nil, err
	}
	if len(top) == 0 {
		return nil, nil
	}
	for _, entry := range top {
		switch entry.Name {
		case "install.lock", "mihari-channel", "transactions", "locks":
		default:
			return nil, nil
		}
	}
	obs := map[string]sourceObservation{}
	record := func(rel string, entry migrationEntry) error {
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		if _, exists := obs[rel]; exists {
			return migrateInvalid("duplicate migration source entry")
		}
		if len(obs) >= migrationMaxFiles {
			return migrateData("migration source exceeds size or file limits")
		}
		obs[rel] = rememberObservation(rel, entry.Hash, entry)
		return nil
	}
	read := func(rel string, entry migrationEntry, max int64) ([]byte, error) {
		if err := record(rel, entry); err != nil {
			return nil, err
		}
		if entry.Dir || entry.Kind != "file" || entry.Size > max {
			return nil, migrateState("unsupported migration source")
		}
		return source.ReadFile(ctx, rel, max)
	}
	for _, entry := range top {
		if entry.Name == "locks" {
			if err := record(entry.Name, entry); err != nil {
				return nil, err
			}
			if !entry.Dir {
				return nil, migrateState("unsupported migration source")
			}
			entries, err := source.List(ctx, "locks")
			if err != nil {
				return nil, err
			}
			if len(entries) != 0 {
				return nil, migrateState("unsupported migration source")
			}
			continue
		}
		if entry.Name != "transactions" {
			raw, err := read(entry.Name, entry, 32)
			if err != nil {
				return nil, err
			}
			if entry.Name == "install.lock" {
				if len(raw) != 0 {
					return nil, migrateState("unsupported migration source")
				}
			} else if channel := strings.TrimSuffix(string(raw), "\n"); channel != InstallChannelDev && channel != InstallChannelMain {
				return nil, migrateState("unsupported migration source")
			}
			continue
		}
		if err := record(entry.Name, entry); err != nil {
			return nil, err
		}
		if !entry.Dir {
			return nil, migrateState("unsupported migration source")
		}
		transactions, err := source.List(ctx, "transactions")
		if err != nil {
			return nil, err
		}
		for _, transaction := range transactions {
			rel := "transactions/" + transaction.Name
			if err := record(rel, transaction); err != nil {
				return nil, err
			}
			if !transaction.Dir || !validTransactionID(transaction.Name) {
				return nil, migrateState("unsupported migration source")
			}
			entries, err := source.List(ctx, rel)
			if err != nil {
				return nil, err
			}
			if len(entries) != 1 || entries[0].Name != "transaction-id" {
				return nil, migrateState("unsupported migration source")
			}
			raw, err := read(rel+"/transaction-id", entries[0], 32)
			if err != nil {
				return nil, err
			}
			if string(raw) != transaction.Name {
				return nil, migrateState("unsupported migration source")
			}
		}
	}
	return obs, nil
}

func (p *preparedMigration) verifySource(ctx context.Context) error {
	if !p.bootstrapOnly {
		return verifyStationary(ctx, p.source, p.obs)
	}
	current, err := observeBootstrapSource(ctx, p.source)
	if err != nil || current == nil || len(current) != len(p.obs) {
		return migrateConflict("migration source changed during copy")
	}
	for rel, previous := range p.obs {
		if now, ok := current[rel]; !ok || now != previous {
			return migrateConflict("migration source changed during copy")
		}
	}
	return nil
}
