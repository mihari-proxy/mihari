//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlySource_StatRetainsIdentityAndBounds(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "candidate")
	if err := os.WriteFile(file, []byte("candidate bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := OpenReadOnlySource(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { assertTestClose(t, source.Close) })
	want, raw, err := source.Read(ctx, "candidate", 128)
	if err != nil || string(raw) != "candidate bytes" {
		t.Fatalf("source read: %v", err)
	}
	got, err := source.Stat(ctx, "candidate", 128)
	if err != nil || got != want {
		t.Fatalf("streamed metadata differs: got=%+v want=%+v err=%v", got, want, err)
	}
	oversize, err := source.Stat(ctx, "candidate", 4)
	if !errors.Is(err, os.ErrInvalid) || oversize.Kind != "file" || oversize.Size != int64(len(raw)) {
		t.Fatalf("oversize lost its metadata: %+v err=%v", oversize, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := source.Stat(cancelled, "candidate", 128); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
	source.backend = sourceReplaceOnOpen{trustedBackend: source.backend, replace: func() {
		if err := os.Rename(file, file+"-old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := source.Stat(ctx, "candidate", 128); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("accepted replaced source file: %v", err)
	}
}
