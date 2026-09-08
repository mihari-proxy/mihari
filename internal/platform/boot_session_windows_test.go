//go:build windows

package platform

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestWindowsBootSessionRead_IsReadOnly(t *testing.T) {
	backend := &fakeWindowsBootRegistry{children: []windowsBootRegistryChild{{name: "0123456789abcdef0123456789abcdef", protected: true}}}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	id, present, err := provider.Read(context.Background())
	if err != nil || !present || id != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("id=%q present=%v err=%v", id, present, err)
	}
	if got, want := backend.events, []string{"open-parent-read", "verify-parent", "list-children", "close-parent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("read operations=%v want %v", got, want)
	}
}

func TestWindowsBootSessionRead_MissingParentDoesNotInitialize(t *testing.T) {
	backend := &fakeWindowsBootRegistry{missingParent: true}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	id, present, err := provider.Read(context.Background())
	if err != nil || present || id != "" {
		t.Fatalf("id=%q present=%v err=%v", id, present, err)
	}
	if got, want := backend.events, []string{"open-parent-read"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing read operations=%v want %v", got, want)
	}
}

func TestWindowsBootSessionEnsure_RequiresHeldStartupGate(t *testing.T) {
	backend := &fakeWindowsBootRegistry{}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	gateErr := errors.New("startup lock is not held")
	_, err := provider.Ensure(context.Background(), fakeWindowsStartupGate{err: gateErr})
	if !errors.Is(err, gateErr) {
		t.Fatalf("ensure error=%v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("invalid gate reached registry: %v", backend.events)
	}
}

func TestWindowsBootSessionEnsure_CreatesOneProtectedVolatileChild(t *testing.T) {
	backend := &fakeWindowsBootRegistry{}
	random := bytes.NewReader([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10})
	provider := newWindowsBootSessionProvider(backend, random)
	id, err := provider.Ensure(context.Background(), fakeWindowsStartupGate{})
	if err != nil || id != "0123456789abcdeffedcba9876543210" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if backend.createdChild != id || !backend.createdChildVolatile || !backend.createdChildProtected {
		t.Fatalf("child name=%q volatile=%v protected=%v", backend.createdChild, backend.createdChildVolatile, backend.createdChildProtected)
	}
	if got, want := backend.events, []string{"open-or-create-parent", "verify-parent", "list-children", "create-volatile-child", "close-child", "list-children", "close-parent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ensure operations=%v want %v", got, want)
	}
}

func TestWindowsBootSessionEnsure_ReusesOnlySingleProtectedValidChild(t *testing.T) {
	backend := &fakeWindowsBootRegistry{children: []windowsBootRegistryChild{{name: "0123456789abcdef0123456789abcdef", protected: true}}}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	id, err := provider.Ensure(context.Background(), fakeWindowsStartupGate{})
	if err != nil || id != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if backend.createdChild != "" {
		t.Fatalf("existing boot session replaced by %q", backend.createdChild)
	}

	for name, children := range map[string][]windowsBootRegistryChild{
		"multiple": {
			{name: "0123456789abcdef0123456789abcdef", protected: true},
			{name: "fedcba9876543210fedcba9876543210", protected: true},
		},
		"invalid-name": {{name: "not-a-boot-id", protected: true}},
		"unprotected":  {{name: "0123456789abcdef0123456789abcdef", protected: false}},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &fakeWindowsBootRegistry{children: children}
			provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
			if id, err := provider.Ensure(context.Background(), fakeWindowsStartupGate{}); id != "" || !errors.Is(err, ErrWindowsBootSessionInvalid) {
				t.Fatalf("id=%q err=%v", id, err)
			}
			if backend.createdChild != "" {
				t.Fatalf("invalid registry state was rebuilt with %q", backend.createdChild)
			}
		})
	}
}

func TestWindowsBootSessionEnsure_RejectsParentACLFailureWithoutRepair(t *testing.T) {
	aclErr := errors.New("unsafe registry ACL")
	backend := &fakeWindowsBootRegistry{verifyParentErr: aclErr}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	id, err := provider.Ensure(context.Background(), fakeWindowsStartupGate{})
	if id != "" || !errors.Is(err, aclErr) {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if backend.createdChild != "" {
		t.Fatalf("unsafe parent was repaired by creating %q", backend.createdChild)
	}
}

func TestWindowsBootSessionEnsure_ChildCollisionIsNotAdopted(t *testing.T) {
	backend := &fakeWindowsBootRegistry{childCollision: true}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	id, err := provider.Ensure(context.Background(), fakeWindowsStartupGate{})
	if id != "" || !errors.Is(err, ErrWindowsBootSessionInvalid) {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestWindowsBootSession_CanceledContextDoesNotReachRegistry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &fakeWindowsBootRegistry{}
	provider := newWindowsBootSessionProvider(backend, bytes.NewReader(make([]byte, 16)))
	if _, _, err := provider.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error=%v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("canceled read reached registry: %v", backend.events)
	}
	if _, err := provider.Ensure(ctx, fakeWindowsStartupGate{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ensure error=%v", err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("canceled ensure reached registry: %v", backend.events)
	}
}

type fakeWindowsStartupGate struct{ err error }

func (g fakeWindowsStartupGate) CheckWindowsStartupGate() error { return g.err }

type fakeWindowsBootRegistry struct {
	events                []string
	children              []windowsBootRegistryChild
	missingParent         bool
	verifyParentErr       error
	childCollision        bool
	createdChild          string
	createdChildVolatile  bool
	createdChildProtected bool
}

func (f *fakeWindowsBootRegistry) openParentRead(context.Context) (windowsRegistryHandle, error) {
	f.events = append(f.events, "open-parent-read")
	if f.missingParent {
		return 0, errWindowsBootRegistryNotFound
	}
	return 31, nil
}

func (f *fakeWindowsBootRegistry) openOrCreateProtectedParent(context.Context) (windowsRegistryHandle, error) {
	f.events = append(f.events, "open-or-create-parent")
	return 31, nil
}

func (f *fakeWindowsBootRegistry) verifyProtectedParent(_ context.Context, _ windowsRegistryHandle) error {
	f.events = append(f.events, "verify-parent")
	return f.verifyParentErr
}

func (f *fakeWindowsBootRegistry) listChildren(_ context.Context, _ windowsRegistryHandle) ([]windowsBootRegistryChild, error) {
	f.events = append(f.events, "list-children")
	return append([]windowsBootRegistryChild(nil), f.children...), nil
}

func (f *fakeWindowsBootRegistry) createVolatileProtectedChild(_ context.Context, _ windowsRegistryHandle, name string) (windowsRegistryHandle, bool, error) {
	f.events = append(f.events, "create-volatile-child")
	f.createdChild = name
	f.createdChildVolatile = true
	f.createdChildProtected = true
	if !f.childCollision {
		f.children = append(f.children, windowsBootRegistryChild{name: name, protected: true})
	}
	return 32, f.childCollision, nil
}

func (f *fakeWindowsBootRegistry) closeKey(handle windowsRegistryHandle) error {
	if handle == 31 {
		f.events = append(f.events, "close-parent")
	} else {
		f.events = append(f.events, "close-child")
	}
	return nil
}
