package platform

import "testing"

func TestTrustedRoot_FDInfoMountID(t *testing.T) {
	got, err := parseTrustedMountID([]byte("pos:\t0\nflags:\t0100000\nmnt_id:\t42\nino:\t1\n"))
	if err != nil || got != 42 {
		t.Fatalf("positive mount id: %d %v", got, err)
	}
	for _, s := range []string{"", "mnt_id: 0\n", "mnt_id: -1\n", "mnt_id: +2\n", "mnt_id: 12x\n", "mnt_id: 1\nmnt_id: 2\n", "mnt_id: 18446744073709551616\n"} {
		if _, err := parseTrustedMountID([]byte(s)); err == nil {
			t.Fatalf("accepted invalid fdinfo %q", s)
		}
	}
}
