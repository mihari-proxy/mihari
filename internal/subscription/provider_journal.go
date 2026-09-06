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

// PreparedProvider binds validated bytes to the exact previous resource object.
type PreparedProvider struct {
	store          *ProviderStore
	spec           ProviderSpec
	transaction    string
	old, candidate providerObject
	marker         providerObject
	closed         bool
	geo            GeoResourceKind
	configuration  bool
}

func providerDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Commit replaces one local resource and compensates a failed core reload.
func (p *PreparedProvider) Commit(ctx context.Context, reload func(context.Context) error) error {
	if p == nil || p.store == nil || reload == nil {
		return dataError("provider transaction unavailable")
	}
	target, err := providerTarget(p.spec)
	if err != nil {
		return err
	}
	fs := p.store.files
	release, err := p.store.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if p.closed {
		return dataError("provider candidate closed")
	}
	if err := p.store.noPending(ctx); err != nil {
		return err
	}
	if pending, e := fs.inspect(ctx, providerJournalPath); e != nil {
		return e
	} else if pending.Present {
		return dataError("provider recovery required")
	}
	actual, err := fs.inspect(ctx, target)
	if err != nil {
		return err
	}
	if actual != p.old {
		return providerConflict()
	}
	candidatePath := "staging/providers/" + p.transaction + "/candidate"
	actual, err = fs.inspect(ctx, candidatePath)
	if err != nil {
		return err
	}
	if actual != p.candidate {
		return providerConflict()
	}
	marker, err := fs.inspect(ctx, "staging/providers/"+p.transaction+"/transaction-id")
	if err != nil {
		return err
	}
	if marker != p.marker {
		return providerConflict()
	}
	backupPath := target + ".old-" + p.transaction
	failPreparation := func(cause error) error {
		// No target move has been attempted. Resolve ambiguous backup/journal
		// writes now so a transient error does not require a daemon restart.
		if e := p.store.recover(context.WithoutCancel(ctx)); e != nil {
			return providerDegraded()
		}
		return cause
	}
	j := providerJournal{Schema: "mihari.provider-commit/v1", ID: p.transaction, Resource: p.spec.ResourceID, Format: p.spec.Format, Old: p.old, New: p.candidate, Phase: "prepared", Marker: p.marker, SubscriptionID: p.spec.SubscriptionID, Generation: p.spec.Generation, Kind: p.spec.Kind, Name: p.spec.Name}
	if err = p.store.saveJournal(ctx, j); err != nil {
		return failPreparation(err)
	}
	var backup providerObject
	if p.old.Present {
		b, e := fs.read(ctx, target, 16<<20)
		if e != nil {
			return failPreparation(e)
		}
		if providerDigest(b) != p.old.SHA256 {
			return failPreparation(providerConflict())
		}
		if e = fs.write(ctx, backupPath, b, providerObject{}); e != nil {
			return failPreparation(e)
		}
		backup, e = fs.inspect(ctx, backupPath)
		if e != nil {
			return failPreparation(e)
		}
	}
	j.Backup = backup
	j.Phase = "intent"
	if err = p.store.saveJournal(ctx, j); err != nil {
		return failPreparation(err)
	}
	recoveryCtx := context.WithoutCancel(ctx)
	if err = fs.move(ctx, candidatePath, p.candidate, target, p.old); err == nil {
		err = reload(ctx)
	}
	if err == nil {
		var current providerObject
		current, err = fs.inspect(ctx, target)
		if err == nil && current != p.candidate {
			err = providerConflict()
		}
	}
	if err != nil {
		if recoverErr := p.store.recover(recoveryCtx); recoverErr != nil {
			return providerDegraded()
		}
		if reloadErr := reload(recoveryCtx); reloadErr != nil {
			return providerDegraded()
		}
		return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "provider update failed; previous resource restored"}
	}
	j.Phase = "done"
	if err = p.store.saveJournal(recoveryCtx, j); err != nil {
		return providerDegraded()
	}
	err = p.store.recover(recoveryCtx)
	if err == nil {
		err = p.store.cleanupTransaction(recoveryCtx, p.transaction)
	}
	if err == nil {
		p.closed = true
	}
	return err
}

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

func providerTarget(spec ProviderSpec) (string, error) {
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
func providerDegraded() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "provider recovery could not be confirmed", Details: map[string]any{"degraded": true}}
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

