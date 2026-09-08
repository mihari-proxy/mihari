//go:build windows

package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsRuntimeControl_ReadOnlyMissingRecordDoesNotCreate(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := installation.Close(); err != nil {
		t.Fatal(err)
	}

	control, err := openWindowsRuntimeControl(context.Background(), true, nil, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	if raw, digest, err := control.Read(context.Background()); !errors.Is(err, os.ErrNotExist) || raw != nil || digest != "" {
		t.Fatalf("Read raw=%q digest=%q err=%v", raw, digest, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Mihari", installControlDirName, windowsRuntimeStateName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only access created runtime record: %v", err)
	}
}

func TestWindowsRuntimeControl_MissingControlDiffersFromMissingRuntimeRecord(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, err := openWindowsRuntimeControl(context.Background(), true, nil, fixture.deps)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing K error=%v", err)
	}
}

func TestWindowsRuntimeControl_PublishCreateReplaceAndConfirm(t *testing.T) {
	control, gate, _ := newWindowsRuntimeControlTest(t)
	ctx := context.Background()
	one := []byte(`{"generation":"one"}`)
	publication, err := control.Publish(ctx, "", one)
	if err != nil || !publication.Published || !publication.Durable {
		t.Fatalf("initial publication=%+v err=%v", publication, err)
	}
	raw, digest, err := control.Read(ctx)
	if err != nil || !bytes.Equal(raw, one) || digest != installStateSHA256(one) {
		t.Fatalf("Read raw=%q digest=%q err=%v", raw, digest, err)
	}
	if publication, err := control.Publish(ctx, "", []byte(`{"generation":"stale"}`)); !errors.Is(err, ErrInstallStateChanged) || publication.Published {
		t.Fatalf("empty predecessor replay publication=%+v err=%v", publication, err)
	}
	two := []byte(`{"generation":"two"}`)
	publication, err = control.Publish(ctx, digest, two)
	if err != nil || !publication.Published || !publication.Durable {
		t.Fatalf("replacement publication=%+v err=%v", publication, err)
	}
	if err := control.Confirm(ctx, installStateSHA256(two)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := gate.CheckWindowsStartupGate(); err != nil {
		t.Fatalf("runtime publication consumed startup gate: %v", err)
	}
}

func TestWindowsRuntimeControl_ReadOnlyConfirmDoesNotFlush(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, installation.Close)
	gate, err := installation.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	writable, err := openWindowsRuntimeControl(context.Background(), false, gate, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, writable.Close)
	raw := []byte(`{"generation":"one"}`)
	if publication, err := writable.Publish(context.Background(), "", raw); err != nil || !publication.Durable {
		t.Fatalf("Publish publication=%+v err=%v", publication, err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}

	flushCalls, syncCalls := 0, 0
	readDeps := fixture.deps
	readDeps.flushFile = func(windows.Handle) error {
		flushCalls++
		return nil
	}
	readDeps.syncVolume = func(windows.Handle) error {
		syncCalls++
		return nil
	}
	readOnly, err := openWindowsRuntimeControl(context.Background(), true, nil, readDeps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, readOnly.Close)
	if err := readOnly.Confirm(context.Background(), installStateSHA256(raw)); !errors.Is(err, ErrInstallControlReadOnly) {
		t.Fatalf("read-only Confirm error=%v", err)
	}
	if flushCalls != 0 || syncCalls != 0 {
		t.Fatalf("read-only Confirm flushed state: file=%d volume=%d", flushCalls, syncCalls)
	}
}

func TestWindowsRuntimeControl_ConfirmRequiresLiveStartupGateBeforeFlush(t *testing.T) {
	control, gate, _ := newWindowsRuntimeControlTest(t)
	raw := []byte(`{"generation":"one"}`)
	if publication, err := control.Publish(context.Background(), "", raw); err != nil || !publication.Durable {
		t.Fatalf("Publish publication=%+v err=%v", publication, err)
	}
	flushCalls, syncCalls := 0, 0
	control.backend.deps.flushFile = func(windows.Handle) error {
		flushCalls++
		return nil
	}
	control.backend.deps.syncVolume = func(windows.Handle) error {
		syncCalls++
		return nil
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	if err := control.Confirm(context.Background(), installStateSHA256(raw)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Confirm with closed startup gate error=%v", err)
	}
	if flushCalls != 0 || syncCalls != 0 {
		t.Fatalf("Confirm with closed startup gate flushed state: file=%d volume=%d", flushCalls, syncCalls)
	}
}

func TestWindowsRuntimeControl_EnforcesRawFourKiBBound(t *testing.T) {
	control, _, _ := newWindowsRuntimeControlTest(t)
	exact := bytes.Repeat([]byte{' '}, windowsRuntimeStateMaxBytes)
	publication, err := control.Publish(context.Background(), "", exact)
	if err != nil || !publication.Durable {
		t.Fatalf("exact-bound publication=%+v err=%v", publication, err)
	}
	digest := installStateSHA256(exact)
	publication, err = control.Publish(context.Background(), digest, append(exact, ' '))
	if !errors.Is(err, ErrWindowsRuntimeStateTooLarge) || publication.Published {
		t.Fatalf("oversize publication=%+v err=%v", publication, err)
	}
	raw, _, err := control.Read(context.Background())
	if err != nil || len(raw) != windowsRuntimeStateMaxBytes {
		t.Fatalf("retained runtime size=%d err=%v", len(raw), err)
	}
}

func TestWindowsRuntimeControl_ReadRejectsOversizeRawFileIncludingWhitespace(t *testing.T) {
	control, _, _ := newWindowsRuntimeControlTest(t)
	if _, err := control.Publish(context.Background(), "", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeInstallControlWindowsFile(control.runtime, bytes.Repeat([]byte{' '}, windowsRuntimeStateMaxBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.Read(context.Background()); !errors.Is(err, ErrWindowsRuntimeStateTooLarge) {
		t.Fatalf("oversize Read error=%v", err)
	}
}

func TestWindowsRuntimeControl_PublishRequiresWritableStoreAndBoundStartupGate(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, installation.Close)
	startup, err := installation.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, startup.Close)
	operation, err := installation.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)

	readOnly, err := openWindowsRuntimeControl(context.Background(), true, nil, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, readOnly.Close)
	if _, err := readOnly.Publish(context.Background(), "", []byte(`{}`)); !errors.Is(err, ErrInstallControlReadOnly) {
		t.Fatalf("read-only Publish error=%v", err)
	}
	if c, err := openWindowsRuntimeControl(context.Background(), false, nil, fixture.deps); c != nil || !errors.Is(err, os.ErrClosed) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("nil-gate control=%v err=%v", c, err)
	}
	if c, err := openWindowsRuntimeControl(context.Background(), false, operation, fixture.deps); c != nil || !errors.Is(err, os.ErrPermission) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("operation-gate control=%v err=%v", c, err)
	}
	if err := startup.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := openWindowsRuntimeControl(context.Background(), false, startup, fixture.deps); c != nil || !errors.Is(err, os.ErrClosed) {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("closed-gate control=%v err=%v", c, err)
	}
}

