//go:build windows

package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBinary_DefersPostCommitCleanup(t *testing.T) {
	dir := t.TempDir()
	target, candidate := filepath.Join(dir, "binary.exe"), filepath.Join(dir, "candidate.exe")
	for path, content := range map[string]string{target: "old", candidate: "new"} {
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	warning, err := replaceBinary(candidate, target)
	if err != nil || warning != nil {
		t.Fatalf("replace: %v %v", warning, err)
	}
	files, err := filepath.Glob(target + ".old-*")
	if err != nil || len(files) != 1 {
		t.Fatalf("old binary not retained: %v %v", files, err)
	}
	for path, want := range map[string]string{target: "new", files[0]: "old"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
}

func TestReplaceBinary_FailedPublicationRestoresOldTarget(t *testing.T) {
	publication, rollback := errors.New("publication failed"), errors.New("rollback failed")
	for _, restoreFails := range []bool{false, true} {
		var moves [][2]string
		warning, err := replaceBinaryWithOps("candidate", "target", func(from, to string) error {
			moves = append(moves, [2]string{from, to})
			if len(moves) == 2 {
				return publication
			}
			if len(moves) == 3 && restoreFails {
				return rollback
			}
			return nil
		})
		if warning != nil || !errors.Is(err, publication) || errors.Is(err, rollback) != restoreFails || len(moves) != 3 || moves[2][0] != moves[0][1] || moves[2][1] != "target" {
			t.Fatalf("rollback: moves=%v warning=%v err=%v", moves, warning, err)
		}
	}
}
