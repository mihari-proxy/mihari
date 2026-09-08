package service

import "testing"

func TestLaunchdRuntimeIdentity_RejectsAmbiguousRecordedStart(t *testing.T) {
	for _, start := range []string{"", "10.1", "010.000001", "-1.000001", "10.1000000", "10.+00001"} {
		if _, err := RecordedLaunchdIdentity("boot-a", 123, start); err == nil {
			t.Fatalf("ambiguous process start accepted: %q", start)
		}
	}
}

func TestLaunchdRuntimeIdentity_BindsRecordedIdentityToGroup(t *testing.T) {
	got, err := RecordedLaunchdIdentity("boot-a", 123, "10.000001")
	if err != nil || got.PID != 123 || got.StartUnix != 10 || got.StartUsec != 1 || got.Group != "darwin-pgid-v1:boot-a:123:10:1" {
		t.Fatalf("recorded group identity: %+v %v", got, err)
	}
	if _, err := RecordedLaunchdIdentity("boot-a", 1, "10.000001"); err == nil {
		t.Fatal("unsafe group identity accepted")
	}
}
