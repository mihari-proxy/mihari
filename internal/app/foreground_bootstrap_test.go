package app

import (
	"context"
	"errors"
	"testing"
)

func TestUnixBootstrap_ProductionLifecycle(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "system", true: "root-private"}[private], func(t *testing.T) {
			h := newInstallHarness(t, InstallDataCreate)
			h.tx.Service = nil
			h.tx.Effects = nil
			h.tx.Artifacts.Source = ""
			h.tx.Artifacts.SourceBytes = nil
			h.tx.Private = private
			created, ran := false, false
			failure := errors.New("target startup failed")
			b := ForegroundBootstrap{Root: true, Transaction: h.tx, DiscoverSource: func(context.Context) (bool, error) {
				if created {
					t.Fatal("source inspection after data creation")
				}
				return false, nil
			}, CreateData: func(context.Context) error { created = true; return nil }, Run: func(_ context.Context, phase string) error {
				ran = true
				if h.lease.held {
					t.Fatal("normal daemon inherited install lock")
				}
				if phase != InstallPhaseActivationCommitted {
					t.Fatal("daemon before durable activation")
				}
				return failure
			}}
			err := b.Start(context.Background())
			if !errors.Is(err, failure) || !created || !ran {
				t.Fatalf("lifecycle did not reach target: err=%v create=%v run=%v", err, created, ran)
			}
			j := h.loadedJournal(t, h.disk)
			if j.RecoveryAuthority != InstallAuthorityTarget {
				t.Fatal("startup failure lost target authority")
			}
		})
	}
}

func TestUnixBootstrap_PreparesRealAuthorityBeforeWAL(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Service = nil
	h.tx.Effects = nil
	prepared := false
	b := ForegroundBootstrap{Root: true, Transaction: h.tx, DiscoverSource: func(context.Context) (bool, error) { return false, nil }, PrepareJournal: func(context.Context, string) error { prepared = true; return errors.New("backup preparation failed") }, CreateData: func(context.Context) error { t.Fatal("data created before durable authority"); return nil }, Run: func(context.Context, string) error { return nil }}
	err := b.Start(context.Background())
	if !prepared || err == nil {
		t.Fatalf("missing authority preparation: prepared=%v err=%v", prepared, err)
	}
}
