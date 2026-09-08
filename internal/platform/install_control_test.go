package platform

import (
	"context"
	"errors"
	"os"
	"testing"
)

func assertInstallTestClose(t *testing.T, close func() error) {
	t.Helper()
	if err := close(); err != nil {
		t.Errorf("close installation test capability: %v", err)
	}
}

type installControlFake struct {
	state       []byte
	publish     InstallPublication
	publishErr  error
	archived    string
	confirmed   string
	probeLocked bool
	lockCalls   []string
	closed      bool
}

func (f *installControlFake) readState(context.Context) ([]byte, error) {
	return append([]byte(nil), f.state...), nil
}
func (f *installControlFake) archiveState(_ context.Context, digest string) error {
	f.archived = digest
	return nil
}
func (f *installControlFake) publishState(_ context.Context, _ string, next []byte) (InstallPublication, error) {
	f.state = append([]byte(nil), next...)
	return f.publish, f.publishErr
}
func (f *installControlFake) confirmState(_ context.Context, digest string) error {
	f.confirmed = digest
	return nil
}
func (f *installControlFake) probeOperation(context.Context) (bool, error) {
	return f.probeLocked, nil
}
func (f *installControlFake) lockOperation(context.Context) (installControlLockPlatform, error) {
	f.lockCalls = append(f.lockCalls, installOperationLockName)
	return installControlFakeLock{}, nil
}
func (f *installControlFake) lockStartup(context.Context) (installControlLockPlatform, error) {
	f.lockCalls = append(f.lockCalls, installStartupLockName)
	return installControlFakeLock{}, nil
}
func (f *installControlFake) close() error { f.closed = true; return nil }

type installControlFakeLock struct{}

func (installControlFakeLock) checkHeld() error { return nil }
func (installControlFakeLock) close() error     { return nil }

func TestInstallControl_RawStateContractAndPublicationOutcome(t *testing.T) {
	fake := &installControlFake{
		state:      []byte(`{"raw":true}`),
		publish:    InstallPublication{Published: true, Durable: false},
		publishErr: errors.New("injected durable sync failure"),
	}
	c := newInstallControl(fake, false)
	lock, err := c.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, lock.Close)
	state, digest, err := c.ReadState(context.Background())
	if err != nil || string(state) != `{"raw":true}` || digest != "f528a13eb3833b6af304159a3db6785702540fce4c13674eec4e2df22fdffecf" {
		t.Fatalf("ReadState state=%q digest=%q err=%v", state, digest, err)
	}
	publication, err := c.PublishState(context.Background(), digest, []byte(`{"raw":false}`))
	if !errors.Is(err, fake.publishErr) || !publication.Published || publication.Durable {
		t.Fatalf("PublishState publication=%+v err=%v", publication, err)
	}
}

func TestInstallControl_ReadOnlyRejectsMutationAndLockWithoutCallingBackend(t *testing.T) {
	fake := &installControlFake{}
	c := newInstallControl(fake, true)
	if err := c.ArchiveState(context.Background(), installStateSHA256(nil)); !errors.Is(err, ErrInstallControlReadOnly) {
		t.Fatalf("ArchiveState error=%v", err)
	}
	if _, err := c.PublishState(context.Background(), "", nil); !errors.Is(err, ErrInstallControlReadOnly) {
		t.Fatalf("PublishState error=%v", err)
	}
	if _, err := c.LockOperation(context.Background()); !errors.Is(err, ErrInstallControlReadOnly) {
		t.Fatalf("LockOperation error=%v", err)
	}
	if len(fake.lockCalls) != 0 || fake.archived != "" {
		t.Fatalf("read-only calls escaped to backend: locks=%v archive=%q", fake.lockCalls, fake.archived)
	}
}

func TestInstallControl_RejectsInvalidDigestsAndOversizeState(t *testing.T) {
	fake := &installControlFake{state: make([]byte, installStateMaxBytes+1)}
	c := newInstallControl(fake, false)
	if _, _, err := c.ReadState(context.Background()); !errors.Is(err, ErrInstallStateTooLarge) {
		t.Fatalf("ReadState error=%v", err)
	}
	if _, err := c.PublishState(context.Background(), "ABC", nil); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("PublishState digest error=%v", err)
	}
	if err := c.ConfirmState(context.Background(), ""); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("ConfirmState digest error=%v", err)
	}
}

func TestInstallControl_CloseInvalidatesCapability(t *testing.T) {
	fake := &installControlFake{}
	c := newInstallControl(fake, false)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !fake.closed {
		t.Fatal("platform capability was not closed")
	}
	if _, _, err := c.ReadState(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("ReadState after Close error=%v", err)
	}
}

func TestInstallControl_WindowsStartupGateRequiresLiveStartupLock(t *testing.T) {
	c := newInstallControl(&installControlFake{}, false)
	operation, err := c.LockOperation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := operation.CheckWindowsStartupGate(); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("operation gate check error=%v", err)
	}
	startup, err := c.LockStartup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := startup.CheckWindowsStartupGate(); err != nil {
		t.Fatalf("live startup gate check: %v", err)
	}
	if err := startup.Close(); err != nil {
		t.Fatal(err)
	}
	if err := startup.CheckWindowsStartupGate(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed startup gate check error=%v", err)
	}
}

func TestInstallControl_MutationRequiresHeldOperationLock(t *testing.T) {
	c := newInstallControl(&installControlFake{}, false)
	digest := installStateSHA256(nil)
	if err := c.ArchiveState(context.Background(), digest); !errors.Is(err, ErrInstallControlBusy) {
		t.Fatalf("ArchiveState without operation lock error=%v", err)
	}
	if _, err := c.PublishState(context.Background(), digest, nil); !errors.Is(err, ErrInstallControlBusy) {
		t.Fatalf("PublishState without operation lock error=%v", err)
	}
}

func TestInstallControl_ZeroNilAndClosedLockCallsFailClosed(t *testing.T) {
	var nilControl *InstallControl
	if _, err := nilControl.LockOperation(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("nil LockOperation error=%v", err)
	}
	var zero InstallControl
	if _, err := zero.LockStartup(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("zero LockStartup error=%v", err)
	}
	c := newInstallControl(&installControlFake{}, false)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LockOperation(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed LockOperation error=%v", err)
	}
}
