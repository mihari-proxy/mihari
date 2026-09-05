package onboarding

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
)

func TestOpen_InitializesAndLoadsOnlyOnboardingState(t *testing.T) {
	tests := []struct {
		name            string
		initialRequired bool
		wantComplete    bool
	}{
		{name: "new installation", initialRequired: true, wantComplete: false},
		{name: "existing installation", initialRequired: false, wantComplete: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "onboarding.json")
			service, err := Open(Options{StatePath: path, InitialSetupRequired: test.initialRequired})
			if err != nil {
				t.Fatal(err)
			}
			if got := service.State().Complete; got != test.wantComplete {
				t.Fatalf("complete=%v want=%v", got, test.wantComplete)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "onboarding.json" {
				t.Fatalf("created entries=%v want only onboarding.json", entries)
			}
			if _, err := service.Update(boolPointer(!test.wantComplete)); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(Options{StatePath: path, InitialSetupRequired: test.initialRequired})
			if err != nil {
				t.Fatal(err)
			}
			if got := reopened.State().Complete; got == test.wantComplete {
				t.Fatalf("reopened complete=%v want=%v", got, !test.wantComplete)
			}
		})
	}
}

func TestUpdate_NilIsNoOp(t *testing.T) {
	var saves int
	service, err := Open(Options{
		StatePath: filepath.Join(t.TempDir(), "onboarding.json"),
		SaveState: func(string, State) (config.CommitResult, error) {
			saves++
			return config.CommitResult{Committed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saves != 1 {
		t.Fatalf("open saves=%d want=1", saves)
	}
	before := service.State()

	got, err := service.Update(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != before || saves != 1 {
		t.Fatalf("state=%#v before=%#v saves=%d want unchanged", got, before, saves)
	}
}

func TestUpdate_PersistsTrueAndFalse(t *testing.T) {
	var saved []State
	service, err := Open(Options{
		StatePath:            filepath.Join(t.TempDir(), "onboarding.json"),
		InitialSetupRequired: true,
		SaveState: func(_ string, state State) (config.CommitResult, error) {
			saved = append(saved, state)
			return config.CommitResult{Committed: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, complete := range []bool{true, false} {
		got, err := service.Update(&complete)
		if err != nil {
			t.Fatal(err)
		}
		if got.Complete != complete || service.State().Complete != complete {
			t.Fatalf("state=%#v service=%#v want complete=%v", got, service.State(), complete)
		}
	}
	if len(saved) != 3 || saved[0].Complete || !saved[1].Complete || saved[2].Complete {
		t.Fatalf("saved states=%#v", saved)
	}
}

func TestUpdate_PreCommitFailureKeepsMemory(t *testing.T) {
	replaceErr := errors.New("replace failed at C:\\sensitive\\onboarding.json")
	var calls int
	service, err := Open(Options{
		StatePath:            filepath.Join(t.TempDir(), "onboarding.json"),
		InitialSetupRequired: true,
		SaveState: func(_ string, _ State) (config.CommitResult, error) {
			calls++
			if calls == 1 {
				return config.CommitResult{Committed: true}, nil
			}
			return config.CommitResult{}, replaceErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := true
	if _, err := service.Update(&complete); !errors.Is(err, replaceErr) {
		t.Fatalf("err=%v want replace failure", err)
	}
	if got := service.State(); got.Complete {
		t.Fatalf("pre-commit failure published state=%#v", got)
	}
}

func TestUpdate_PostCommitWarningPublishesAndReportsStableWarning(t *testing.T) {
	var calls int
	var warnings []error
	service, err := Open(Options{
		StatePath:            filepath.Join(t.TempDir(), "onboarding.json"),
		InitialSetupRequired: true,
		SaveState: func(_ string, _ State) (config.CommitResult, error) {
			calls++
			if calls == 1 {
				return config.CommitResult{Committed: true}, nil
			}
			return config.CommitResult{Committed: true, Warning: errors.New("C:\\sensitive\\onboarding.json")}, nil
		},
		OnPersistenceWarning: func(err error) { warnings = append(warnings, err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	complete := true
	got, err := service.Update(&complete)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || !service.State().Complete {
		t.Fatalf("committed warning did not publish state: got=%#v service=%#v", got, service.State())
	}
	if len(warnings) != 1 || warnings[0].Error() != "onboarding parent directory sync failed after commit" {
		t.Fatalf("warnings=%v", warnings)
	}
}

func boolPointer(value bool) *bool { return &value }
