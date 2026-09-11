//go:build unix_security && (linux || darwin)

package securitytest

import (
	"fmt"
	"strings"
	"testing"
)

func TestChildFailureSites_OnlyCoordinates(t *testing.T) {
	input := "--- FAIL: TestChild (0.00s)\n    fixture_test.go:42: token=secret /private/path\n    fixture_test.go:42: duplicate\n\tcontrol_test.go:123: sensitive message\n /private/other_test.go:8: private filename\n {\"token\":\"secret\"}\n"
	if got, want := ChildFailureSites([]byte(input)), "fixture_test.go:42:\ncontrol_test.go:123:"; got != want {
		t.Fatalf("coordinates=%q want=%q", got, want)
	}
}

func TestChildFailureSites_BoundsDiagnostics(t *testing.T) {
	var input strings.Builder
	for n := 0; n < 32; n++ {
		fmt.Fprintf(&input, "    fixture_test.go:%d: private-message\n", n+1)
	}
	got := ChildFailureSites([]byte(input.String()))
	if len(strings.Split(got, "\n")) != 16 || strings.Contains(got, "private-message") {
		t.Fatalf("unbounded source diagnostics: %q", got)
	}
	if got := ChildFailureSites([]byte(strings.Repeat("x", 65536) + "\nfixture_test.go:99: hidden")); got != "" {
		t.Fatalf("read past bounded child output: %q", got)
	}
}
