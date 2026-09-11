package subscription

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
)

type providerObject struct {
	Present  bool   `json:"present"`
	Identity string `json:"identity"`
	SHA256   string `json:"sha256"`
	BootID   string `json:"boot_id"`
}

type providerFiles interface {
	storeBinding() (path, identity string)
	inspect(context.Context, string) (providerObject, error)
	read(context.Context, string, int64) ([]byte, error)
	write(context.Context, string, []byte, providerObject) error
	move(context.Context, string, providerObject, string, providerObject) error
	remove(context.Context, string, providerObject) error
	transactions(context.Context) ([]string, error)
	removeTransaction(context.Context, string) error
}

// ProviderStore holds daemon-owned fixed provider files and their recovery journal.
// Its only production constructor borrows a trusted Unix data root.
type ProviderStore struct {
	files providerFiles
	once  sync.Once
	gate  chan struct{}
}

func (s *ProviderStore) acquire(ctx context.Context) (func(), error) {
	if s == nil || s.files == nil {
		return nil, dataError("provider store unavailable")
	}
	s.once.Do(func() { s.gate = make(chan struct{}, 1) })
	select {
	case s.gate <- struct{}{}:
		return func() { <-s.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func providerDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

const providerJournalPath = "staging/providers/commit.json"

type providerJournal struct {
	Schema         string         `json:"schema"`
	ID             string         `json:"id"`
	Resource       string         `json:"resource_id"`
	Format         string         `json:"format"`
	Old            providerObject `json:"old"`
	New            providerObject `json:"new"`
	Backup         providerObject `json:"backup"`
	Phase          string         `json:"phase"`
	Marker         providerObject `json:"marker"`
	SubscriptionID string         `json:"subscription_id"`
	Generation     uint64         `json:"generation"`
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	RecoveryIntent bool           `json:"recovery_intent"`
	RecoveryDone   bool           `json:"recovery_done"`
}

func providerTarget(spec legacyProviderIdentity) (string, error) {
	id, err := ProviderResourceID(spec.SubscriptionID, spec.Generation, spec.Kind, spec.Name)
	if err != nil || id != spec.ResourceID {
		return "", dataError("provider identity mismatch")
	}
	return providerResourcePath(id, spec.Format)
}
func providerResourcePath(id, format string) (string, error) {
	if len(id) != 64 {
		return "", dataError("invalid provider resource")
	}
	if _, e := hex.DecodeString(id); e != nil {
		return "", dataError("invalid provider resource")
	}
	ext := "yaml"
	if format == "text" {
		ext = "txt"
	} else if format != "yaml" {
		return "", dataError("invalid provider format")
	}
	return "runtime/core-home/providers/" + id + "." + ext, nil
}
func providerConflict() error {
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "provider changed during preparation"}
}

func (s *ProviderStore) saveJournal(ctx context.Context, j providerJournal) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	old, err := s.files.inspect(ctx, providerJournalPath)
	if err != nil {
		return err
	}
	return s.files.write(ctx, providerJournalPath, b, old)
}

// Recover restores unfinished replacements before workers, core recovery or execution.
// A durable done record accepts only the reloaded new file.
func (s *ProviderStore) Recover(ctx context.Context) error {
	release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err = s.recoverResources(ctx); err != nil {
		return err
	}
	if err = s.recover(ctx); err != nil {
		return err
	}
	transactions, err := s.files.transactions(ctx)
	if err != nil {
		return err
	}
	for _, tx := range transactions {
		if err = s.cleanupTransaction(ctx, tx); err != nil {
			return err
		}
	}
	return nil
}

func (s *ProviderStore) cleanupTransaction(ctx context.Context, tx string) error {
	if !profileIDPattern.MatchString(tx) {
		return dataError("invalid staging transaction")
	}
	prefix := "staging/providers/" + tx + "/"
	marker, err := s.files.inspect(ctx, prefix+"transaction-id")
	if err != nil {
		return err
	}
	if !marker.Present {
		return s.files.removeTransaction(ctx, tx)
	}
	if !validProviderObject(marker) || marker.SHA256 != providerDigest([]byte(tx)) {
		return dataError("invalid staging transaction marker")
	}
	for _, name := range []string{"candidate", "source", "transaction-id"} {
		object, e := s.files.inspect(ctx, prefix+name)
		if e != nil {
			return e
		}
		if !object.Present {
			continue
		}
		if name == "transaction-id" && object != marker {
			return providerConflict()
		}
		if e = s.files.remove(ctx, prefix+name, object); e != nil {
			return e
		}
	}
	return s.files.removeTransaction(ctx, tx)
}

func (s *ProviderStore) recover(ctx context.Context) error {
	b, err := s.files.read(ctx, providerJournalPath, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var j providerJournal
	if err = providerJSONShape(b, reflect.TypeOf(j)); err != nil {
		return dataError("invalid provider journal shape")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = uniqueProviderJSON(dec, 0); err != nil {
		return dataError("invalid provider journal")
	}
	if _, err = dec.Token(); err != io.EOF {
		return dataError("invalid provider journal")
	}
	dec = json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&j); err != nil {
		return dataError("invalid provider journal")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return dataError("invalid provider journal")
	}
	if j.Schema != "mihari.provider-commit/v1" || !profileIDPattern.MatchString(j.ID) || (j.Phase != "prepared" && j.Phase != "intent" && j.Phase != "done") || !j.New.Present || j.New.Identity == "" || len(j.New.SHA256) != 64 {
		return dataError("invalid provider journal")
	}
	id, err := ProviderResourceID(j.SubscriptionID, j.Generation, j.Kind, j.Name)
	if err != nil || id != j.Resource || !j.Marker.Present || j.Marker.SHA256 != providerDigest([]byte(j.ID)) || j.RecoveryDone && !j.RecoveryIntent || j.Phase == "done" && j.RecoveryIntent {
		return dataError("invalid provider journal identity")
	}
	for _, object := range []providerObject{j.Old, j.New, j.Backup, j.Marker} {
		adoptedBackup := j.Phase == "prepared" && j.RecoveryDone && object == j.Backup
		if !validProviderObject(object) || object.Present && object.BootID != j.Marker.BootID && !adoptedBackup {
			return dataError("invalid provider journal object")
		}
	}
	identities := map[string]bool{}
	for _, object := range []providerObject{j.Old, j.New, j.Backup, j.Marker} {
		if object.Present {
			key := object.BootID + "/" + object.Identity
			if identities[key] {
				return dataError("aliased provider journal object")
			}
			identities[key] = true
		}
	}
	if j.Phase != "prepared" && (j.Old.Present != j.Backup.Present || j.Old.Present && j.Old.SHA256 != j.Backup.SHA256) {
		return dataError("invalid provider journal backup")
	}
	marker, err := s.files.inspect(ctx, "staging/providers/"+j.ID+"/transaction-id")
	if err != nil {
		return err
	}
	if !sameProviderObject(marker, j.Marker) {
		return dataError("provider transaction identity changed")
	}
	target, err := providerResourcePath(j.Resource, j.Format)
	if err != nil {
		return err
	}
	backupPath := target + ".old-" + j.ID
	actual, err := s.files.inspect(ctx, target)
	if err != nil {
		return err
	}
	if j.Phase == "prepared" {
		if !sameProviderObject(actual, j.Old) {
			return dataError("prepared provider target changed")
		}
		backup, e := s.files.inspect(ctx, backupPath)
		if e != nil {
			return e
		}
		if backup.Present && (!j.Old.Present || backup.SHA256 != j.Old.SHA256) {
			return dataError("unrecognized prepared backup")
		}

		if j.Backup.Present && !sameProviderObject(backup, j.Backup) && (backup.Present || !j.RecoveryDone) {
			return dataError("prepared backup changed")
		}
		if !j.RecoveryDone {
			j.Backup = backup
			j.RecoveryIntent = true
			j.RecoveryDone = true
			if err = s.saveJournal(ctx, j); err != nil {
				return err
			}
		}
	}
	if j.Phase == "done" {
		if !sameProviderObject(actual, j.New) {
			return dataError("committed provider identity changed")
		}
	} else if j.Phase != "prepared" {
		if j.RecoveryDone && !sameProviderObject(actual, j.Old) && !sameProviderObject(actual, j.Backup) {
			return dataError("restored provider identity changed")
		}
		if !sameProviderObject(actual, j.Old) && !sameProviderObject(actual, j.New) && !sameProviderObject(actual, j.Backup) {
			return dataError("provider recovery identity changed")
		}
		if !j.RecoveryIntent {
			j.RecoveryIntent = true
			if err = s.saveJournal(ctx, j); err != nil {
				return err
			}
		}
		if j.Old.Present {
			if !j.Backup.Present || j.Backup.SHA256 != j.Old.SHA256 {
				return dataError("invalid provider backup")
			}
			if !sameProviderObject(actual, j.Old) && !sameProviderObject(actual, j.Backup) {
				backup, e := s.files.inspect(ctx, backupPath)
				if e != nil {
					return e
				}
				if !sameProviderObject(backup, j.Backup) {
					return dataError("provider backup identity changed")
				}
				if err = s.files.move(ctx, backupPath, backup, target, actual); err != nil {
					return err
				}
			}
		} else if actual.Present {
			if err = s.files.remove(ctx, target, actual); err != nil {
				return err
			}
		}
		if !j.RecoveryDone {
			j.RecoveryDone = true
			if err = s.saveJournal(ctx, j); err != nil {
				return err
			}
		}
	}
	for _, path := range []string{backupPath, "staging/providers/" + j.ID + "/candidate"} {
		actual, e := s.files.inspect(ctx, path)
		if e != nil {
			return e
		}
		expected := j.New
		if path == backupPath {
			expected = j.Backup
		}
		if actual.Present {
			if !sameProviderObject(actual, expected) {
				return dataError("provider cleanup identity changed")
			}
			if err = s.files.remove(ctx, path, actual); err != nil {
				return err
			}
		}
	}
	journal, err := s.files.inspect(ctx, providerJournalPath)
	if err != nil {
		return err
	}
	return s.files.remove(ctx, providerJournalPath, journal)
}

func validProviderObject(o providerObject) bool {
	if !o.Present {
		return o == (providerObject{})
	}
	b, err := hex.DecodeString(o.SHA256)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == o.SHA256 && o.Identity != "" && o.BootID != ""
}
func sameProviderObject(a, b providerObject) bool {
	if !a.Present || !b.Present {
		return a.Present == b.Present
	}
	return validProviderObject(a) && validProviderObject(b) && a.SHA256 == b.SHA256 && (a.BootID != b.BootID || a.Identity == b.Identity)
}

func uniqueProviderJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return os.ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			name, ok := key.(string)
			if !ok || seen[strings.ToLower(name)] {
				return os.ErrInvalid
			}
			seen[strings.ToLower(name)] = true
			if e = uniqueProviderJSON(d, depth+1); e != nil {
				return e
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err = uniqueProviderJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}

// Require exact names and all fields, including explicit false/absent objects.
// encoding/json alone accepts case aliases and null scalar zero values.
func providerJSONShape(b []byte, t reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return os.ErrInvalid
	}
	if t.Kind() == reflect.Slice {
		var items []json.RawMessage
		if err := json.Unmarshal(b, &items); err != nil {
			return err
		}
		for _, item := range items {
			if err := providerJSONShape(item, t.Elem()); err != nil {
				return err
			}
		}
		return nil
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		return err
	}
	if len(members) != t.NumField() {
		return os.ErrInvalid
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		raw, ok := members[field.Tag.Get("json")]
		if !ok {
			return os.ErrInvalid
		}
		if err := providerJSONShape(raw, field.Type); err != nil {
			return err
		}
	}
	return nil
}
