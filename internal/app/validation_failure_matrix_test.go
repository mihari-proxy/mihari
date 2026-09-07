package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type validationFailFiles struct {
	journalFiles
	kind, status string
	fired        bool
}

func (f *validationFailFiles) write(ctx context.Context, name string, raw []byte, old JournalObject) (JournalDurability, error) {
	if name == installJournalFileName && !f.fired {
		var j InstallJournal
		if json.Unmarshal(raw, &j) == nil && len(j.Actions) > 0 {
			a := j.Actions[len(j.Actions)-1]
			if a.Kind == f.kind && a.Status == f.status {
				f.fired = true
				return JournalDurability{}, errors.New("injected validation journal failure")
			}
		}
	}
	return f.journalFiles.write(ctx, name, raw, old)
}
func TestInstallValidation_EveryJournalFailureJoinsChild(t *testing.T) {
	for _, kind := range []string{JournalActionValidationStart, JournalActionValidationStop} {
		for _, status := range []string{JournalActionIntent, JournalActionDone} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				h := newInstallHarness(t, InstallDataCreate)
				files := &validationFailFiles{journalFiles: h.disk, kind: kind, status: status}
				h.tx.Store.files = files
				s := &ownershipSession{}
				h.tx.Validation = ownershipChild{s}
				_, err := h.tx.Apply(context.Background(), h.req)
				if err == nil || !files.fired {
					t.Fatalf("failure stage not exercised: %v", err)
				}
				if kind == JournalActionValidationStart && status == JournalActionIntent {
					if s.ValidationSession != nil {
						t.Fatal("child started before durable intent")
					}
				} else if s.closes != 1 || s.waits != 1 {
					t.Fatalf("child escaped failure cleanup: closes=%d waits=%d", s.closes, s.waits)
				}
				if h.lease.held {
					t.Fatal("installer failed to release joined lease")
				}
				j := h.loadedJournal(t, h.disk)
				if j.RecoveryAuthority == InstallAuthorityTarget {
					t.Fatal("failed validation activated target")
				}
			})
		}
	}
}

type cancelReadySession struct {
	ValidationSession
	cancel context.CancelFunc
}

func (s cancelReadySession) WaitReady(ctx context.Context) error {
	err := s.ValidationSession.WaitReady(ctx)
	s.cancel()
	return err
}

type cancelReadyChild struct{ cancel context.CancelFunc }

func (c cancelReadyChild) Start(ctx context.Context, r ValidationStart) (ValidationSession, error) {
	s, err := (&FakeValidationChild{}).Start(ctx, r)
	return cancelReadySession{s, c.cancel}, err
}
func TestInstallValidation_CancelAfterReadyStillJoins(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.tx.Validation = cancelReadyChild{cancel}
	_, err := h.tx.Apply(ctx, h.req)
	if err == nil {
		t.Fatal("canceled installation activated")
	}
	if h.lease.held || h.tx.validation != nil {
		t.Fatal("cancellation lost owned child or install lease")
	}
}
