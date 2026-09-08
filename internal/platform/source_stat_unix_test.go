//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

type sourceGrowAfterStat struct {
	trustedBackend
	grow func()
}

func (b *sourceGrowAfterStat) stat(fd int) (trustedNode, error) {
	node, err := b.trustedBackend.stat(fd)
	if err == nil && node.mode&unix.S_IFMT == unix.S_IFREG && b.grow != nil {
		grow := b.grow
		b.grow = nil
		grow()
	}
	return node, err
}

func TestReadOnlySource_GrowthPastReadBoundIsOversize(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(map[bool]string{false: "stat", true: "read"}[retain], func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, "candidate")
			if err := os.WriteFile(file, []byte("1234"), 0600); err != nil {
				t.Fatal(err)
			}
			source, err := OpenReadOnlySource(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { assertTestClose(t, source.Close) })
			source.backend = &sourceGrowAfterStat{trustedBackend: source.backend, grow: func() {
				if err := os.WriteFile(file, []byte("12345678"), 0600); err != nil {
					t.Fatal(err)
				}
			}}
			var entry SourceEntry
			var raw []byte
			if retain {
				entry, raw, err = source.Read(context.Background(), "candidate", 5)
			} else {
				entry, err = source.Stat(context.Background(), "candidate", 5)
			}
			if !errors.Is(err, os.ErrInvalid) || entry.Kind != "file" || entry.Size != 6 || raw != nil || entry.SHA256 != "" {
				t.Fatalf("growth lost oversize classification: size=%d err=%v", entry.Size, err)
			}
		})
	}
}

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
