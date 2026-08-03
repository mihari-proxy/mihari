package state

import (
	"reflect"
	"testing"
	"time"
)

func TestStoreReturnsIndependentSnapshots(t *testing.T) {
	started := time.Unix(100, 0).UTC()
	store := NewStore(Snapshot{Revision: 1, Version: "dev", StartedAt: started, Health: "ok"})
	a := store.Load()
	b := store.Load()
	if !reflect.DeepEqual(a, b) || a.Revision != 1 {
		t.Fatalf("unexpected snapshots: %#v %#v", a, b)
	}
}

func TestStoreDeepCopiesSubscriptionSlices(t *testing.T) {
	store := NewStore(Snapshot{Subscriptions: []SubscriptionState{{ID: "one", Name: "first"}}})
	a := store.Load()
	a.Subscriptions[0].Name = "changed"
	a.Subscriptions = append(a.Subscriptions, SubscriptionState{ID: "two"})
	b := store.Load()
	if len(b.Subscriptions) != 1 || b.Subscriptions[0].Name != "first" {
		t.Fatalf("stored snapshot was mutated through a reader: %#v", b)
	}
}
