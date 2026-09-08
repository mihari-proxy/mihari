package onboarding

import (
	"path/filepath"
	"testing"
)

func TestPreparedUpdate_CheckPreviousRejectsUnknownStateFields(t *testing.T) {
	s, err := Open(Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json"), InitialSetupRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PrepareActivation(s.State(), boolPointer(true))
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.CheckPrevious([]byte(`{"schema":"mihari.onboarding/v1","complete":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := prepared.CheckPrevious([]byte(`{"schema":"mihari.onboarding/v1","complete":false,"external":true}`)); err == nil {
		t.Fatal("unknown persisted field would be silently discarded during activation")
	}
	if s.State().Complete {
		t.Fatal("rejected activation changed service state")
	}
}
