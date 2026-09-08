package app

import "testing"

func TestRetainedDataAuthority_LifecycleRoundTripAndTampering(t *testing.T) {
	original := JournalObject{Present: true, Identity: "1:2", MountID: "3", BootID: "boot"}
	marker := sha256Hex("original install")
	for _, operation := range []string{"stop", "start", "uninstall", "install"} {
		if !retainedDataMatches(original, marker, original, marker, "boot") {
			t.Fatalf("%s lost retained data authority", operation)
		}
	}
	changed := original
	changed.Identity = "1:99"
	if retainedDataMatches(original, marker, changed, marker, "boot") {
		t.Fatal("equal marker authorized replaced data root")
	}
	if retainedDataMatches(original, marker, original, sha256Hex("changed"), "boot") {
		t.Fatal("tampered marker authorized data")
	}
	if !retainedDataMatches(original, marker, changed, marker, "new boot") {
		t.Fatal("cross-boot private marker proof rejected")
	}
	if retainedDataMatches(original, "absent", changed, "absent", "new boot") {
		t.Fatal("cross-boot data without private marker authorized")
	}
}
