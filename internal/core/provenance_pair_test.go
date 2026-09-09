package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

type memoryObject struct {
	bytes []byte
	id    string
}
type memoryDisk struct {
	files map[string]memoryObject
	next  int
}
type memoryStore struct {
	root        string
	denyReceipt bool
	gate        executionGate
	disk        *memoryDisk
	step, fail  int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{disk: &memoryDisk{files: map[string]memoryObject{}}}
}
func (s *memoryStore) coreStore() storeBackend { return s }
func objectKey(r ProvenanceRole, tx string) string {
	if r == InstalledBinary || r == InstalledReceipt || r == PairJournal {
		return string(r)
	}
	return tx + "/" + string(r)
}
func (s *memoryStore) Load(ctx context.Context, r ProvenanceRole, tx string) ([]byte, error) {
	if s.denyReceipt && r == InstalledReceipt {
		return nil, os.ErrPermission
	}
	v, ok := s.disk.files[objectKey(r, tx)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), v.bytes...), ctx.Err()
}
func (s *memoryStore) checkpoint() error {
	s.step++
	if s.step == s.fail {
		return errors.New("simulated power loss")
	}
	return nil
}
func (s *memoryStore) Save(ctx context.Context, r ProvenanceRole, tx string, b []byte) error {
	if err := s.checkpoint(); err != nil {
		return err
	}
	s.disk.next++
	s.disk.files[objectKey(r, tx)] = memoryObject{append([]byte(nil), b...), fmt.Sprint(s.disk.next)}
	return s.checkpoint()
}
func (s *memoryStore) Inspect(ctx context.Context, r ProvenanceRole, tx string) (ProvenanceObject, error) {
	v, ok := s.disk.files[objectKey(r, tx)]
	if !ok {
		return ProvenanceObject{}, nil
	}
	return ProvenanceObject{Present: true, SHA256: digest(v.bytes), Identity: v.id, BootID: "boot-1"}, ctx.Err()
}
func (s *memoryStore) Sync(context.Context) error { return s.checkpoint() }
func (s *memoryStore) Apply(ctx context.Context, m ProvenanceMutation) error {
	v, err := s.Inspect(ctx, m.Role, m.Transaction)
	if err != nil {
		return err
	}
	if v != m.Expected {
		return errors.New("identity changed")
	}
	if m.Source == "" {
		if err = s.checkpoint(); err != nil {
			return err
		}
		delete(s.disk.files, objectKey(m.Role, m.Transaction))
		return s.checkpoint()
	}
	if err = s.checkpoint(); err != nil {
		return err
	}
	key := objectKey(m.Source, m.Transaction)
	source, ok := s.disk.files[key]
	if !ok {
		return os.ErrNotExist
	}
	observed, err := s.Inspect(ctx, m.Source, m.Transaction)
	if err != nil {
		return err
	}
	if !sameObject(observed, m.SourceExpected) {
		return errors.New("source changed")
	}
	s.disk.files[objectKey(m.Role, m.Transaction)] = source
	delete(s.disk.files, key)
	return s.checkpoint()
}

const testTransaction = "0123456789abcdef0123456789abcdef"

