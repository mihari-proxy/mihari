package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
)

const resourceJournalPath = "staging/providers/activation.json"

type resourceEntry struct {
	Transaction    string            `json:"transaction"`
	SubscriptionID string            `json:"subscription_id"`
	Generation     uint64            `json:"generation"`
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Resource       string            `json:"resource"`
	Format         string            `json:"format"`
	Geo            GeoResourceKind   `json:"geo"`
	Configuration  bool              `json:"configuration"`
	StateRole      resourceStateRole `json:"state_role"`
	Old            providerObject    `json:"old"`
	New            providerObject    `json:"new"`
	Marker         providerObject    `json:"marker"`
	BackupIntent   bool              `json:"backup_intent"`
	BackupDone     bool              `json:"backup_done"`
	SwapIntent     bool              `json:"swap_intent"`
	SwapDone       bool              `json:"swap_done"`
	RestoreIntent  bool              `json:"restore_intent"`
	RestoreDone    bool              `json:"restore_done"`
}
type resourceJournal struct {
	Schema  string          `json:"schema"`
	Done    bool            `json:"done"`
	Entries []resourceEntry `json:"entries"`
}

func (e resourceEntry) target() (string, error) {
	if e.StateRole != "" {
		if e.Configuration || e.Geo != "" || e.Generation != 0 || e.Kind != "" || e.Name != "" || e.Resource != "" || e.Format != "" {
			return "", dataError("invalid activation state identity")
		}
		return stateTarget(e.StateRole, e.SubscriptionID)
	}
	if e.Configuration {
		if e.Geo != "" || e.SubscriptionID != "" || e.Generation != 0 || e.Kind != "" || e.Name != "" || e.Resource != "" || e.Format != "" {
			return "", dataError("invalid configuration activation identity")
		}
		return "runtime/config.yaml", nil
	}
	if e.Geo != "" {
		if e.SubscriptionID != "" || e.Generation != 0 || e.Kind != "" || e.Name != "" || e.Resource != "" || e.Format != "" {
			return "", dataError("invalid Geo activation identity")
		}
		name, err := GeoResourcePath(e.Geo)
		return "runtime/core-home/" + name, err
	}
	return providerTarget(legacyProviderIdentity{SubscriptionID: e.SubscriptionID, Generation: e.Generation, Kind: e.Kind, Name: e.Name, ResourceID: e.Resource, Format: e.Format})
}

func (e resourceEntry) candidatePath() string {
	return "staging/providers/" + e.Transaction + "/candidate"
}
func (e resourceEntry) markerPath() string {
	return "staging/providers/" + e.Transaction + "/transaction-id"
}
func (e resourceEntry) backupPath() string { p, _ := e.target(); return p + ".old-" + e.Transaction }

func (s *ProviderStore) saveResourceJournal(ctx context.Context, j resourceJournal) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	old, err := s.files.inspect(ctx, resourceJournalPath)
	if err != nil {
		return err
	}
	return s.files.write(ctx, resourceJournalPath, b, old)
}

