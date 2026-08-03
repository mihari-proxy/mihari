package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteKeepsPreviousFileWhenReplaceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaceError := errors.New("replace failed")
	err := writeAtomic(path, []byte("new"), 0o600, func(_, _ string) error {
		return replaceError
	})
	if !errors.Is(err, replaceError) {
		t.Fatalf("err=%v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old" {
		t.Fatalf("active file=%q", raw)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".settings.yaml.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestAtomicWriteCreatesParentAndReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.yaml")
	if err := AtomicWrite(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "second" {
		t.Fatalf("content=%q", raw)
	}
}
