//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestInstallControl_WindowsReadOnlyDoesNotCreate(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, err := openWindowsInstallControl(context.Background(), true, fixture.deps)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open error=%v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("read-only open created entries=%v err=%v", entries, readErr)
	}
}

func TestInstallControl_WindowsInitializationPublishesFixedCompleteDirectory(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, publication, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = control.Close() })
	if !publication.Published || !publication.Durable {
		t.Fatalf("publication=%+v", publication)
	}
	dir := filepath.Join(root, "Mihari", installControlDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{installOperationLockName, installStartupLockName, installStateName}
	if len(names) != len(want) {
		t.Fatalf("fixed entries=%v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("fixed entries=%v want %v", names, want)
		}
	}
	state, _, err := control.ReadState(context.Background())
	if err != nil || string(state) != `{"state":"complete"}` {
		t.Fatalf("state=%q err=%v", state, err)
	}
}

func TestInstallControl_WindowsInitializationRaceReopensPublishedLocks(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	start := make(chan struct{})
	type result struct {
		control     *InstallControl
		publication InstallPublication
		err         error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			control, publication, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
			results <- result{control: control, publication: publication, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var controls []*InstallControl
	published := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("initialize: %v", result.err)
		}
		controls = append(controls, result.control)
		if result.publication.Published {
			published++
		}
	}
	defer func() {
		for _, control := range controls {
			_ = control.Close()
		}
	}()
	if published != 1 {
		t.Fatalf("published initial directories=%d", published)
	}
	lock, err := controls[0].LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, lock.Close)
	if _, err := controls[1].LockOperation(context.Background()); !errors.Is(err, ErrInstallControlBusy) {
		t.Fatalf("second operation lock error=%v", err)
	}
}

func TestInstallControl_WindowsPermanentLockProbeAndStartupKind(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	lock, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, lock.Close)
	reader, err := openWindowsInstallControl(context.Background(), true, fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, reader.Close)
	locked, err := reader.ProbeOperation(context.Background())
	if err != nil || !locked {
		t.Fatalf("ProbeOperation locked=%v err=%v", locked, err)
	}
	startup, err := control.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := startup.CheckWindowsStartupGate(); err != nil {
		t.Fatalf("startup gate: %v", err)
	}
	if err := startup.Close(); err != nil {
		t.Fatal(err)
	}
	if err := startup.CheckWindowsStartupGate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed startup gate error=%v", err)
	}
}

func TestInstallControl_WindowsPublishedButVolumeSyncFailed(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"generation":1}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	_, previous, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("injected volume sync failure")
	fixture.syncVolumeState.err = syncErr
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	publication, err := control.PublishState(context.Background(), previous, []byte(`{"generation":2}`))
	if !errors.Is(err, syncErr) || !publication.Published || publication.Durable {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
	state, _, readErr := control.ReadState(context.Background())
	if readErr != nil || string(state) != `{"generation":2}` {
		t.Fatalf("visible state=%q err=%v", state, readErr)
	}
	if err := control.ConfirmState(context.Background(), installStateSHA256(state)); !errors.Is(err, syncErr) {
		t.Fatalf("ConfirmState error=%v", err)
	}
}

func TestInstallControl_WindowsInitializationRejectsUnsupportedVolumeBeforePublication(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	volumeErr := errors.New("injected unsupported installation control volume")
	fixture.deps.checkVolume = func(windows.Handle) error { return volumeErr }
	control, publication, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, volumeErr) || publication.Published || publication.Durable {
		t.Fatalf("control=%v publication=%+v err=%v", control, publication, err)
	}
	_, statErr := os.Stat(filepath.Join(root, "Mihari", installControlDirName))
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsupported volume published installation control: %v", statErr)
	}
}

func TestInstallControl_WindowsPublishedButTargetFileSyncFailed(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"generation":1}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	_, previous, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flushErr := errors.New("injected published file sync failure")
	fixture.flushFileState.mu.Lock()
	fixture.flushFileState.err = flushErr
	fixture.flushFileState.successes = 1
	fixture.flushFileState.mu.Unlock()
	publication, err := control.PublishState(context.Background(), previous, []byte(`{"generation":2}`))
	if !errors.Is(err, flushErr) || !publication.Published || publication.Durable {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
}

