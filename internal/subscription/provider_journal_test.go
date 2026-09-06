package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"os"
	"strings"
	"testing"
)

func seededProvider(t *testing.T) (*memoryProviderFiles, *PreparedProvider, string) {
	t.Helper()
	ctx := context.Background()
	fs := newMemoryProviderFiles()
	s := &ProviderStore{files: fs}
	spec := ProviderSpec{SubscriptionID: "0123456789abcdef0123456789abcdef", Generation: 7, Kind: "rule", Name: "domains", Format: "yaml", Inline: []byte("new")}
	var err error
	spec.ResourceID, err = ProviderResourceID(spec.SubscriptionID, spec.Generation, spec.Kind, spec.Name)
	if err != nil {
		t.Fatal(err)
	}
	path, err := providerTarget(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, path, []byte("old"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	fs.mutations = 0
	return fs, p, path
}

func TestProviderCommit_ReloadFailureRestoresOld(t *testing.T) {
	for _, double := range []bool{false, true} {
		t.Run(fmt.Sprint(double), func(t *testing.T) {
			fs, p, path := seededProvider(t)
			calls := 0
			err := p.Commit(context.Background(), func(context.Context) error {
				calls++
				if calls == 1 || double {
					return errors.New("reload failed")
				}
				return nil
			})
			var api protocol.APIError
			if !errors.As(err, &api) || calls != 2 {
				t.Fatalf("rollback result %v calls=%d", err, calls)
			}
			if double && api.Details["degraded"] != true {
				t.Fatal("double failure not degraded")
			}
			if string(fs.objects[path]) != "old" {
				t.Fatal("disk did not retain old bytes")
			}
		})
	}
}

func TestProviderCommit_EveryDurableCrashBoundaryRecovers(t *testing.T) {
	fs, p, _ := seededProvider(t)
	if err := p.Commit(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	boundaries := fs.mutations
	for point := 1; point <= boundaries; point++ {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			fs, p, path := seededProvider(t)
			fs.crashAt = point
			func() {
				defer func() {
					if r := recover(); r != "simulated crash" {
						t.Fatalf("unexpected crash %v", r)
					}
				}()
				_ = p.Commit(context.Background(), func(context.Context) error { return nil })
			}()
			fs.crashAt = 0
			restarted := &ProviderStore{files: fs}
			if err := restarted.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := restarted.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "old"
			if fs.doneDurable {
				want = "new"
			}
			if string(fs.objects[path]) != want {
				t.Fatalf("recovery picked %q, want %q", fs.objects[path], want)
			}
		})
	}
}

type memoryProviderFiles struct {
	objects     map[string][]byte
	identities  map[string]string
	sequence    int
	mutations   int
	crashAt     int
	doneDurable bool
	boot        string
}

func (m *memoryProviderFiles) checkpoint() {
	m.mutations++
	if m.mutations == m.crashAt {
		panic("simulated crash")
	}
}

func newMemoryProviderFiles() *memoryProviderFiles {
	return &memoryProviderFiles{objects: map[string][]byte{}, identities: map[string]string{}, boot: "boot-1"}
}
func (m *memoryProviderFiles) inspect(_ context.Context, path string) (providerObject, error) {
	b, ok := m.objects[path]
	if !ok {
		return providerObject{}, nil
	}
	return providerObject{Present: true, Identity: m.identities[path], SHA256: providerDigest(b), BootID: m.boot}, nil
}
func (m *memoryProviderFiles) read(_ context.Context, path string, limit int64) ([]byte, error) {
	b, ok := m.objects[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	if int64(len(b)) > limit {
		return nil, errors.New("too large")
	}
	return append([]byte(nil), b...), nil
}
func (m *memoryProviderFiles) write(ctx context.Context, path string, b []byte, old providerObject) error {
	actual, _ := m.inspect(ctx, path)
	if actual != old {
		return errors.New("changed identity")
	}
	m.sequence++
	m.objects[path] = append([]byte(nil), b...)
	m.identities[path] = fmt.Sprint(m.sequence)
	if path == providerJournalPath && bytes.Contains(b, []byte(`"phase":"done"`)) {
		m.doneDurable = true
	}
	m.checkpoint()
	return nil
}
func (m *memoryProviderFiles) move(ctx context.Context, from string, source providerObject, to string, target providerObject) error {
	a, _ := m.inspect(ctx, from)
	b, _ := m.inspect(ctx, to)
	if a != source || b != target || !a.Present {
		return errors.New("changed identity")
	}
	m.objects[to] = m.objects[from]
	m.identities[to] = m.identities[from]
	delete(m.objects, from)
	delete(m.identities, from)
	m.checkpoint()
	return nil
}
func (m *memoryProviderFiles) remove(ctx context.Context, path string, old providerObject) error {
	a, _ := m.inspect(ctx, path)
	if a != old {
		return errors.New("changed identity")
	}
	delete(m.objects, path)
	delete(m.identities, path)
	m.checkpoint()
	return nil
}

func TestProviderCommit_ReplacesOnlyAfterDurableIntent(t *testing.T) {
	ctx := context.Background()
	fs := newMemoryProviderFiles()
	s := &ProviderStore{files: fs}
	spec := ProviderSpec{SubscriptionID: "0123456789abcdef0123456789abcdef", Generation: 7, Kind: "rule", Name: "domains", Format: "yaml"}
	id, err := ProviderResourceID(spec.SubscriptionID, spec.Generation, spec.Kind, spec.Name)
	if err != nil {
		t.Fatal(err)
	}
	spec.ResourceID = id
	target := "runtime/core-home/providers/" + id + ".yaml"
	candidate := "staging/providers/0123456789abcdef0123456789abcdef/candidate"
	if err = fs.write(ctx, target, []byte("old"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, candidate, []byte("new"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	old, _ := fs.inspect(ctx, target)
	next, _ := fs.inspect(ctx, candidate)
	markerPath := "staging/providers/0123456789abcdef0123456789abcdef/transaction-id"
	if err = fs.write(ctx, markerPath, []byte("0123456789abcdef0123456789abcdef"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	marker, _ := fs.inspect(ctx, markerPath)
	p := &PreparedProvider{store: s, spec: spec, transaction: "0123456789abcdef0123456789abcdef", old: old, candidate: next, marker: marker}
	called := false
	err = p.Commit(ctx, func(ctx context.Context) error {
		called = true
		b, _ := fs.read(ctx, target, 100)
		if string(b) != "new" {
			t.Fatalf("reload saw %q", b)
		}
		if _, err := fs.read(ctx, "staging/providers/commit.json", 1<<20); err != nil {
			t.Fatal("reload ran without durable journal")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("provider transaction not committed: %v, reload=%v", err, called)
	}
}

func TestProviderRecovery_RejectsDuplicateJournalMembers(t *testing.T) {
	fs, p, _ := seededProvider(t)
	fs.crashAt = 3
	func() {
		defer func() { _ = recover() }()
		_ = p.Commit(context.Background(), func(context.Context) error { return nil })
	}()
	b := fs.objects[providerJournalPath]
	if len(b) == 0 {
		t.Fatal("fixture missed journal")
	}
	b = bytes.Replace(b, []byte(`"phase":"intent"`), []byte(`"phase":"intent","phase":"intent"`), 1)
	fs.objects[providerJournalPath] = b
	fs.crashAt = 0
	if err := p.store.Recover(context.Background()); err == nil {
		t.Fatal("duplicate journal member accepted")
	}
}

func TestProviderRecovery_RejectsIncompleteObjectIdentity(t *testing.T) {
	for _, field := range []string{"boot_id", "sha256", "identity"} {
		t.Run(field, func(t *testing.T) {
			fs, p, _ := seededProvider(t)
			fs.crashAt = 3
			func() {
				defer func() { _ = recover() }()
				_ = p.Commit(context.Background(), func(context.Context) error { return nil })
			}()
			var j map[string]any
			if err := json.Unmarshal(fs.objects[providerJournalPath], &j); err != nil {
				t.Fatal(err)
			}
			j["old"].(map[string]any)[field] = ""
			b, err := json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			fs.objects[providerJournalPath] = b
			fs.crashAt = 0
			if err = p.store.Recover(context.Background()); err == nil {
				t.Fatal("incomplete old identity accepted")
			}
		})
	}
}

func TestProviderRecovery_NestedRecoveryCrashBoundaries(t *testing.T) {
	for point := 1; point <= 7; point++ {
		t.Run(fmt.Sprint(point), func(t *testing.T) {
			fs, p, path := seededProvider(t)
			fs.crashAt = 3
			func() {
				defer func() { _ = recover() }()
				_ = p.Commit(context.Background(), func(context.Context) error { return nil })
			}()
			fs.crashAt = 0
			fs.mutations = 0
			fs.crashAt = point
			func() { defer func() { _ = recover() }(); _ = p.store.Recover(context.Background()) }()
			fs.crashAt = 0
			for i := 0; i < 2; i++ {
				if err := p.store.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if string(fs.objects[path]) != "old" {
				t.Fatal("nested recovery lost old resource")
			}
		})
	}
}
func TestProviderRecovery_RebootUsesMarkerAndHashes(t *testing.T) {
	fs, p, path := seededProvider(t)
	fs.crashAt = 3
	func() {
		defer func() { _ = recover() }()
		_ = p.Commit(context.Background(), func(context.Context) error { return nil })
	}()
	fs.crashAt = 0
	fs.boot = "boot-2"
	for path, id := range fs.identities {
		fs.identities[path] = "reboot-" + id
	}
	if err := p.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(fs.objects[path]) != "old" {
		t.Fatal("reboot recovery lost old resource")
	}
}

func TestProviderCommit_PreIntentCrashDoesNotLeakBackup(t *testing.T) {
	fs, p, _ := seededProvider(t)
	fs.crashAt = 1
	func() {
		defer func() { _ = recover() }()
		_ = p.Commit(context.Background(), func(context.Context) error { return nil })
	}()
	fs.crashAt = 0
	if err := p.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for path := range fs.objects {
		if strings.Contains(path, ".old-") {
			t.Fatal("pre-intent backup leaked")
		}
	}
}

func TestProviderRecovery_CleansAbandonedPrivateCandidates(t *testing.T) {
	fs, p, _ := seededProvider(t)
	if err := p.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for path := range fs.objects {
		if strings.HasPrefix(path, "staging/providers/") {
			t.Fatal("abandoned private candidate retained")
		}
	}
}
func (m *memoryProviderFiles) transactions(context.Context) ([]string, error) {
	seen := map[string]bool{}
	for path := range m.objects {
		parts := strings.Split(path, "/")
		if len(parts) == 4 && parts[0] == "staging" && parts[1] == "providers" {
			seen[parts[2]] = true
		}
	}
	result := []string{}
	for tx := range seen {
		result = append(result, tx)
	}
	return result, nil
}

func TestProviderRecovery_RejectsNoncanonicalJournalShape(t *testing.T) {
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema":`), []byte(`"Schema":`), 1) },
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"recovery_done":false`), []byte(`"recovery_done":null`), 1)
		},
		func(b []byte) []byte { return bytes.Replace(b, []byte(`,"recovery_done":false`), nil, 1) },
	} {
		fs, p, _ := seededProvider(t)
		fs.crashAt = 3
		func() {
			defer func() { _ = recover() }()
			_ = p.Commit(context.Background(), func(context.Context) error { return nil })
		}()
		fs.crashAt = 0
		fs.objects[providerJournalPath] = mutate(fs.objects[providerJournalPath])
		if err := p.store.Recover(context.Background()); err == nil {
			t.Fatal("noncanonical journal shape accepted")
		}
	}
}

func (m *memoryProviderFiles) removeTransaction(context.Context, string) error { return nil }

func cloneProviderMemory(m *memoryProviderFiles) *memoryProviderFiles {
	n := newMemoryProviderFiles()
	n.sequence = m.sequence
	n.boot = m.boot
	n.doneDurable = m.doneDurable
	for k, b := range m.objects {
		n.objects[k] = append([]byte(nil), b...)
	}
	for k, id := range m.identities {
		n.identities[k] = id
	}
	return n
}
func crashProviderOperation(f func()) {
	defer func() {
		if v := recover(); v != nil && v != "simulated crash" {
			panic(v)
		}
	}()
	f()
}
func TestProviderRecovery_EveryForwardAndNestedDurableBoundary(t *testing.T) {
	fs, p, _ := seededProvider(t)
	if err := p.Commit(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for forward := 1; forward <= fs.mutations; forward++ {
		t.Run(fmt.Sprint(forward), func(t *testing.T) {
			fs, p, path := seededProvider(t)
			fs.crashAt = forward
			crashProviderOperation(func() { _ = p.Commit(context.Background(), func(context.Context) error { return nil }) })
			probe := cloneProviderMemory(fs)
			if err := (&ProviderStore{files: probe}).Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			for reverse := 1; reverse <= probe.mutations; reverse++ {
				n := cloneProviderMemory(fs)
				n.crashAt = reverse
				s := &ProviderStore{files: n}
				crashProviderOperation(func() { _ = s.Recover(context.Background()) })
				n.crashAt = 0
				for i := 0; i < 2; i++ {
					if err := s.Recover(context.Background()); err != nil {
						t.Fatalf("reverse %d: %v", reverse, err)
					}
				}
				want := "old"
				if n.doneDurable {
					want = "new"
				}
				if string(n.objects[path]) != want {
					t.Fatal("wrong recovery authority")
				}
			}
		})
	}
}

type providerFaultFiles struct {
	providerFiles
	at, count int
	after     bool
}

func (f *providerFaultFiles) boundary() error {
	f.count++
	if f.count == f.at {
		return errors.New("injected storage failure")
	}
	return nil
}
func (f *providerFaultFiles) write(c context.Context, p string, b []byte, o providerObject) error {
	if !f.after {
		if e := f.boundary(); e != nil {
			return e
		}
	}
	e := f.providerFiles.write(c, p, b, o)
	if f.after && e == nil {
		e = f.boundary()
	}
	return e
}
func (f *providerFaultFiles) move(c context.Context, p string, a providerObject, q string, b providerObject) error {
	if !f.after {
		if e := f.boundary(); e != nil {
			return e
		}
	}
	e := f.providerFiles.move(c, p, a, q, b)
	if f.after && e == nil {
		e = f.boundary()
	}
	return e
}
func (f *providerFaultFiles) remove(c context.Context, p string, a providerObject) error {
	if !f.after {
		if e := f.boundary(); e != nil {
			return e
		}
	}
	e := f.providerFiles.remove(c, p, a)
	if f.after && e == nil {
		e = f.boundary()
	}
	return e
}
func TestProviderRecovery_IOFailuresBeforeAndAfterPublication(t *testing.T) {
	for _, after := range []bool{false, true} {
		baseline, p, _ := seededProvider(t)
		f := &providerFaultFiles{providerFiles: baseline, after: after}
		p.store.files = f
		if e := p.Commit(context.Background(), func(context.Context) error { return nil }); e != nil {
			t.Fatal(e)
		}
		for at := 1; at <= f.count; at++ {
			t.Run(fmt.Sprintf("after=%v/%d", after, at), func(t *testing.T) {
				fs, p, path := seededProvider(t)
				f := &providerFaultFiles{providerFiles: fs, after: after, at: at}
				p.store.files = f
				if e := p.Commit(context.Background(), func(context.Context) error { return nil }); e == nil {
					t.Fatal("injected IO failure swallowed")
				}
				p.store.files = fs
				for i := 0; i < 2; i++ {
					if e := p.store.Recover(context.Background()); e != nil {
						t.Fatal(e)
					}
				}
				want := "old"
				if fs.doneDurable {
					want = "new"
				}
				if string(fs.objects[path]) != want {
					t.Fatal("IO failure lost durable authority")
				}
			})
		}
	}
}

func TestProviderRecovery_RebootDuringPreparedCleanup(t *testing.T) {
	fs, p, path := seededProvider(t)
	fs.crashAt = 2
	crashProviderOperation(func() { _ = p.Commit(context.Background(), func(context.Context) error { return nil }) })
	fs.crashAt = 0
	fs.boot = "boot-2"
	for p, id := range fs.identities {
		fs.identities[p] = "reboot-" + id
	}
	probe := cloneProviderMemory(fs)
	if err := (&ProviderStore{files: probe}).Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for point := 1; point <= probe.mutations; point++ {
		n := cloneProviderMemory(fs)
		n.crashAt = point
		s := &ProviderStore{files: n}
		crashProviderOperation(func() { _ = s.Recover(context.Background()) })
		n.crashAt = 0
		if err := s.Recover(context.Background()); err != nil {
			t.Fatalf("point %d: %v", point, err)
		}
		if string(n.objects[path]) != "old" {
			t.Fatal("reboot lost old bytes")
		}
	}
}

func TestProviderCommit_PrepublicationIOFailureRecoversWithoutRestart(t *testing.T) {
	fs, p, _ := seededProvider(t)
	p.store.files = &providerFaultFiles{providerFiles: fs, at: 2}
	if err := p.Commit(context.Background(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("missing injected error")
	}
	if _, present := fs.objects[providerJournalPath]; present {
		t.Fatal("prepublication failure left blocking journal")
	}
}
