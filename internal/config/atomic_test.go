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
	result, err := writeAtomic(path, []byte("new"), 0o600, atomicWriteOps{
		replace: func(_, _ string) error { return replaceError },
		syncDir: syncDirectory,
	})
	if !errors.Is(err, replaceError) {
		t.Fatalf("err=%v", err)
	}
	if result.Committed {
		t.Fatalf("result=%#v, want uncommitted", result)
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

func TestAtomicWrite_ReplaceAndCleanupFailuresKeepBothCauses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	replaceCause := errors.New("replace fixture failure")
	var temporary string
	result, err := writeAtomic(path, []byte("new"), 0600, atomicWriteOps{
		replace: func(source, _ string) error {
			temporary = source
			// A non-empty directory at the temporary path makes the real
			// cleanup fail on every OS, without changing user filesystem state.
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "fixture"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			return replaceCause
		},
		syncDir: syncDirectory,
	})
	var cleanup *os.PathError
	if !errors.Is(err, replaceCause) || !errors.As(err, &cleanup) || cleanup.Path != temporary {
		t.Fatalf("replace or cleanup cause missing: %v", err)
	}
	if result.Committed {
		t.Fatal("failed replace became committed")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "old" {
		t.Fatal("last valid configuration changed")
	}
}

func TestAtomicWriteWithCommitReportsDirectorySyncFailureAfterReplacement(t *testing.T) {
	// This catches treating a durability warning after replace as an uncommitted write.
	path := filepath.Join(t.TempDir(), "settings.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	syncError := errors.New("sync directory failed")
	result, err := writeAtomic(path, []byte("new"), 0o600, atomicWriteOps{
		replace: replaceFile,
		syncDir: func(string) error {
			return syncError
		},
	})
	if err != nil {
		t.Fatalf("writeAtomic() error = %v", err)
	}
	if !result.Committed || !errors.Is(result.Warning, syncError) {
		t.Fatalf("result=%#v, want committed directory sync warning", result)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new" {
		t.Fatalf("active file=%q, want new content", raw)
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