func TestInstallControl_WindowsArchiveFailurePreservesCurrentState(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"generation":1}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	state, digest, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flushErr := errors.New("injected archive flush failure")
	fixture.flushFileState.err = flushErr
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	if err := control.ArchiveState(context.Background(), digest); !errors.Is(err, flushErr) {
		t.Fatalf("ArchiveState error=%v", err)
	}
	got, _, err := control.ReadState(context.Background())
	if err != nil || string(got) != string(state) {
		t.Fatalf("state after archive failure=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "Mihari", installControlDirName, installPreviousStateName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous-state exists after prepublication failure: %v", err)
	}
}

func TestInstallControl_WindowsExplicitArchiveRemainsOriginalAcrossPublishes(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"generation":1}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	_, digest1, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := control.ArchiveState(context.Background(), digest1); err != nil {
		t.Fatal(err)
	}
	if publication, err := control.PublishState(context.Background(), digest1, []byte(`{"generation":2}`)); err != nil || !publication.Durable {
		t.Fatalf("publish generation 2 publication=%+v err=%v", publication, err)
	}
	_, digest2, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if publication, err := control.PublishState(context.Background(), digest2, []byte(`{"generation":3}`)); err != nil || !publication.Durable {
		t.Fatalf("publish generation 3 publication=%+v err=%v", publication, err)
	}
	previous, err := os.ReadFile(filepath.Join(root, "Mihari", installControlDirName, installPreviousStateName))
	if err != nil || string(previous) != `{"generation":1}` {
		t.Fatalf("previous-state=%q err=%v", previous, err)
	}
}

func TestInstallControl_WindowsRechecksPredecessorHashBeforeRename(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"generation":1}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	_, digest, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	backend := control.platform.(*windowsInstallControl)
	var mutationErr error
	fixture.flushFileState.mu.Lock()
	fixture.flushFileState.onCall = func() {
		mutationErr = writeInstallControlWindowsFile(backend.state, []byte(`{"generation":9}`))
	}
	fixture.flushFileState.mu.Unlock()
	publication, err := control.PublishState(context.Background(), digest, []byte(`{"generation":2}`))
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}
	if !errors.Is(err, ErrInstallStateChanged) || publication.Published {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
}

func TestInstallControl_WindowsRejectsStateIdentityReplacement(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"generation":1}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	_, digest, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	dir := filepath.Join(root, "Mihari", installControlDirName)
	backend := control.platform.(*windowsInstallControl)
	replacement, err := openRelative(backend.root, "replacement.json", installControlWindowsFileAccess, windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_NORMAL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInstallControlWindowsFile(replacement, []byte(`{"attacker":true}`)); err != nil {
		_ = windows.CloseHandle(replacement)
		t.Fatal(err)
	}
	if err := fixture.deps.rename(replacement, backend.root, installStateName,
		windows.FILE_RENAME_REPLACE_IF_EXISTS|windows.FILE_RENAME_POSIX_SEMANTICS); err != nil {
		_ = windows.CloseHandle(replacement)
		t.Fatal(err)
	}
	if err := windows.CloseHandle(replacement); err != nil {
		t.Fatal(err)
	}
	publication, err := control.PublishState(context.Background(), digest, []byte(`{"generation":2}`))
	if !errors.Is(err, ErrInstallStateChanged) || publication.Published {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, installStateName))
	if err != nil || string(got) != `{"attacker":true}` {
		t.Fatalf("replacement changed=%q err=%v", got, err)
	}
}

func TestInstallControl_WindowsExistingDirectoryWithoutStateIsUninitialized(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Mihari", installControlDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, ErrInstallControlUninitialized) {
		t.Fatalf("initialize error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, installStateName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("initializer repaired existing K: %v", statErr)
	}
}

func TestInstallControl_WindowsRejectsUntrustedFixedFileSecurity(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	securityErr := errors.New("injected untrusted DACL")
	bad := fixture.deps
	bad.checkSecurity = func(h windows.Handle, directory bool) error {
		path, err := finalPathFromHandle(h)
		if err != nil {
			return err
		}
		if !directory && filepath.Base(path) == installStateName {
			return securityErr
		}
		return nil
	}
	opened, err := openWindowsInstallControl(context.Background(), true, bad)
	if opened != nil {
		_ = opened.Close()
	}
	if !errors.Is(err, securityErr) {
		t.Fatalf("open untrusted state error=%v", err)
	}
}

