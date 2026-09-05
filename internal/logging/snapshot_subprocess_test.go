package logging

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

const snapshotLockChildEnv = "MIHARI_SNAPSHOT_LOCK_CHILD"

func TestSnapshotSource_SubprocessExclusiveCannotPassSharedSnapshot(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.TUILog, "{\"record\":1}\n")
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		handles, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil,
			func(fs *platform.PrivateFS, path string, identity platform.FileIdentity) (*os.File, error) {
				close(entered)
				<-release
				return platform.OpenSnapshot(fs, path, identity)
			})
		if err == nil {
			err = closeSnapshots(handles)
		}
		done <- err
	}()
	<-entered

	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotLockChild$")
	cmd.Env = append(os.Environ(), snapshotLockChildEnv+"=1", "MIHARI_SNAPSHOT_ROOT="+filepath.Dir(paths.LogDir), "MIHARI_SNAPSHOT_BASE="+paths.TUILog)
	output, err := cmd.CombinedOutput()
	if err != nil {
		close(release)
		t.Fatalf("exclusive child: %v\n%s", err, output)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("snapshotSource: %v", err)
	}
}

func TestSnapshotLockChild(t *testing.T) {
	if os.Getenv(snapshotLockChildEnv) != "1" {
		t.Skip("subprocess helper")
	}
	fs, err := platform.NewPrivateFS(filepath.Clean(os.Getenv("MIHARI_SNAPSHOT_ROOT")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer func() { _ = fs.Close() }()
	lock, err := platform.OpenAdvisoryLock(fs, os.Getenv("MIHARI_SNAPSHOT_BASE")+".lock")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer func() { _ = lock.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := lock.Lock(ctx, platform.LockExclusive); err != context.DeadlineExceeded {
		fmt.Fprintf(os.Stderr, "exclusive lock error=%v, want deadline exceeded\n", err)
		os.Exit(2)
	}
}
