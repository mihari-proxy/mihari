package core

import "os"

func updateRolePath(role ProvenanceRole, transaction string) (string, uint32, error) {
	if role == UpdateJournal {
		if transaction != "" {
			return "", 0, os.ErrInvalid
		}
		return "staging/core/update-journal.json", 0o600, nil
	}
	if !validTransaction(transaction) {
		return "", 0, os.ErrInvalid
	}
	var name string
	mode := uint32(0o600)
	switch role {
	case UpdateCandidate:
		name, mode = "candidate-binary", 0o700
	case UpdateRestore:
		name, mode = "restore-binary", 0o700
	case UpdateBackup:
		name = "backup-binary"
	case UpdateInterrupted:
		name = "interrupted-update.json"
	case UpdateCleanup:
		name = "pending-cleanup.json"
	case UpdateMarker:
		name = "transaction-id"
	default:
		return "", 0, os.ErrInvalid
	}
	return "staging/core/update/" + transaction + "/" + name, mode, nil
}