// Prepare stages policy-generated bytes privately, retaining the prior identity.
func (s *ProviderStore) Prepare(ctx context.Context, spec ProviderSpec) (*PreparedProvider, error) {
	if s == nil || s.files == nil {
		return nil, dataError("provider store unavailable")
	}
	target, err := providerTarget(spec)
	if err != nil {
		return nil, err
	}
	if len(spec.Inline) == 0 || len(spec.Inline) > 16<<20 {
		return nil, dataError("invalid provider content size")
	}
	return s.prepareBytes(ctx, spec, "", target, spec.Inline)
}

func (s *ProviderStore) prepareGeo(ctx context.Context, geo GeoResourceSpec) (*PreparedProvider, error) {
	name, err := GeoResourcePath(geo.Kind)
	if err != nil {
		return nil, err
	}
	if len(geo.Bytes) == 0 || len(geo.Bytes) > maxGeoResourceBytes || providerDigest(geo.Bytes) != geo.SHA256 {
		return nil, dataError("invalid Geo candidate")
	}
	return s.prepareBytes(ctx, ProviderSpec{}, geo.Kind, "runtime/core-home/"+name, geo.Bytes)
}

func (s *ProviderStore) prepareBytes(ctx context.Context, spec ProviderSpec, geo GeoResourceKind, target string, b []byte) (prepared *PreparedProvider, err error) {
	old, err := s.files.inspect(ctx, target)
	if err != nil {
		return nil, err
	}
	tx, err := newProfileID()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.cleanupTransaction(context.WithoutCancel(ctx), tx))
		}
	}()
	path := "staging/providers/" + tx + "/candidate"
	markerPath := "staging/providers/" + tx + "/transaction-id"
	if err = s.files.write(ctx, markerPath, []byte(tx), providerObject{}); err != nil {
		return nil, err
	}
	marker, err := s.files.inspect(ctx, markerPath)
	if err != nil {
		return nil, err
	}
	if err = s.files.write(ctx, path, b, providerObject{}); err != nil {
		return nil, err
	}
	next, err := s.files.inspect(ctx, path)
	if err != nil {
		return nil, err
	}
	if !next.Present || next.SHA256 != providerDigest(b) {
		return nil, dataError("staged provider content changed")
	}
	spec.Inline = nil // authority is the privately staged object, never a caller slice.
	return &PreparedProvider{store: s, spec: spec, transaction: tx, old: old, candidate: next, marker: marker, geo: geo}, nil
}

func (p *PreparedProvider) targetPath() (string, error) {
	if p.configuration {
		return "runtime/config.yaml", nil
	}
	if p.geo != "" {
		name, err := GeoResourcePath(p.geo)
		return "runtime/core-home/" + name, err
	}
	return providerTarget(p.spec)
}
func (p *PreparedProvider) recheck(ctx context.Context) error {
	if p == nil || p.store == nil {
		return dataError("resource candidate unavailable")
	}
	release, err := p.store.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if p.closed {
		return dataError("resource candidate unavailable")
	}
	target, err := p.targetPath()
	if err != nil {
		return err
	}
	for path, want := range map[string]providerObject{target: p.old, "staging/providers/" + p.transaction + "/candidate": p.candidate, "staging/providers/" + p.transaction + "/transaction-id": p.marker} {
		got, err := p.store.files.inspect(ctx, path)
		if err != nil {
			return err
		}
		if got != want {
			return providerConflict()
		}
	}
	return nil
}

// Close releases private unpublished files, preserving any pending WAL authority.
func (p *PreparedProvider) Close(ctx context.Context) error {
	if p == nil || p.store == nil {
		return nil
	}
	release, err := p.store.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if p.closed {
		return nil
	}
	if err := p.store.noPending(ctx); err != nil {
		return err
	}
	journal, err := p.store.files.inspect(ctx, providerJournalPath)
	if err != nil {
		return err
	}
	if journal.Present {
		return dataError("provider recovery required before cleanup")
	}
	for path, want := range map[string]providerObject{"staging/providers/" + p.transaction + "/candidate": p.candidate, "staging/providers/" + p.transaction + "/transaction-id": p.marker} {
		got, e := p.store.files.inspect(ctx, path)
		if e != nil {
			return e
		}
		if !got.Present {
			continue
		}
		if got != want {
			return providerConflict()
		}
		if e = p.store.files.remove(ctx, path, got); e != nil {
			return e
		}
	}
	p.closed = true
	return p.store.files.removeTransaction(ctx, p.transaction)
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
	} // An empty pre-marker directory grants no file authority.
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
		// No target mutation was authorized. Record an observed completed copy
		// before deleting it, including an ambiguous write/sync result.
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
