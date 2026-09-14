//go:build windows

package core

import (
	"errors"
	"testing"
)

func TestReplaceBinaryWithOps_ReturnsPostCommitCleanupWarning(t *testing.T) {
	cleanupErr := errors.New("remove old core fixture")
	renames := 0
	warning, err := replaceBinaryWithOps("candidate", "target", func(string, string) error {
		renames++
		return nil
	}, func(string) error { return cleanupErr })
	if err != nil || !errors.Is(warning, cleanupErr) || renames != 2 {
		t.Fatalf("post-commit cleanup warning lost: warning=%v err=%v renames=%d", warning, err, renames)
	}
}
