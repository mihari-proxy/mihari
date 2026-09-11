package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// ProvenanceRole identifies a fixed object within the private data root.
type ProvenanceRole string

const (
	InstalledBinary   ProvenanceRole = "binary"
	InstalledReceipt  ProvenanceRole = "receipt"
	CandidateBinary   ProvenanceRole = "candidate_binary"
	CandidateReceipt  ProvenanceRole = "candidate_receipt"
	BackupBinary      ProvenanceRole = "backup_binary"
	BackupReceipt     ProvenanceRole = "backup_receipt"
	RestoreBinary     ProvenanceRole = "restore_binary"
	RestoreReceipt    ProvenanceRole = "restore_receipt"
	QuarantineBinary  ProvenanceRole = "quarantine_binary"
	QuarantineReceipt ProvenanceRole = "quarantine_receipt"
	PairJournal       ProvenanceRole = "journal"
	TransactionMarker ProvenanceRole = "transaction_marker"
)

// ProvenanceObject is an observation, not authority to bypass store checks.
type ProvenanceObject struct {
	Present  bool   `json:"present"`
	SHA256   string `json:"sha256,omitempty"`
	Identity string `json:"identity,omitempty"`
	BootID   string `json:"boot_id,omitempty"`
}

// ProvenanceMutation replaces/removes one observed fixed-role object.
type ProvenanceMutation struct {
	Role, Source             ProvenanceRole
	Transaction              string
	Expected, SourceExpected ProvenanceObject
}

