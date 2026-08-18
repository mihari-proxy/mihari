package ui

import "testing"

func TestClassifyPortHold_ListenFreeIsAvailable(t *testing.T) {
	got := ClassifyPortHold(true, 9736, "mihomo.exe", 42)
	if got.Kind != PortHoldAvailable {
		t.Fatalf("kind=%v want available", got.Kind)
	}
}

func TestClassifyPortHold_MatchingOwnerPIDIsOwned(t *testing.T) {
	got := ClassifyPortHold(false, 42, "mihomo.exe", 42)
	if got.Kind != PortHoldOwned || got.PID != 42 {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassifyPortHold_SameMihomoNameDifferentPIDIsOccupied(t *testing.T) {
	got := ClassifyPortHold(false, 9736, "mihomo.exe", 42)
	if got.Kind != PortHoldOccupied || got.PID != 9736 || got.Process != "mihomo.exe" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassifyPortHold_MihariNameWithoutMatchingPIDIsOccupied(t *testing.T) {
	got := ClassifyPortHold(false, 88, "mihari.exe", 0)
	if got.Kind != PortHoldOccupied {
		t.Fatalf("mihari.exe with no owner pid must not be owned: %#v", got)
	}
	got = ClassifyPortHold(false, 88, "mihari.exe", 99)
	if got.Kind != PortHoldOccupied {
		t.Fatalf("other mihari.exe must be occupied: %#v", got)
	}
}

func TestClassifyPortHold_ZeroOwnerPIDNeverOwned(t *testing.T) {
	got := ClassifyPortHold(false, 9736, "mihomo.exe", 0)
	if got.Kind != PortHoldOccupied {
		t.Fatalf("core pid 0 must treat any mihomo as occupied: %#v", got)
	}
}

func TestClassifyPortHold_LookupMissIsUnknown(t *testing.T) {
	got := ClassifyPortHold(false, 0, "", 42)
	if got.Kind != PortHoldUnknown {
		t.Fatalf("got=%#v", got)
	}
}

func TestFormatPortHoldLabel_OccupiedIncludesPID(t *testing.T) {
	if got := FormatPortHoldLabel(PortHold{Kind: PortHoldOccupied, Process: "mihomo.exe", PID: 9736}); got != "Occupied by mihomo.exe (9736)" {
		t.Fatalf("got=%q", got)
	}
	if got := FormatPortHoldLabel(PortHold{Kind: PortHoldOccupied, PID: 9736}); got != "Occupied by PID 9736" {
		t.Fatalf("got=%q", got)
	}
}

func TestPortHoldTone_OccupiedIsDangerOwnedIsPositive(t *testing.T) {
	if PortHoldTone(PortHoldOccupied) != ToneNegative {
		t.Fatal("occupied must be danger")
	}
	if PortHoldTone(PortHoldOwned) != TonePositive {
		t.Fatal("owned must be positive")
	}
	if PortHoldTone(PortHoldAvailable) != ToneNeutral || PortHoldTone(PortHoldUnknown) != ToneNeutral {
		t.Fatal("available/unknown must be muted")
	}
}