func TestWindowsRuntimeControl_RejectsStartupGateFromAnotherControlRoot(t *testing.T) {
	leftRoot := t.TempDir()
	leftFixture := installControlWindowsTestDeps(leftRoot)
	left, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), leftFixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, left.Close)

	rightRoot := t.TempDir()
	rightFixture := installControlWindowsTestDeps(rightRoot)
	right, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), rightFixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, right.Close)
	rightGate, err := right.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, rightGate.Close)

	control, err := openWindowsRuntimeControl(context.Background(), false, rightGate, leftFixture.deps)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, ErrInstallStateChanged) {
		t.Fatalf("foreign startup gate error=%v", err)
	}
}

func TestWindowsRuntimeControl_PublishKeepsGateAndStoreOpenUntilIOFinishes(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, installation.Close)
	gate, err := installation.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	control, err := openWindowsRuntimeControl(context.Background(), false, gate, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.flushFileState.mu.Lock()
	fixture.flushFileState.onCall = func() {
		close(entered)
		<-release
	}
	fixture.flushFileState.mu.Unlock()
	published := make(chan error, 1)
	go func() {
		_, err := control.Publish(context.Background(), "", []byte(`{}`))
		published <- err
	}()
	<-entered
	gateClosed := make(chan error, 1)
	storeClosed := make(chan error, 1)
	go func() { gateClosed <- gate.Close() }()
	go func() { storeClosed <- control.Close() }()
	select {
	case err := <-gateClosed:
		t.Fatalf("startup gate closed during publication: %v", err)
	case err := <-storeClosed:
		t.Fatalf("runtime store closed during publication: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := <-gateClosed; err != nil {
		t.Fatalf("gate Close: %v", err)
	}
	if err := <-storeClosed; err != nil {
		t.Fatalf("store Close: %v", err)
	}
}

func TestWindowsRuntimeControl_PublishCannotStartAfterGateOrStoreClose(t *testing.T) {
	t.Run("gate closed", func(t *testing.T) {
		control, gate, root := newWindowsRuntimeControlTest(t)
		if err := gate.Close(); err != nil {
			t.Fatal(err)
		}
		publication, err := control.Publish(context.Background(), "", []byte(`{}`))
		if !errors.Is(err, os.ErrClosed) || publication.Published {
			t.Fatalf("publication=%+v err=%v", publication, err)
		}
		assertWindowsRuntimeMissing(t, root)
	})

	t.Run("store closed", func(t *testing.T) {
		control, _, root := newWindowsRuntimeControlTest(t)
		if err := control.Close(); err != nil {
			t.Fatal(err)
		}
		publication, err := control.Publish(context.Background(), "", []byte(`{}`))
		if !errors.Is(err, os.ErrClosed) || publication.Published {
			t.Fatalf("publication=%+v err=%v", publication, err)
		}
		assertWindowsRuntimeMissing(t, root)
	})
}

func TestWindowsRuntimeControl_PublishedButNotDurableRemainsReadable(t *testing.T) {
	control, _, _ := newWindowsRuntimeControlTest(t)
	backend := control.install.platform.(*windowsInstallControl)
	syncErr := errors.New("injected runtime volume sync failure")
	backend.deps.syncVolume = func(windows.Handle) error { return syncErr }
	raw := []byte(`{"generation":"visible"}`)
	publication, err := control.Publish(context.Background(), "", raw)
	if !errors.Is(err, syncErr) || !publication.Published || publication.Durable {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
	got, _, readErr := control.Read(context.Background())
	if readErr != nil || !bytes.Equal(got, raw) {
		t.Fatalf("visible runtime=%q err=%v", got, readErr)
	}
}

func TestWindowsRuntimeControl_RevalidatesRuntimeIdentityAndSecurity(t *testing.T) {
	t.Run("identity", func(t *testing.T) {
		control, _, root := newWindowsRuntimeControlTest(t)
		if _, err := control.Publish(context.Background(), "", []byte(`{"generation":"held"}`)); err != nil {
			t.Fatal(err)
		}
		backend := control.install.platform.(*windowsInstallControl)
		replacement, err := openRelative(backend.root, "runtime-replacement.json", installControlWindowsFileAccess, windows.FILE_CREATE,
			windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			windows.FILE_ATTRIBUTE_NORMAL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeInstallControlWindowsFile(replacement, []byte(`{"generation":"attacker"}`)); err != nil {
			_ = windows.CloseHandle(replacement)
			t.Fatal(err)
		}
		if err := backend.deps.rename(replacement, backend.root, windowsRuntimeStateName,
			windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS); err != nil {
			_ = windows.CloseHandle(replacement)
			t.Fatal(err)
		}
		if err := windows.CloseHandle(replacement); err != nil {
			t.Fatal(err)
		}
		if _, _, err := control.Read(context.Background()); !errors.Is(err, ErrInstallStateChanged) {
			t.Fatalf("identity replacement error=%v file=%s", err, filepath.Join(root, windowsRuntimeStateName))
		}
	})

	t.Run("security", func(t *testing.T) {
		root := t.TempDir()
		fixture := installControlWindowsTestDeps(root)
		changed := false
		securityErr := errors.New("injected runtime ACL change")
		fixture.deps.checkSecurity = func(h windows.Handle, directory bool) error {
			if !changed || directory {
				return nil
			}
			path, err := finalPathFromHandle(h)
			if err != nil {
				return err
			}
			if filepath.Base(path) == windowsRuntimeStateName {
				return securityErr
			}
			return nil
		}
		installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
		if err != nil {
			t.Fatal(err)
		}
		defer assertInstallTestClose(t, installation.Close)
		gate, err := installation.LockStartup(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer assertInstallTestClose(t, gate.Close)
		control, err := openWindowsRuntimeControl(context.Background(), false, gate, fixture.deps)
		if err != nil {
			t.Fatal(err)
		}
		defer assertInstallTestClose(t, control.Close)
		if _, err := control.Publish(context.Background(), "", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		changed = true
		if _, _, err := control.Read(context.Background()); !errors.Is(err, securityErr) {
			t.Fatalf("runtime security change error=%v", err)
		}
	})
}

func TestWindowsRuntimeControl_InitialPublicationRaceHasOneWinner(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, installation.Close)
	gate, err := installation.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	one, err := openWindowsRuntimeControl(context.Background(), false, gate, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, one.Close)
	two, err := openWindowsRuntimeControl(context.Background(), false, gate, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, two.Close)

	if publication, err := one.Publish(context.Background(), "", []byte(`{"winner":1}`)); err != nil || !publication.Durable {
		t.Fatalf("first publication=%+v err=%v", publication, err)
	}
	if publication, err := two.Publish(context.Background(), "", []byte(`{"winner":2}`)); !errors.Is(err, ErrInstallStateChanged) || publication.Published {
		t.Fatalf("second publication=%+v err=%v", publication, err)
	}
}

func TestWindowsRuntimeControl_PrepublicationFailureRemovesCandidate(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, installation.Close)
	gate, err := installation.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	renameErr := errors.New("injected runtime rename failure")
	bad := fixture.deps
	bad.rename = func(windows.Handle, windows.Handle, string, uint32) error { return renameErr }
	control, err := openWindowsRuntimeControl(context.Background(), false, gate, bad)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	publication, err := control.Publish(context.Background(), "", []byte(`{}`))
	if !errors.Is(err, renameErr) || publication.Published {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "Mihari", installControlDirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), windowsRuntimeTempPrefix) {
			t.Fatalf("leaked runtime candidate %q", entry.Name())
		}
	}
}

func TestWindowsRuntimeControl_CanceledContextDoesNotPublish(t *testing.T) {
	control, _, root := newWindowsRuntimeControlTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	publication, err := control.Publish(ctx, "", []byte(`{}`))
	if !errors.Is(err, context.Canceled) || publication.Published {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Mihari", installControlDirName, windowsRuntimeStateName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled publish created runtime record: %v", err)
	}
}

func newWindowsRuntimeControlTest(t *testing.T) (*WindowsRuntimeControl, *InstallControlLock, string) {
	t.Helper()
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	installation, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := installation.LockStartup(context.Background())
	if err != nil {
		_ = installation.Close()
		t.Fatal(err)
	}
	control, err := openWindowsRuntimeControl(context.Background(), false, gate, fixture.deps)
	if err != nil {
		_ = gate.Close()
		_ = installation.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = control.Close()
		_ = gate.Close()
		_ = installation.Close()
	})
	return control, gate, root
}

func assertWindowsRuntimeMissing(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "Mihari", installControlDirName, windowsRuntimeStateName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime record exists: %v", err)
	}
}
