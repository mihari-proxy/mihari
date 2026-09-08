package app

import (
	"context"
	"os"
	"strings"
	"sync"
)

// NewMemoryInstallJournalForTest supplies only volatile journal storage for
// cross-package lifecycle tests. It cannot touch a filesystem or a service.
// Production callers must use NewInstallJournalStore with a trusted root.
func NewMemoryInstallJournalForTest() *InstallJournalStore {
	return &InstallJournalStore{files: &activationTestFiles{files: make(map[string][]byte)}}
}

type activationTestFiles struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (f *activationTestFiles) object(name string) JournalObject {
	raw, ok := f.files[name]
	if !ok {
		return JournalObject{}
	}
	marker := ""
	parts := strings.Split(name, "/")
	if len(parts) == 3 {
		marker = parts[1]
	}
	return JournalObject{Present: true, SHA256: sha256HexBytes(raw), Dev: "1", Ino: "1", MountID: "1", BootID: "test-boot", Marker: marker, Identity: "1:1"}
}
func (f *activationTestFiles) inspect(ctx context.Context, name string) (JournalObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return JournalObject{}, err
	}
	return f.object(name), nil
}
func (f *activationTestFiles) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if int64(len(raw)) > limit {
		return nil, invalidInstallJournal()
	}
	return append([]byte(nil), raw...), nil
}
func (f *activationTestFiles) write(ctx context.Context, name string, raw []byte, want JournalObject) (JournalDurability, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return JournalDurability{}, err
	}
	if want.Present && want.SHA256 != f.object(name).SHA256 {
		return JournalDurability{}, unknownInstallState()
	}
	f.files[name] = append([]byte(nil), raw...)
	return JournalDurability{Published: true, Durable: true, Object: f.object(name)}, nil
}
