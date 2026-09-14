package diagnostics

import (
	"strings"
	"testing"
)

func TestHTTPOriginal_PreservesBodyUpTo256KiB(t *testing.T) {
	raw := "secret=fixture-password\n" + strings.Repeat("response context ", 10000)
	if got := HTTPBody([]byte(raw)); got != raw {
		t.Fatal("HTTP diagnostic was shortened or redacted")
	}
	oversize := strings.Repeat("x", (256<<10)+1)
	if got := HTTPBody([]byte(oversize)); len(got) > 256<<10 || !strings.HasSuffix(got, " [truncated]") {
		t.Fatal("HTTP diagnostic limit or truncation marker is incorrect")
	}
}

func TestHTTPOriginal_InvalidBytesStillDiscloseTruncation(t *testing.T) {
	raw := strings.Repeat("\xff", MaxHTTPBodyBytes+100)
	got := HTTPBody([]byte(raw))
	if len(got) > MaxHTTPBodyBytes || !strings.HasSuffix(got, " [truncated]") || !strings.Contains(got, "[invalid UTF-8]") {
		t.Fatal("UTF-8 repair hid discarded input or exceeded the body budget")
	}
}