func TestInstallControl_WindowsRejectsOversizeHeldState(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	backend := control.platform.(*windowsInstallControl)
	h, _, err := backend.openFixedFile(installStateName)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeInstallControlWindowsFile(h, make([]byte, installStateMaxBytes+1)); err != nil {
		_ = windows.CloseHandle(h)
		t.Fatal(err)
	}
	if err := windows.CloseHandle(h); err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.ReadState(context.Background()); !errors.Is(err, ErrInstallStateTooLarge) {
		t.Fatalf("ReadState oversize error=%v", err)
	}
}

func TestInstallControl_WindowsRejectsHardLinkedFixedFile(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Mihari", installControlDirName)
	if err := os.Link(filepath.Join(dir, installStateName), filepath.Join(dir, "state-alias.json")); err != nil {
		t.Fatal(err)
	}
	opened, err := openWindowsInstallControl(context.Background(), true, fixture.deps)
	if opened != nil {
		_ = opened.Close()
	}
	if !errors.Is(err, ErrUnsafeComponent) {
		t.Fatalf("open hard-linked state error=%v", err)
	}
}

func TestInstallControl_WindowsStartupLockOwnsIndependentHandles(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := control.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if err := startup.CheckWindowsStartupGate(); err != nil {
		t.Fatalf("startup gate after parent Close: %v", err)
	}
	if err := startup.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallControl_WindowsStartupGatePreventsNamespaceReplacementUntilClose(t *testing.T) {
	tests := []struct {
		name       string
		parentPath func(string) string
		target     string
	}{
		{name: "control directory", parentPath: func(root string) string { return filepath.Join(root, "Mihari") }, target: installControlDirName},
		{name: "Mihari ancestor", parentPath: func(root string) string { return root }, target: "Mihari"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			fixture := installControlWindowsTestDeps(root)
			control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
			if err != nil {
				t.Fatal(err)
			}
			startup, err := control.LockStartup(context.Background())
			if err != nil {
				_ = control.Close()
				t.Fatal(err)
			}
			defer assertInstallTestClose(t, startup.Close)
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			parent, err := openNTPath(tc.parentPath(root), installControlWindowsDirAccess, windows.FILE_OPEN,
				windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
				windows.FILE_ATTRIBUTE_DIRECTORY, nil)
			if err != nil {
				t.Fatal(err)
			}
			retired, err := openRelative(parent, tc.target, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
				windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
				windows.FILE_ATTRIBUTE_DIRECTORY, nil)
			if err != nil {
				_ = windows.CloseHandle(parent)
				t.Fatal(err)
			}
			retiredName := "retired-" + tc.target
			if err := renameHandle(retired, parent, retiredName, windows.FILE_RENAME_POSIX_SEMANTICS); !errors.Is(err, windows.STATUS_ACCESS_DENIED) && !errors.Is(err, windows.STATUS_SHARING_VIOLATION) {
				_ = windows.CloseHandle(retired)
				_ = windows.CloseHandle(parent)
				t.Fatalf("rename while startup gate held error=%v", err)
			}
			if err := startup.Close(); err != nil {
				_ = windows.CloseHandle(retired)
				_ = windows.CloseHandle(parent)
				t.Fatal(err)
			}
			if err := renameHandle(retired, parent, retiredName, windows.FILE_RENAME_POSIX_SEMANTICS); err != nil {
				_ = windows.CloseHandle(retired)
				_ = windows.CloseHandle(parent)
				t.Fatalf("rename after startup gate close: %v", err)
			}
			if err := errors.Join(windows.CloseHandle(retired), windows.CloseHandle(parent)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallControl_WindowsStartupGateRechecksProtectedSecurity(t *testing.T) {
	root := t.TempDir()
	fixture := installControlWindowsTestDeps(root)
	securityErr := errors.New("injected changed security descriptor")
	changed := false
	fixture.deps.checkSecurity = func(windows.Handle, bool) error {
		if changed {
			return securityErr
		}
		return nil
	}
	control, _, err := initializeWindowsInstallControl(context.Background(), []byte(`{"state":"complete"}`), fixture.deps)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := control.LockStartup(context.Background())
	if err != nil {
		_ = control.Close()
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, startup.Close)
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	changed = true
	if err := startup.CheckWindowsStartupGate(); !errors.Is(err, securityErr) {
		t.Fatalf("startup gate security change error=%v", err)
	}
}

func TestInstallControl_WindowsNativeSecurityPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := openNTPath(path, installControlWindowsFileAccess|windows.WRITE_OWNER, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_NORMAL, nil)
	if err != nil {
		t.Fatal(err)
	}
	user, err := currentUserSID()
	if err != nil {
		_ = windows.CloseHandle(h)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hardenHandle(h, principalSDDL(user, false))
		_ = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil)
		_ = windows.CloseHandle(h)
	})
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil); err != nil {
		t.Fatalf("set untrusted test owner: %v", err)
	}
	if err := hardenInstallControlWindowsSecurity(h, false); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_OWNER) {
			t.Skipf("current token cannot assign the protected Administrators owner: %v", err)
		}
		t.Fatal(err)
	}
	if err := checkInstallControlWindowsSecurity(h, false); err != nil {
		t.Fatalf("canonical SYSTEM/Administrators DACL rejected: %v", err)
	}
}