// ProvenanceStore is implemented by the core-owned Unix capability adapter.
// Its private method prevents callers from supplying an untrusted path/hash store.
type ProvenanceStore interface {
	Load(context.Context, ProvenanceRole, string) ([]byte, error)
	Save(context.Context, ProvenanceRole, string, []byte) error
	Apply(context.Context, ProvenanceMutation) error
	Inspect(context.Context, ProvenanceRole, string) (ProvenanceObject, error)
	Sync(context.Context) error
	coreStore() storeBackend
}
type storeBackend interface {
	location() string
	execution() *executionGate
	target() (string, string)
	open(context.Context, ProvenanceRole, string) (verifiedFile, error)
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

const pairSchema = "mihari.core-provenance-commit/v1"

type pairMember struct {
	Role           ProvenanceRole   `json:"role"`
	Old            ProvenanceObject `json:"old"`
	New            ProvenanceObject `json:"new"`
	Backup         ProvenanceObject `json:"backup"`
	Restore        ProvenanceObject `json:"restore"`
	RestoreRef     string           `json:"restore_ref"`
	CandidateRef   string           `json:"candidate_ref"`
	BackupRef      string           `json:"backup_ref"`
	Intent         bool             `json:"intent"`
	Done           bool             `json:"done"`
	RecoveryIntent bool             `json:"recovery_intent"`
	RecoveryDone   bool             `json:"recovery_done"`
}
type pairCommit struct {
	Schema      string           `json:"schema"`
	Transaction string           `json:"tx_id"`
	Phase       string           `json:"phase"`
	OldPresent  bool             `json:"old_present"`
	Marker      ProvenanceObject `json:"transaction_identity"`
	Members     [2]pairMember    `json:"members"`
}

func validTransaction(tx string) bool {
	b, e := hex.DecodeString(tx)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == tx
}
func candidateRole(r ProvenanceRole) ProvenanceRole {
	if r == InstalledBinary {
		return CandidateBinary
	}
	return CandidateReceipt
}
func backupRole(r ProvenanceRole) ProvenanceRole {
	if r == InstalledBinary {
		return BackupBinary
	}
	return BackupReceipt
}
func restoreRole(r ProvenanceRole) ProvenanceRole {
	if r == InstalledBinary {
		return RestoreBinary
	}
	return RestoreReceipt
}
func quarantineRole(r ProvenanceRole) ProvenanceRole {
	if r == InstalledBinary {
		return QuarantineBinary
	}
	return QuarantineReceipt
}
func savePair(ctx context.Context, s ProvenanceStore, j pairCommit) error {
	b, e := json.Marshal(j)
	if e != nil {
		return e
	}
	return s.Save(ctx, PairJournal, "", b)
}
func decodeStrict(b []byte, v any) error {
	if len(b) > 1<<20 {
		return dataFailure("provenance document too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if e := uniqueJSON(d); e != nil {
		return dataFailure("invalid provenance JSON")
	}
	if _, e := d.Token(); e != io.EOF {
		return dataFailure("invalid provenance JSON tail")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return dataFailure("invalid provenance document")
	}
	return nil
}
func uniqueJSON(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			name, ok := k.(string)
			if !ok || seen[name] {
				return os.ErrInvalid
			}
			seen[name] = true
			if e = uniqueJSON(d); e != nil {
				return e
			}
		}
		_, e = d.Token()
		return e
	case json.Delim('['):
		for d.More() {
			if e = uniqueJSON(d); e != nil {
				return e
			}
		}
		_, e = d.Token()
		return e
	}
	return nil
}
func loadPair(ctx context.Context, s ProvenanceStore) (pairCommit, error) {
	var j pairCommit
	b, e := s.Load(ctx, PairJournal, "")
	if e != nil {
		return j, e
	}
	if e = decodeStrict(b, &j); e != nil {
		return j, e
	}
	if !validProvenanceObject(j.Marker) || j.Schema != pairSchema || !validTransaction(j.Transaction) || (j.Phase != "prepared" && j.Phase != "publishing" && j.Phase != "committed") || !j.Marker.Present || j.Marker.SHA256 != digest([]byte(j.Transaction)) {
		return j, dataFailure("invalid provenance journal")
	}
	for n, r := range []ProvenanceRole{InstalledBinary, InstalledReceipt} {
		m := j.Members[n]
		for _, o := range []ProvenanceObject{m.Old, m.New, m.Backup, m.Restore} {
			if !validProvenanceObject(o) || o.Present && o.BootID != j.Marker.BootID {
				return j, dataFailure("invalid provenance journal identity")
			}
		}
		if j.Phase == "committed" && (!m.Intent || !m.Done) {
			return j, dataFailure("invalid committed provenance journal")
		}
		if m.Role != r || m.Old.Present != j.OldPresent || !m.New.Present || !validHash(m.New.SHA256) || m.New.Identity == "" || m.CandidateRef != j.Transaction+"/"+string(candidateRole(r)) || m.BackupRef != j.Transaction+"/"+string(backupRole(r)) || m.Done && !m.Intent || m.RecoveryDone && !m.RecoveryIntent || j.OldPresent && (!m.Backup.Present || m.Backup.SHA256 != m.Old.SHA256 || !m.Restore.Present || m.Restore.SHA256 != m.Old.SHA256 || m.RestoreRef != j.Transaction+"/"+string(restoreRole(r))) {
			return j, dataFailure("invalid provenance journal member")
		}
	}
	marker, e := s.Inspect(ctx, TransactionMarker, j.Transaction)
	if e != nil {
		return j, e
	}
	if !sameObject(marker, j.Marker) {
		return j, dataFailure("provenance transaction identity changed")
	}
	return j, nil
}
func validProvenanceObject(o ProvenanceObject) bool {
	if !o.Present {
		return o.SHA256 == "" && o.Identity == "" && o.BootID == ""
	}
	return validHash(o.SHA256) && o.Identity != "" && o.BootID != ""
}
func sameObject(a, b ProvenanceObject) bool {
	if !a.Present || !b.Present {
		return a.Present == b.Present
	}
	if a.SHA256 != b.SHA256 {
		return false
	}
	// Mount/device numbers are not stable across boot. A fresh trusted-root walk,
	// private transaction marker and exact hashes are required by the store then.
	return a.BootID != b.BootID || a.Identity == b.Identity
}
func commitProvenance(ctx context.Context, s ProvenanceStore, tx string) error {
	if s == nil || !validTransaction(tx) {
		return dataFailure("invalid provenance transaction")
	}
	if _, e := s.Load(ctx, PairJournal, ""); !errors.Is(e, os.ErrNotExist) {
		if e != nil {
			return e
		}
		return dataFailure("provenance recovery required")
	}
	j := pairCommit{Schema: pairSchema, Transaction: tx, Phase: "prepared"}
	var e error
	j.Marker, e = s.Inspect(ctx, TransactionMarker, tx)
	if e != nil {
		return e
	}
	if !j.Marker.Present || j.Marker.SHA256 != digest([]byte(tx)) {
		return dataFailure("missing provenance transaction identity")
	}
	for n, r := range []ProvenanceRole{InstalledBinary, InstalledReceipt} {
		m := pairMember{Role: r, CandidateRef: tx + "/" + string(candidateRole(r)), BackupRef: tx + "/" + string(backupRole(r)), RestoreRef: tx + "/" + string(restoreRole(r))}
		m.Old, e = s.Inspect(ctx, r, "")
		if e != nil {
			return e
		}
		m.New, e = s.Inspect(ctx, candidateRole(r), tx)
		if e != nil {
			return e
		}
		if !m.New.Present {
			return dataFailure("missing provenance candidate")
		}
		if n == 0 {
			j.OldPresent = m.Old.Present
		} else if j.OldPresent != m.Old.Present {
			return dataFailure("incomplete installed provenance pair")
		}
		if m.Old.Present {
			b, e := s.Load(ctx, r, "")
			if e != nil {
				return e
			}
			if digest(b) != m.Old.SHA256 {
				return dataFailure("old core changed during backup")
			}
			if e = s.Save(ctx, backupRole(r), tx, b); e != nil {
				return e
			}
			m.Backup, e = s.Inspect(ctx, backupRole(r), tx)
			if e != nil {
				return e
			}
			// Backups remain 0600. A separate durable rollback candidate has
			// the final target mode, avoiding chmod/rename crash ambiguity.
			if e = s.Save(ctx, restoreRole(r), tx, b); e != nil {
				return e
			}
			m.Restore, e = s.Inspect(ctx, restoreRole(r), tx)
			if e != nil {
				return e
			}
		}
		j.Members[n] = m
	}
	if e = s.Sync(ctx); e != nil {
		return e
	}
	if e = savePair(ctx, s, j); e != nil {
		return e
	}
	j.Phase = "publishing"
	for n := range j.Members {
		m := &j.Members[n]
		m.Intent = true
		if e = savePair(ctx, s, j); e != nil {
			return e
		}
		if e = s.Apply(ctx, ProvenanceMutation{Role: m.Role, Source: candidateRole(m.Role), Transaction: tx, Expected: m.Old, SourceExpected: m.New}); e != nil {
			return e
		}
		m.Done = true
		if e = savePair(ctx, s, j); e != nil {
			return e
		}
	}
	for _, m := range j.Members {
		v, e := s.Inspect(ctx, m.Role, "")
		if e != nil {
			return e
		}
		if !sameObject(v, m.New) {
			return dataFailure("published provenance pair changed")
		}
	}
	j.Phase = "committed"
	return savePair(ctx, s, j)
}

