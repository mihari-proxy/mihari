package core

import (
	"strings"
	"testing"
)

func TestUpdateRolePathsStayWithinSeparateFixedNamespace(t *testing.T) {
	for _, role := range []ProvenanceRole{UpdateCandidate, UpdateBackup, UpdateRestore, UpdateMarker} {
		path, mode, err := updateRolePath(role, testTransaction)
		if err != nil || !strings.HasPrefix(path, "staging/core/update/"+testTransaction+"/") || (mode != 0o600 && mode != 0o700) {
			t.Fatalf("role=%s path=%q mode=%o err=%v", role, path, mode, err)
		}
	}
	if path, mode, err := updateRolePath(UpdateJournal, ""); err != nil || path != "staging/core/update-journal.json" || mode != 0o600 {
		t.Fatalf("journal path=%q mode=%o err=%v", path, mode, err)
	}
	for _, tx := range []string{"", "../outside", strings.Repeat("a", 31), strings.Repeat("A", 32)} {
		if path, _, err := updateRolePath(UpdateRestore, tx); err == nil {
			t.Fatalf("invalid transaction %q resolved to %q", tx, path)
		}
	}
	if _, _, err := updateRolePath(ProvenanceRole("../../outside"), testTransaction); err == nil {
		t.Fatal("arbitrary role accepted")
	}
}
