package core

import (
	"strings"
	"testing"
)

func TestOpenValidationCore_BundledBinaryWithoutReceipt(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("bundled mihomo")); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenValidationCore(t.Context(), store)
	if err != nil || !opened {
		t.Fatalf("bundled core without receipt rejected: opened=%v err=%v", opened, err)
	}
}

func TestOpenValidationCore_MalformedReceiptStillOpens(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("bundled mihomo")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), InstalledReceipt, "", []byte("obsolete or malformed receipt")); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenValidationCore(t.Context(), store)
	if err != nil || !opened {
		t.Fatalf("local core with a malformed receipt rejected: opened=%v err=%v", opened, err)
	}
}

func TestOpenValidationCore_AbsentBinary(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), InstalledReceipt, "", []byte("orphan receipt")); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenValidationCore(t.Context(), store)
	if err != nil || opened {
		t.Fatalf("missing binary = opened %v err %v", opened, err)
	}
}

func TestOpenValidationCore_AbsentBinaryWithPendingJournal(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), PairJournal, "", []byte("unfinished journal")); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenValidationCore(t.Context(), store)
	if err == nil || opened || !strings.Contains(err.Error(), "provenance recovery required") {
		t.Fatalf("absent core with a pending journal was accepted: opened=%v err=%v", opened, err)
	}
}

func TestOpenValidationCore_PendingJournalStillRejected(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("bundled mihomo")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), PairJournal, "", []byte("unfinished journal")); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenValidationCore(t.Context(), store)
	if err == nil || opened || !strings.Contains(err.Error(), "provenance recovery required") {
		t.Fatalf("pending provenance journal was accepted: opened=%v err=%v", opened, err)
	}
}