func seedPair(t *testing.T, s *memoryStore, old bool) {
	t.Helper()
	for r, b := range map[ProvenanceRole]string{CandidateBinary: "new binary", CandidateReceipt: "new receipt", TransactionMarker: testTransaction} {
		if err := s.Save(context.Background(), r, testTransaction, []byte(b)); err != nil {
			t.Fatal(err)
		}
	}
	if old {
		for r, b := range map[ProvenanceRole]string{InstalledBinary: "old binary", InstalledReceipt: "old receipt"} {
			if err := s.Save(context.Background(), r, "", []byte(b)); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.step = 0
}
func TestProvenancePair_Commit(t *testing.T) {
	s := newMemoryStore()
	seedPair(t, s, true)
	if err := commitProvenance(context.Background(), s, testTransaction); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Load(context.Background(), InstalledBinary, "")
	r, _ := s.Load(context.Background(), InstalledReceipt, "")
	if string(b) != "new binary" || string(r) != "new receipt" {
		t.Fatal("pair was not published")
	}
}
func TestProvenancePair_RecoversEveryDurableBoundary(t *testing.T) {
	control := newMemoryStore()
	seedPair(t, control, true)
	if err := commitProvenance(context.Background(), control, testTransaction); err != nil {
		t.Fatal(err)
	}
	boundaries := control.step
	if boundaries < 16 {
		t.Fatalf("missing WAL boundaries: %d", boundaries)
	}
	for _, old := range []bool{false, true} {
		for failure := 1; failure <= boundaries; failure++ {
			t.Run(fmt.Sprintf("old=%v/failure=%d", old, failure), func(t *testing.T) {
				s := newMemoryStore()
				seedPair(t, s, old)
				s.fail = failure
				_ = commitProvenance(context.Background(), s, testTransaction)
				reopened := &memoryStore{disk: s.disk}
				for range 2 {
					if err := RecoverProvenance(context.Background(), reopened); err != nil {
						t.Fatal(err)
					}
				}
				b, be := reopened.Load(context.Background(), InstalledBinary, "")
				r, re := reopened.Load(context.Background(), InstalledReceipt, "")
				isNew := string(b) == "new binary" && string(r) == "new receipt"
				isOld := string(b) == "old binary" && string(r) == "old receipt"
				absent := errors.Is(be, os.ErrNotExist) && errors.Is(re, os.ErrNotExist)
				if !isNew && !(old && isOld) && !(!old && absent) {
					t.Fatal("recovery left a mixed pair")
				}
			})
		}
	}
}

func (s *memoryStore) location() string {
	if s.root != "" {
		return s.root
	}
	return "/private/data"
}

func (s *memoryStore) open(ctx context.Context, r ProvenanceRole, tx string) (verifiedFile, error) {
	o, e := s.Inspect(ctx, r, tx)
	if e != nil {
		return nil, e
	}
	if !o.Present {
		return nil, os.ErrNotExist
	}
	return &memoryVerifiedFile{s: s, role: r, tx: tx, observed: o, path: s.location() + "/" + objectKey(r, tx)}, nil
}

type memoryVerifiedFile struct {
	s        *memoryStore
	role     ProvenanceRole
	tx, path string
	observed ProvenanceObject
	closed   bool
}

func (f *memoryVerifiedFile) verify(ctx context.Context) (string, error) {
	if f.closed {
		return "", os.ErrClosed
	}
	o, e := f.s.Inspect(ctx, f.role, f.tx)
	if e != nil {
		return "", e
	}
	if o != f.observed {
		return "", errors.New("file changed")
	}
	return f.path, nil
}
func (f *memoryVerifiedFile) Close() error { f.closed = true; return nil }

func (s *memoryStore) target() (string, string) { return "linux", "amd64" }

func cloneMemoryDisk(d *memoryDisk) *memoryDisk {
	copy := &memoryDisk{files: map[string]memoryObject{}, next: d.next}
	for k, v := range d.files {
		copy.files[k] = memoryObject{bytes: append([]byte(nil), v.bytes...), id: v.id}
	}
	return copy
}
func TestProvenancePair_RecoveryItselfIsCrashSafe(t *testing.T) {
	for _, old := range []bool{false, true} {
		base := newMemoryStore()
		seedPair(t, base, old)
		control := &memoryStore{disk: cloneMemoryDisk(base.disk)}
		if e := commitProvenance(context.Background(), control, testTransaction); e != nil {
			t.Fatal(e)
		}
		for fail := 1; fail <= control.step; fail++ {
			s := &memoryStore{disk: cloneMemoryDisk(base.disk), fail: fail}
			_ = commitProvenance(context.Background(), s, testTransaction)
			j, e := loadPair(context.Background(), s)
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			wantNew := j.Phase == "committed"
			recoveryControl := &memoryStore{disk: cloneMemoryDisk(s.disk)}
			if e = RecoverProvenance(context.Background(), recoveryControl); e != nil {
				t.Fatal(e)
			}
			for crash := 1; crash <= recoveryControl.step; crash++ {
				t.Run(fmt.Sprintf("old=%v/commit=%d/recovery=%d", old, fail, crash), func(t *testing.T) {
					interrupted := &memoryStore{disk: cloneMemoryDisk(s.disk), fail: crash}
					_ = RecoverProvenance(context.Background(), interrupted)
					reopened := &memoryStore{disk: interrupted.disk}
					for range 2 {
						if e := RecoverProvenance(context.Background(), reopened); e != nil {
							t.Fatal(e)
						}
					}
					binary, be := reopened.Load(context.Background(), InstalledBinary, "")
					receipt, re := reopened.Load(context.Background(), InstalledReceipt, "")
					if wantNew {
						if string(binary) != "new binary" || string(receipt) != "new receipt" {
							t.Fatal("committed recovery reverted new pair")
						}
					} else if old {
						if string(binary) != "old binary" || string(receipt) != "old receipt" {
							t.Fatal("precommit recovery failed to restore old pair")
						}
					} else if !errors.Is(be, os.ErrNotExist) || !errors.Is(re, os.ErrNotExist) {
						t.Fatal("green precommit recovery retained new pair")
					}
				})
			}
		}
	}
}
func TestProvenancePair_UnknownIdentityRetainsJournal(t *testing.T) {
	s := newMemoryStore()
	seedPair(t, s, true)
	if e := commitProvenance(context.Background(), s, testTransaction); e != nil {
		t.Fatal(e)
	}
	key := objectKey(InstalledBinary, "")
	object := s.disk.files[key]
	object.id = "unrecognized-inode"
	s.disk.files[key] = object
	if e := RecoverProvenance(context.Background(), s); e == nil {
		t.Fatal("unknown identity recovered")
	}
	if _, e := s.Load(context.Background(), PairJournal, ""); e != nil {
		t.Fatal("unconverged journal deleted")
	}
}

func (s *memoryStore) execution() *executionGate { return &s.gate }

func TestProvenancePair_MissingBootIdentityIsCorruption(t *testing.T) {
	s := newMemoryStore()
	seedPair(t, s, true)
	if e := commitProvenance(context.Background(), s, testTransaction); e != nil {
		t.Fatal(e)
	}
	j, e := loadPair(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	j.Members[0].New.BootID = ""
	b, e := json.Marshal(j)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Save(context.Background(), PairJournal, "", b); e != nil {
		t.Fatal(e)
	}
	if e = RecoverProvenance(context.Background(), s); e == nil {
		t.Fatal("missing boot identity treated as a reboot")
	}
}
