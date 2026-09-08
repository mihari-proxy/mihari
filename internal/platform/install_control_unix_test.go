//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
)

func TestInstallControl_UnixReadOnlyDoesNotCreate(t *testing.T) {
	base := installControlUnixTestRoot(t)
	control, err := openUnixInstallControlAt(context.Background(), base, true, nil)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open error=%v", err)
	}
	names, readErr := base.ReadNames(context.Background())
	if readErr != nil || len(names) != 0 {
		t.Fatalf("read-only open created names=%v err=%v", names, readErr)
	}
}

func TestInstallControl_UnixInitializationRaceUsesPublishedPermanentLock(t *testing.T) {
	base := installControlUnixTestRoot(t)
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
			control, publication, err := initializeUnixInstallControlAt(context.Background(), base, []byte(`{"state":"complete"}`), nil)
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

func TestInstallControl_UnixPublishReportsParentSyncFailureAfterRename(t *testing.T) {
	base := installControlUnixTestRoot(t)
	control, _, err := initializeUnixInstallControlAt(context.Background(), base, []byte(`{"generation":1}`), nil)
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
	syncErr := errors.New("injected parent sync failure")
	backend := control.platform.(*unixInstallControl)
	backend.root.backend = &installControlUnixFaultBackend{trustedBackend: backend.root.backend, syncErr: syncErr}
	publication, err := control.PublishState(context.Background(), previous, []byte(`{"generation":2}`))
	if !errors.Is(err, syncErr) || !publication.Published || publication.Durable {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
	state, _, readErr := control.ReadState(context.Background())
	if readErr != nil || string(state) != `{"generation":2}` {
		t.Fatalf("visible state=%q err=%v", state, readErr)
	}
}

func TestInstallControl_UnixArchiveFailureLeavesStateUnchanged(t *testing.T) {
	base := installControlUnixTestRoot(t)
	control, _, err := initializeUnixInstallControlAt(context.Background(), base, []byte(`{"generation":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	operation, err := control.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	state, digest, err := control.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("injected archive sync failure")
	backend := control.platform.(*unixInstallControl)
	backend.root.backend = &installControlUnixFaultBackend{trustedBackend: backend.root.backend, syncErr: syncErr}
	if err := control.ArchiveState(context.Background(), digest); !errors.Is(err, syncErr) {
		t.Fatalf("ArchiveState error=%v", err)
	}
	got, _, err := control.ReadState(context.Background())
	if err != nil || string(got) != string(state) {
		t.Fatalf("state after archive failure=%q err=%v", got, err)
	}
}

func TestInstallControl_UnixStartupLockSurvivesParentClose(t *testing.T) {
	base := installControlUnixTestRoot(t)
	control, _, err := initializeUnixInstallControlAt(context.Background(), base, []byte(`{"state":"complete"}`), nil)
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
		t.Fatalf("independent held startup capability: %v", err)
	}
	if err := startup.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallControl_UnixExistingControlWithoutStateIsUninitialized(t *testing.T) {
	base := installControlUnixTestRoot(t)
	if _, err := base.OpenDir(context.Background(), installControlDirName, RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true}); err != nil {
		t.Fatal(err)
	}
	control, _, err := initializeUnixInstallControlAt(context.Background(), base, []byte(`{"state":"complete"}`), nil)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, ErrInstallControlUninitialized) {
		t.Fatalf("initialize error=%v", err)
	}
}

type installControlUnixFaultBackend struct {
	trustedBackend
	syncErr error
}

func (b *installControlUnixFaultBackend) sync(int) error { return b.syncErr }

func installControlUnixTestRoot(t *testing.T) *TrustedRoot {
	t.Helper()
	root, _ := trustedTempCapability(t)
	return root
}
