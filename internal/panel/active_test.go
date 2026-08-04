package panel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadActiveMissingReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	active, err := LoadActive(path)
	if err != nil {
		t.Fatal(err)
	}
	if active.Panel != "" || active.Build != "" {
		t.Fatalf("active=%#v", active)
	}
}

func TestSaveAndLoadActiveAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web", "active.json")
	want := Active{Panel: IDZashboard, Build: "v2.1.0"}
	if err := SaveActive(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadActive(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
	// Overwrite replaces content; no leftover temp files in parent.
	next := Active{Panel: IDMetaCubeXD, Build: "8e31c4a"}
	if err := SaveActive(path, next); err != nil {
		t.Fatal(err)
	}
	got, err = LoadActive(path)
	if err != nil || got != next {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "active.json" {
			t.Fatalf("unexpected entry after atomic save: %s", entry.Name())
		}
	}
}

func TestSaveActiveRejectsIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := SaveActive(path, Active{Panel: IDZashboard}); err == nil {
		t.Fatal("expected incomplete active to be rejected")
	}
	if err := SaveActive(path, Active{Build: "v1"}); err == nil {
		t.Fatal("expected incomplete active to be rejected")
	}
}
