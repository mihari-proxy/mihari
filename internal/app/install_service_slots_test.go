package app

import (
	"context"
	"errors"
	"testing"
)

type consumingServiceSlots struct {
	afterExchange error
	live          serviceSlotEntry
	slots         map[string]serviceSlotEntry
}

func (s *consumingServiceSlots) Read(_ context.Context, name string) (serviceSlotEntry, error) {
	return s.slots[name], nil
}
func (s *consumingServiceSlots) Publish(_ context.Context, name string) error {
	s.live = s.slots[name]
	delete(s.slots, name)
	return nil
}
func (s *consumingServiceSlots) Exchange(_ context.Context, name string) error {
	s.live, s.slots[name] = s.slots[name], s.live
	return s.afterExchange
}
func (s *consumingServiceSlots) Retain(_ context.Context, name string) error {
	s.slots[name] = s.live
	s.live = serviceSlotEntry{}
	return nil
}
func TestServiceObjectSlots_RepeatedInterruptedSourceRecovery(t *testing.T) {
	versions := []serviceObjectVersion{{Name: "original", State: "old", Identity: "original-inode"}, {Name: "restore", State: "old", Identity: "restore-inode"}, {Name: "mask", State: "mask", Identity: "mask-inode"}}
	slots := &consumingServiceSlots{live: serviceSlotEntry{Present: true, State: "old", Identity: "original-inode"}, slots: map[string]serviceSlotEntry{"restore": {Present: true, State: "old", Identity: "restore-inode"}, "mask": {Present: true, State: "mask", Identity: "mask-inode"}}}
	// Each recovery may fail after restoring the old unit but before restart or
	// completion. Re-enter the same barrier without manufacturing new candidates.
	for retry := 0; retry < 20; retry++ {
		for _, want := range []string{"mask", "old"} {
			if err := publishServiceObject(context.Background(), slots, versions, slots.live, want, "boot", "boot"); err != nil {
				t.Fatalf("recovery %d cannot publish %s: %v", retry, want, err)
			}
			if slots.live.State != want || !serviceObjectMatches(versions, slots.live.State, slots.live.Identity, "boot", "boot") {
				t.Fatal("lost recorded service object")
			}
		}
	}
	if err := publishServiceObject(context.Background(), slots, versions, slots.live, "absent", "boot", "boot"); err != nil {
		t.Fatal("cannot retain removed definition", err)
	}
	if slots.live.Present {
		t.Fatal("definition removal did not take effect")
	}
	if err := publishServiceObject(context.Background(), slots, versions, slots.live, "old", "boot", "boot"); err != nil {
		t.Fatal("cannot undo removal", err)
	}
}
func TestServiceObjectSlots_RejectsForeignCandidateIdentity(t *testing.T) {
	versions := []serviceObjectVersion{{Name: "mask", State: "mask", Identity: "known"}}
	slots := &consumingServiceSlots{slots: map[string]serviceSlotEntry{"mask": {Present: true, State: "mask", Identity: "foreign"}}}
	if err := publishServiceObject(context.Background(), slots, versions, slots.live, "mask", "boot", "boot"); err == nil {
		t.Fatal("same-byte foreign candidate accepted")
	}
}

func TestServiceObjectSlots_ExchangeErrorRetainsPublishedTruth(t *testing.T) {
	failure := errors.New("sync after exchange")
	versions := []serviceObjectVersion{{Name: "original", State: "old", Identity: "old-inode"}, {Name: "mask", State: "mask", Identity: "mask-inode"}}
	slots := &consumingServiceSlots{afterExchange: failure, live: serviceSlotEntry{Present: true, State: "old", Identity: "old-inode"}, slots: map[string]serviceSlotEntry{"mask": {Present: true, State: "mask", Identity: "mask-inode"}}}
	if err := publishServiceObject(context.Background(), slots, versions, slots.live, "mask", "boot", "boot"); !errors.Is(err, failure) {
		t.Fatal("post-exchange error lost", err)
	}
	if slots.live.State != "mask" || slots.slots["mask"].Identity != "old-inode" {
		t.Fatal("exchange publication or retained original lost")
	}
	if err := publishServiceObject(context.Background(), slots, versions, slots.live, "mask", "boot", "boot"); err != nil {
		t.Fatal("published state not recognized after interruption", err)
	}
	slots.afterExchange = nil
	if err := publishServiceObject(context.Background(), slots, versions, slots.live, "old", "boot", "boot"); err != nil {
		t.Fatal("retained original not reusable", err)
	}
}
