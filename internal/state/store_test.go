package state

import (
	"testing"
	"time"
)

func TestStoreReturnsIndependentSnapshots(t *testing.T) {
	started := time.Unix(100, 0).UTC()
	store := NewStore(Snapshot{Revision: 1, Version: "dev", StartedAt: started, Health: "ok"})
	a := store.Load()
	b := store.Load()
	if a != b || a.Revision != 1 {
		t.Fatalf("unexpected snapshots: %#v %#v", a, b)
	}
}