func TestInstallControl_WindowsSecurityDescriptorRequiresProtectedOwner(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%t", directory), func(t *testing.T) {
			protected, err := windows.SecurityDescriptorFromString(installControlWindowsSDDL(directory))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateInstallControlWindowsSecurityDescriptor(protected, directory); err != nil {
				t.Fatalf("canonical protected descriptor: %v", err)
			}
			inherit := ""
			if directory {
				inherit = "OICI"
			}
			unowned, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)")
			if err != nil {
				t.Fatal(err)
			}
			if err := validateInstallControlWindowsSecurityDescriptor(unowned, directory); err == nil {
				t.Fatal("security policy accepted a descriptor without a protected owner")
			}
		})
	}
}

func TestInstallControl_WindowsDirectoryHandleCanReadSecurityDescriptor(t *testing.T) {
	h, err := openNTPath(t.TempDir(), installControlWindowsDirAccess, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_DIRECTORY, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if _, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION); err != nil {
		t.Fatalf("read directory owner and DACL: %v", err)
	}
}

func TestInstallControl_WindowsSecurityPolicyRejectsUntrustedOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := openNTPath(path, installControlWindowsFileAccess|windows.WRITE_OWNER, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_NORMAL, nil)
	if err != nil {
		t.Fatal(err)
	}
	user, err := currentUserSID()
	if err != nil {
		_ = windows.CloseHandle(h)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hardenHandle(h, principalSDDL(user, false))
		_ = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil)
		_ = windows.CloseHandle(h)
	})
	if err := windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil); err != nil {
		t.Fatalf("set untrusted test owner: %v", err)
	}
	if err := hardenHandle(h, installControlWindowsSDDL(false)); err != nil {
		t.Fatalf("set otherwise canonical DACL: %v", err)
	}
	if err := checkInstallControlWindowsSecurity(h, false); err == nil {
		t.Fatal("security policy accepted an untrusted owner")
	}
}

type installControlWindowsTestErrorState struct {
	mu        sync.Mutex
	err       error
	successes int
	onCall    func()
}

func (s *installControlWindowsTestErrorState) get() error {
	s.mu.Lock()
	onCall := s.onCall
	s.onCall = nil
	err := s.err
	if s.err != nil && s.successes > 0 {
		s.successes--
		err = nil
	}
	s.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	return err
}

type installControlWindowsTestFixture struct {
	deps            installControlWindowsDeps
	flushFileState  *installControlWindowsTestErrorState
	syncVolumeState *installControlWindowsTestErrorState
}

func installControlWindowsTestDeps(root string) installControlWindowsTestFixture {
	flushFileState := &installControlWindowsTestErrorState{}
	syncVolumeState := &installControlWindowsTestErrorState{}
	deps := installControlWindowsDeps{
		knownFolderPath: func() (string, error) { return root, nil },
		descriptor:      func(bool) (*windows.SECURITY_DESCRIPTOR, error) { return nil, nil },
		checkSecurity:   func(windows.Handle, bool) error { return nil },
		hardenSecurity:  func(windows.Handle, bool) error { return nil },
		checkVolume:     func(windows.Handle) error { return nil },
		flushFile:       func(windows.Handle) error { return flushFileState.get() },
		syncVolume:      func(windows.Handle) error { return syncVolumeState.get() },
		rename:          renameHandle,
	}
	return installControlWindowsTestFixture{deps: deps, flushFileState: flushFileState, syncVolumeState: syncVolumeState}
}
