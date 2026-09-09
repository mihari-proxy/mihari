package subscription

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type memoryProviderFiles struct {
	objects             map[string][]byte
	identities          map[string]string
	sequence            int
	mutations           int
	crashAt             int
	doneDurable         bool
	resourceDoneDurable bool
	boot                string
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
func (m *memoryProviderFiles) storeBinding() (string, string) { return "/private/data", "root-1" }
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
	if path == resourceJournalPath && bytes.Contains(b, []byte(`"done":true`)) {
		m.resourceDoneDurable = true
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
