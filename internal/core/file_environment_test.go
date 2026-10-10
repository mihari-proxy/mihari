package core

import (
	"strings"
	"testing"
)

func TestFileReferenceEnvironment_OverridesInheritedPolicy(t *testing.T) {
	t.Setenv("SKIP_SAFE_PATH_CHECK", "false")
	count := 0
	for _, entry := range FileReferenceEnvironment() {
		if strings.HasPrefix(strings.ToUpper(entry), "SKIP_SAFE_PATH_CHECK=") {
			count++
			if entry != "SKIP_SAFE_PATH_CHECK=true" {
				t.Fatalf("policy=%s", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("policy count=%d", count)
	}
	if !strings.Contains(strings.Join(fixedEnvironment("/private/home"), "\n"), "SKIP_SAFE_PATH_CHECK=true") {
		t.Fatal("trusted core rejects permitted file references")
	}
}
