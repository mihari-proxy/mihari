//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestUnixLocalOperation_CancellationRemainsCancellation(t *testing.T) {
	layout, err := platform.ResolveLayout(platform.LayoutInput{Data: filepath.Join(t.TempDir(), "private"), Endpoint: "/tmp/m18-unused.sock", EUID: uint32(os.Geteuid())}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	installer, err := NewUnixInstaller(layout, filepath.Join(layout.InstallRoot, "mihari"), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = installer.SetChannel(ctx, "dev")
	var api protocol.APIError
	if !errors.Is(err, context.Canceled) || errors.As(err, &api) {
		t.Fatalf("cancellation relabeled: %v", err)
	}
	if _, err := os.Stat(layout.Data.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled operation created private root: %v", err)
	}
}

func TestUnixLocalOperation_PreservesClassifiedErrorAndCause(t *testing.T) {
	expected := protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "revision changed"}
	original := errors.Join(expected, os.ErrPermission)
	classified := ClassifyUnixLocalError(original)
	var api protocol.APIError
	if classified != original || !errors.As(classified, &api) || api.Code != expected.Code {
		t.Fatalf("classified error overridden: %v", classified)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := ClassifyUnixLocalError(cause); got != cause {
			t.Fatalf("context error changed: %v", got)
		}
	}
}
