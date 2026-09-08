package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"testing"
)

type channelMaintenanceFake struct {
	journal        *InstallJournal
	writes, closes int
}

func (f *channelMaintenanceFake) Journal(context.Context) (*InstallJournal, error) {
	return f.journal, nil
}
func (f *channelMaintenanceFake) Write(context.Context, string) error { f.writes++; return nil }
func (f *channelMaintenanceFake) Close() error                        { f.closes++; return nil }
func TestChannelMaintenance_RejectsUnauthorizedAndPending(t *testing.T) {
	for _, test := range []struct {
		name, value string
		uid         uint32
		pending     bool
	}{{"ordinary system", "dev", 1000, false}, {"invalid value", "../dev", 0, false}, {"pending install", "dev", 0, true}} {
		t.Run(test.name, func(t *testing.T) {
			f := &channelMaintenanceFake{}
			if test.pending {
				f.journal = &InstallJournal{Phase: InstallPhasePrepared}
			}
			opens := 0
			err := maintainChannel(context.Background(), platform.ResolvedLayout{Mode: platform.SystemMode}, test.uid, test.value, func(context.Context) (channelMaintenance, error) { opens++; return f, nil })
			if err == nil || f.writes != 0 || (!test.pending && opens != 0) {
				t.Fatalf("unsafe channel mutation: err=%v opens=%d writes=%d", err, opens, f.writes)
			}
		})
	}
}

func TestChannelMaintenance_PrivateSystemJournalScope(t *testing.T) {
	layout := platform.ResolvedLayout{Mode: platform.PrivateMode, Data: platform.Paths{Root: "/portable"}}
	for _, tc := range []struct {
		name                  string
		uid                   uint32
		local, system         *InstallJournal
		wantPending, wantRead bool
	}{
		{name: "same-P pending", system: &InstallJournal{Mode: "private", DataRoot: "/portable", Phase: InstallPhasePrepared}, wantPending: true, wantRead: true},
		{name: "same-P activation", system: &InstallJournal{Mode: "private", DataRoot: "/portable", Phase: InstallPhaseActivationCommitted}, wantPending: true, wantRead: true},
		{name: "same-P complete", system: &InstallJournal{Mode: "private", DataRoot: "/portable", Phase: InstallPhaseComplete}, wantRead: true},
		{name: "absent", wantRead: true},
		{name: "other-P", system: &InstallJournal{Mode: "private", DataRoot: "/other", Phase: InstallPhasePrepared}, wantRead: true},
		{name: "ordinary-no-B", uid: 1000, system: &InstallJournal{Mode: "private", DataRoot: "/portable", Phase: InstallPhasePrepared}},
		{name: "local-pending", local: &InstallJournal{Phase: InstallPhasePrepared}, wantPending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := false
			journal, err := relevantChannelJournal(context.Background(), layout, tc.uid, tc.local, func(context.Context) (*InstallJournal, error) { read = true; return tc.system, nil })
			pending := journal != nil && journal.Phase != InstallPhaseComplete
			if err != nil || pending != tc.wantPending || read != tc.wantRead {
				t.Fatalf("pending=%v read-B=%v err=%v", pending, read, err)
			}
		})
	}
}

func TestChannelMaintenance_PrivateSystemReadErrorFailsClosed(t *testing.T) {
	denied := errors.New("unsafe machine journal")
	_, err := relevantChannelJournal(context.Background(), platform.ResolvedLayout{Mode: platform.PrivateMode}, 0, nil, func(context.Context) (*InstallJournal, error) { return nil, denied })
	if !errors.Is(err, denied) {
		t.Fatalf("machine journal read error ignored: %v", err)
	}
}