func (s *ProviderStore) loadResourceJournal(ctx context.Context) (resourceJournal, error) {
	var j resourceJournal
	b, err := s.files.read(ctx, resourceJournalPath, 1<<20)
	if err != nil {
		return j, err
	}
	if err = resourceJournalShape(b); err != nil {
		return j, dataError("invalid resource journal shape")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if err = uniqueProviderJSON(d, 0); err != nil {
		return j, dataError("invalid resource journal")
	}
	if _, err = d.Token(); err != io.EOF {
		return j, dataError("invalid resource journal")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&j); err != nil {
		return j, dataError("invalid resource journal")
	}
	if j.Schema != "mihari.resource-activation/v1" || len(j.Entries) == 0 || len(j.Entries) > 265 {
		return j, dataError("invalid resource journal")
	}
	paths := map[string]bool{}
	identities := map[string]bool{}
	configurations := 0
	roles := map[resourceStateRole]bool{}
	for _, e := range j.Entries {
		if e.StateRole != "" {
			if roles[e.StateRole] {
				return j, dataError("duplicate activation state role")
			}
			roles[e.StateRole] = true
		}
		if e.Configuration {
			configurations++
		}
		target, e2 := e.target()
		if e2 != nil {
			return j, e2
		}
		if paths[target] || paths[e.Transaction] || !profileIDPattern.MatchString(e.Transaction) {
			return j, dataError("duplicate resource activation identity")
		}
		paths[target] = true
		paths[e.Transaction] = true
		if !e.New.Present || !e.Marker.Present || e.Marker.SHA256 != providerDigest([]byte(e.Transaction)) || e.BackupDone && !e.BackupIntent || e.SwapIntent && !e.BackupDone || e.SwapDone && !e.SwapIntent || e.RestoreDone && !e.RestoreIntent || j.Done && (!e.SwapDone || e.RestoreIntent) {
			return j, dataError("invalid resource activation action")
		}
		for _, o := range []providerObject{e.Old, e.New, e.Marker} {
			if !validProviderObject(o) || o.Present && o.BootID != e.Marker.BootID {
				return j, dataError("invalid resource activation object")
			}
			if o.Present {
				key := o.BootID + "/" + o.Identity
				if identities[key] {
					return j, dataError("aliased resource activation object")
				}
				identities[key] = true
			}
		}
	}
	if configurations != 1 {
		if configurations != 0 {
			return j, dataError("invalid resource activation configuration")
		}
		for _, e := range j.Entries {
			if e.Geo == "" || e.Configuration || e.StateRole != "" {
				return j, dataError("invalid resource activation configuration")
			}
		}
	}
	return j, nil
}

func resourceJournalShape(b []byte) error {
	// Existing v1 journals predate the finite state_role field. Only its
	// omission means the legacy resource-only role; null/unknown fields and
	// all other omissions still fail the exact shape validator.
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		return err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(members["entries"], &entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry == nil {
			return os.ErrInvalid
		}
		if _, exists := entry["state_role"]; !exists {
			entry["state_role"] = json.RawMessage(`""`)
		}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	members["entries"] = encoded
	encoded, err = json.Marshal(members)
	if err != nil {
		return err
	}
	return providerJSONShape(encoded, reflect.TypeOf(resourceJournal{}))
}
func (s *ProviderStore) recoverResources(ctx context.Context) error {
	j, err := s.loadResourceJournal(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := len(j.Entries) - 1; i >= 0; i-- {
		e := &j.Entries[i]
		target, err := e.target()
		if err != nil {
			return err
		}
		marker, err := s.files.inspect(ctx, e.markerPath())
		if err != nil {
			return err
		}
		if !sameProviderObject(marker, e.Marker) {
			return dataError("resource activation marker changed")
		}
		actual, err := s.files.inspect(ctx, target)
		if err != nil {
			return err
		}
		backup, err := s.files.inspect(ctx, e.backupPath())
		if err != nil {
			return err
		}
		candidate, err := s.files.inspect(ctx, e.candidatePath())
		if err != nil {
			return err
		}
		if backup.Present && !sameProviderObject(backup, e.Old) || candidate.Present && !sameProviderObject(candidate, e.New) {
			return dataError("resource activation staging changed")
		}
		if j.Done {
			if !sameProviderObject(actual, e.New) {
				return dataError("committed resource activation changed")
			}
		} else {
			if !e.BackupIntent && !sameProviderObject(actual, e.Old) || !e.SwapIntent && sameProviderObject(actual, e.New) || e.RestoreDone && !sameProviderObject(actual, e.Old) {
				return dataError("resource activation action mismatch")
			}
			if actual.Present && !sameProviderObject(actual, e.Old) && !sameProviderObject(actual, e.New) {
				return dataError("resource activation target changed")
			}
			if e.Old.Present && !sameProviderObject(actual, e.Old) && !sameProviderObject(backup, e.Old) {
				return dataError("resource activation backup missing")
			}
			if actual.Present && sameProviderObject(actual, e.Old) && backup.Present {
				return dataError("aliased resource activation backup")
			}
			if !e.RestoreIntent {
				e.RestoreIntent = true
				if err = s.saveResourceJournal(ctx, j); err != nil {
					return err
				}
			}
			if backup.Present {
				if err = s.files.move(ctx, e.backupPath(), backup, target, actual); err != nil {
					return err
				}
			} else if !e.Old.Present && actual.Present {
				if err = s.files.remove(ctx, target, actual); err != nil {
					return err
				}
			}
			if !e.RestoreDone {
				e.RestoreDone = true
				if err = s.saveResourceJournal(ctx, j); err != nil {
					return err
				}
			}
		}
	}

	for _, e := range j.Entries {
		for path, want := range map[string]providerObject{e.backupPath(): e.Old, e.candidatePath(): e.New} {
			got, err := s.files.inspect(ctx, path)
			if err != nil {
				return err
			}
			if got.Present {
				if !sameProviderObject(got, want) {
					return dataError("resource activation cleanup changed")
				}
				if err = s.files.remove(ctx, path, got); err != nil {
					return err
				}
			}
		}
	}
	journal, err := s.files.inspect(ctx, resourceJournalPath)
	if err != nil {
		return err
	}
	return s.files.remove(ctx, resourceJournalPath, journal)
}
