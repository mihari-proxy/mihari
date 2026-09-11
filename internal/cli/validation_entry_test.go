package cli

import (
	"context"
	"io"
	"testing"
)

func TestInstallValidation_EntrySkipsOrdinaryRootPreparation(t *testing.T) {
	prepared, ran := false, false
	code := Execute(context.Background(), []string{"daemon", "--install-validation", "0123456789abcdef0123456789abcdef"}, io.Discard, io.Discard, Dependencies{PrepareLocalRoot: func() error { prepared = true; return nil }, RunInstallValidation: func(context.Context, string) error { ran = true; return nil }})
	if code != ExitOK || prepared || !ran {
		t.Fatalf("validation entry did data IO before authentication: code=%d prepared=%v ran=%v", code, prepared, ran)
	}
}

func TestInstallValidation_EmptyFlagNeverRunsOrdinaryDaemon(t *testing.T) {
	ran := false
	code := Execute(context.Background(), []string{"daemon", "--install-validation="}, io.Discard, io.Discard, Dependencies{RunDaemon: func(context.Context) error { ran = true; return nil }})
	if code == ExitOK || ran {
		t.Fatalf("empty validation flag started ordinary daemon: code=%d ran=%v", code, ran)
	}
}