// RecoverProvenance converges the entire pair before any core execution.
// The caller must hold the daemon/maintenance lifecycle lease with no core running.
func RecoverProvenance(ctx context.Context, s ProvenanceStore) error {
	if s == nil {
		return dataFailure("provenance store unavailable")
	}
	release, e := s.coreStore().execution().acquire(ctx)
	if e != nil {
		return e
	}
	defer release()
	return recoverProvenance(ctx, s)
}
func recoverProvenance(ctx context.Context, s ProvenanceStore) error {
	if s == nil {
		return dataFailure("provenance store unavailable")
	}
	j, e := loadPair(ctx, s)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	for n := range j.Members {
		m := &j.Members[n]
		current, e := s.Inspect(ctx, m.Role, "")
		if e != nil {
			return e
		}
		desired := m.Old
		if j.Phase == "committed" {
			desired = m.New
		} else if m.RecoveryIntent && m.Old.Present {
			desired = m.Restore
		}
		allowed := sameObject(current, m.Old) || sameObject(current, m.New) || (m.RecoveryIntent && sameObject(current, m.Restore))
		if !allowed {
			return dataFailure("unknown core identity during recovery")
		}
		if j.Phase == "committed" {
			if !sameObject(current, desired) {
				return dataFailure("committed provenance pair changed")
			}
			continue
		}
		if !sameObject(current, desired) {
			m.RecoveryIntent = true
			if e = savePair(ctx, s, j); e != nil {
				return e
			}
			source := ProvenanceRole("")
			if m.Old.Present {
				source = restoreRole(m.Role)
				v, e := s.Inspect(ctx, source, j.Transaction)
				if e != nil {
					return e
				}
				if !sameObject(v, m.Restore) {
					return dataFailure("provenance backup changed")
				}
			}
			action := ProvenanceMutation{Role: m.Role, Source: source, Transaction: j.Transaction, Expected: current, SourceExpected: m.Restore}
			if !m.Old.Present {
				action = ProvenanceMutation{Role: quarantineRole(m.Role), Source: m.Role, Transaction: j.Transaction, SourceExpected: current}
			}
			if e = s.Apply(ctx, action); e != nil {
				return e
			}
		}
		m.RecoveryDone = true
		m.RecoveryIntent = true
		if e = savePair(ctx, s, j); e != nil {
			return e
		}
	}
	// The converged journal is removed last. Its private backup/candidate objects
	// may then be cleaned; a failure above intentionally retains all evidence.
	observed, e := s.Inspect(ctx, PairJournal, "")
	if e != nil {
		return e
	}
	return s.Apply(ctx, ProvenanceMutation{Role: PairJournal, Expected: observed})
}
