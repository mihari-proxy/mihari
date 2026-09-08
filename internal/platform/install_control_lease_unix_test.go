//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// The sync barrier is inside the publication, after initial lease validation.
// Closing the outer lease must wait until this whole operation returns.
func TestInstallControl_UnixInitializationRetainsLeaseDuringPublication(t *testing.T) {
	testInstallControlUnixRetainsLeaseDuringPublication(t, false)
}

func TestInstallControl_UnixReplacementRetainsLeaseDuringPublication(t *testing.T) {
	testInstallControlUnixRetainsLeaseDuringPublication(t, true)
}

func testInstallControlUnixRetainsLeaseDuringPublication(t *testing.T, replace bool) {
	t.Helper()
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	base.path = platformLayoutDefaults("").BaseDir
	lock, err := acquireRootLock(ctx, base, "install.lock")
	if err != nil {
		t.Fatal(err)
	}
	lease := &OwnedInstallLease{state: &leaseState{global: true, owner: 0, roots: []*TrustedRoot{base}, locks: []*rootLock{lock}}}
	t.Cleanup(func() { _ = lease.Close() })
	var control *InstallControl
	var digest string
	if replace {
		control, _, err = InitializeInstallControlForUpdate(ctx, lease, []byte(`{"generation":1}`))
		if err != nil {
			t.Fatal(err)
		}
		defer assertInstallTestClose(t, control.Close)
		operation, err := control.LockOperation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer assertInstallTestClose(t, operation.Close)
		_, digest, err = control.ReadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	backend := &installControlLeaseBarrier{trustedBackend: base.backend, entered: entered, resume: resume}
	base.backend = backend
	if replace {
		control.platform.(*unixInstallControl).root.backend = backend
	}
	done := make(chan error, 1)
	go func() {
		if replace {
			_, err := control.PublishState(ctx, digest, []byte(`{"generation":2}`))
			done <- err
			return
		}
		control, _, err := InitializeInstallControlForUpdate(ctx, lease, []byte(`{"state":"complete"}`))
		if control != nil {
			err = errors.Join(err, control.Close())
		}
		done <- err
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- lease.Close() }()
	<-lease.state.closing
	releasedEarly := false
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("close lease: %v", err)
		}
		releasedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(resume)
	initErr := <-done
	if !releasedEarly {
		if err := <-closed; err != nil {
			t.Errorf("close lease: %v", err)
		}
	}
	if releasedEarly {
		t.Fatal("global installation lease closed while publication was in flight")
	}
	if initErr != nil {
		t.Fatalf("publication interrupted by concurrent Close: %v", initErr)
	}
}

func TestInstallControl_UnixClosedLeaseCannotPublish(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	base.path = platformLayoutDefaults("").BaseDir
	lock, err := acquireRootLock(ctx, base, "install.lock")
	if err != nil {
		t.Fatal(err)
	}
	lease := &OwnedInstallLease{state: &leaseState{global: true, owner: 0, roots: []*TrustedRoot{base}, locks: []*rootLock{lock}}}
	defer assertInstallTestClose(t, lease.Close)
	control, _, err := InitializeInstallControlForUpdate(ctx, lease, []byte(`{"generation":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	operation, err := control.LockOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, operation.Close)
	_, digest, err := control.ReadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	publication, err := control.PublishState(ctx, digest, []byte(`{"generation":2}`))
	if err == nil || publication.Published {
		t.Fatalf("closed lease published: %+v, %v", publication, err)
	}
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed lease error: %v", err)
	}
}

type installControlLeaseBarrier struct {
	trustedBackend
	once            sync.Once
	entered, resume chan struct{}
}

func (b *installControlLeaseBarrier) sync(fd int) error {
	b.once.Do(func() { close(b.entered); <-b.resume })
	return b.trustedBackend.sync(fd)
}
