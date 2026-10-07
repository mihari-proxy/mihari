package core

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestLocalCoreDoesNotRequireOfficialReceipt(t *testing.T) {
	for _, receipt := range []string{"", "obsolete or malformed receipt"} {
		t.Run(receipt, func(t *testing.T) {
			store := newMemoryStore()
			if err := store.Save(t.Context(), InstalledBinary, "", []byte("administrator deployed self-built core")); err != nil {
				t.Fatal(err)
			}
			if receipt != "" {
				if err := store.Save(t.Context(), InstalledReceipt, "", []byte(receipt)); err != nil {
					t.Fatal(err)
				}
			}
			verified, err := OpenInstalledCore(t.Context(), store)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := verified.Close(); err != nil {
					t.Error(err)
				}
			}()
			x := &recordedExecutor{}
			if _, err := DetectVerifiedVersion(t.Context(), verified, x); err != nil || len(x.commands) != 1 {
				t.Fatalf("local core rejected: err=%v commands=%d", err, len(x.commands))
			}
			if err := store.Save(t.Context(), InstalledBinary, "", []byte("replacement after admission")); err != nil {
				t.Fatal(err)
			}
			if _, err := DetectVerifiedVersion(t.Context(), verified, x); err == nil || len(x.commands) != 1 {
				t.Fatal("changed file reached execution through stale capability")
			}
			if next, err := OpenInstalledCore(t.Context(), store); err == nil || next != nil {
				t.Fatal("same daemon silently readmitted a changed local file")
			}
		})
	}
}

func TestLocalCoreCannotBypassPendingLegacyRecovery(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("local core")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), PairJournal, "", []byte("unfinished journal")); err != nil {
		t.Fatal(err)
	}
	if v, err := OpenInstalledCore(t.Context(), store); err == nil || v != nil {
		t.Fatal("pending legacy recovery was bypassed")
	}
}

type deniedLocalStore struct{ *memoryStore }

func (s *deniedLocalStore) coreStore() storeBackend { return s }
func (*deniedLocalStore) open(context.Context, ProvenanceRole, string) (verifiedFile, error) {
	return nil, os.ErrPermission
}

func TestLocalCoreRequiresPlatformExecutionPermission(t *testing.T) {
	store := &deniedLocalStore{newMemoryStore()}
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("unsafe core")); err != nil {
		t.Fatal(err)
	}
	if v, err := OpenInstalledCore(t.Context(), store); !errors.Is(err, os.ErrPermission) || v != nil {
		t.Fatal("unsafe deployment accepted")
	}
}

func TestTrustedRuntimeInitializesLocalCoreWithoutReceipt(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("self-built core")); err != nil {
		t.Fatal(err)
	}
	files := &memoryConfigs{s: store}
	executor := &recordedExecutor{}
	runtime := &TrustedExecution{store: store, files: files, executor: executor}
	available, err := runtime.InstalledAvailable(t.Context())
	if err != nil || !available {
		t.Fatalf("available=%v err=%v", available, err)
	}
	if err := runtime.InitializeConfig(t.Context(), []byte("generated config")); err != nil {
		t.Fatal(err)
	}
	if files.writes != 1 || len(executor.commands) != 1 || executor.commands[0].Args[0] != "-t" {
		t.Fatalf("config writes=%d commands=%v", files.writes, executor.commands)
	}
}
