//go:build darwin

package service

import (
	"encoding/hex"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinBootID_MatchesInstallJournalObservation(t *testing.T) {
	raw, err := unix.SysctlRaw("kern.boottime")
	if err != nil {
		t.Fatal(err)
	}
	got, err := darwinBootID()
	if err != nil {
		t.Fatal(err)
	}
	if want := hex.EncodeToString(raw); got != want {
		t.Fatalf("service boot ID %q differs from install journal boot ID %q", got, want)
	}
}
