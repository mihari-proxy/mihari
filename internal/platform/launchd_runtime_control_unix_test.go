//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLaunchdRuntimeControl_UnixOpenDoesNotCreate(t *testing.T) {
	base := installControlUnixTestRoot(t)
	control, err := openUnixLaunchdRuntimeControlAt(context.Background(), base)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open error=%v", err)
	}
	names, readErr := base.ReadNames(context.Background())
	if readErr != nil || len(names) != 0 {
		t.Fatalf("open created names=%v err=%v", names, readErr)
	}
}

func TestLaunchdRuntimeControl_UnixInitializationPublishesCompleteFixedDirectory(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	initial := []byte(`{"schema":"mihari.launchd-runtime/v1","generation":null}`)
	control, publication, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	if !publication.Published || !publication.Durable {
		t.Fatalf("publication=%+v", publication)
	}
	dir, err := base.OpenDir(ctx, launchdRuntimeDirName, RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, dir.Close)
	names, err := dir.ReadNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != launchdRuntimeLockName || names[1] != launchdRuntimeStateName {
		t.Fatalf("fixed names=%v", names)
	}
	state, _, err := control.Read(ctx)
	if err != nil || string(state) != string(initial) {
		t.Fatalf("state=%q err=%v", state, err)
	}
}

func TestLaunchdRuntimeControl_UnixExistingIncompleteDirectoryFailsClosed(t *testing.T) {
	for _, present := range []string{launchdRuntimeLockName, launchdRuntimeStateName} {
		t.Run("only_"+present, func(t *testing.T) {
			ctx := context.Background()
			base := installControlUnixTestRoot(t)
			dir, err := base.OpenDir(ctx, launchdRuntimeDirName, RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700, AllowCreate: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = dir.WriteFile(ctx, present, nil, 0600, nil); err != nil {
				t.Fatal(err)
			}
			if err = dir.Close(); err != nil {
				t.Fatal(err)
			}
			control, _, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), nil)
			if control != nil {
				_ = control.Close()
			}
			if !errors.Is(err, ErrLaunchdRuntimeUninitialized) {
				t.Fatalf("initialize error=%v", err)
			}
		})
	}
}

func TestLaunchdRuntimeControl_UnixInitializationRaceUsesPublishedGate(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	start := make(chan struct{})
	type result struct {
		control     *LaunchdRuntimeControl
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
			control, publication, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), nil)
			results <- result{control, publication, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var controls []*LaunchdRuntimeControl
	published := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
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
		t.Fatalf("published=%d", published)
	}
	gate, err := controls[0].LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	if _, err = controls[1].LockStartup(ctx); !errors.Is(err, ErrInstallControlBusy) {
		t.Fatalf("second gate error=%v", err)
	}
}

func TestLaunchdRuntimeControl_UnixInitializationHoldsPublishedGateThroughValidation(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	validationEntered := make(chan struct{})
	releaseValidation := make(chan struct{})
	validationCalls := 0
	validate := func(context.Context) error {
		validationCalls++
		if validationCalls == 3 {
			close(validationEntered)
			<-releaseValidation
		}
		return nil
	}
	type result struct {
		control *LaunchdRuntimeControl
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		control, _, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), validate)
		resultCh <- result{control: control, err: err}
	}()
	<-validationEntered

	concurrent, err := openUnixLaunchdRuntimeControlAt(ctx, base)
	if err != nil {
		close(releaseValidation)
		t.Fatal(err)
	}
	gate, lockErr := concurrent.LockStartup(ctx)
	if gate != nil {
		defer assertInstallTestClose(t, gate.Close)
	}
	if err = concurrent.Close(); err != nil {
		close(releaseValidation)
		t.Fatal(err)
	}
	close(releaseValidation)
	initialized := <-resultCh
	if initialized.control != nil {
		defer assertInstallTestClose(t, initialized.control.Close)
	}
	if initialized.err != nil {
		t.Fatalf("initialize error=%v", initialized.err)
	}
	if !errors.Is(lockErr, ErrInstallControlBusy) {
		t.Fatalf("startup gate during post-publication validation error=%v", lockErr)
	}
}

func TestLaunchdRuntimeControl_UnixGateSurvivesControlClose(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	first, _, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := first.LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := openUnixLaunchdRuntimeControlAt(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, second.Close)
	if _, err = second.LockStartup(ctx); !errors.Is(err, ErrInstallControlBusy) {
		t.Fatalf("gate after control close error=%v", err)
	}
	if err = gate.Close(); err != nil {
		t.Fatal(err)
	}
	secondGate, err := second.LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = secondGate.Close()
}

func TestLaunchdRuntimeControl_UnixInitializationRequiresValidatorBeforeCreation(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	deniedErr := errors.New("injected lease invalid")
	control, _, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), func(context.Context) error { return deniedErr })
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, deniedErr) {
		t.Fatalf("initialize error=%v", err)
	}
	names, readErr := base.ReadNames(ctx)
	if readErr != nil || len(names) != 0 {
		t.Fatalf("invalid lease created names=%v err=%v", names, readErr)
	}
}

func TestLaunchdRuntimeControl_UnixLeaseLossBeforeRenameCleansStaging(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	deniedErr := errors.New("injected lease lost before rename")
	calls := 0
	validate := func(context.Context) error {
		calls++
		if calls == 2 {
			return deniedErr
		}
		return nil
	}
	control, publication, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), validate)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, deniedErr) || publication.Published {
		t.Fatalf("publication=%+v error=%v", publication, err)
	}
	names, readErr := base.ReadNames(ctx)
	if readErr != nil || len(names) != 0 {
		t.Fatalf("lost lease left names=%v err=%v", names, readErr)
	}
}

func TestLaunchdRuntimeControl_UnixCleanupRemovesEmptyStagingAfterCloseError(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	tempName := ".launchd-runtime-cleanup-close-error"
	temp, err := base.OpenDir(ctx, tempName, RootPolicy{Owner: base.policy.Owner, Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanupFinished := false
	t.Cleanup(func() {
		if !cleanupFinished {
			_ = temp.Close()
		}
	})
	for _, item := range []struct {
		name string
		body []byte
	}{
		{name: launchdRuntimeLockName},
		{name: launchdRuntimeStateName, body: []byte(`{"generation":null}`)},
	} {
		if err := temp.WriteFile(ctx, item.name, item.body, 0600, nil); err != nil {
			t.Fatal(err)
		}
	}
	closeErr := errors.New("injected staging close failure")
	failingDescriptors := make(map[int]bool, len(temp.chain))
	for _, link := range temp.chain {
		failingDescriptors[link.fd] = true
	}
	temp.backend = &launchdRuntimeCloseErrorBackend{trustedBackend: temp.backend, err: closeErr, failingDescriptors: failingDescriptors}
	err = cleanupLaunchdRuntimeUnixTemp(base, temp, tempName)
	cleanupFinished = true
	if !errors.Is(err, closeErr) {
		t.Fatalf("cleanup error=%v", err)
	}
	names, readErr := base.ReadNames(ctx)
	if readErr != nil || len(names) != 0 {
		t.Fatalf("failed cleanup left staging names=%v err=%v", names, readErr)
	}
}

func TestLaunchdRuntimeControl_UnixOversizeInitializationCreatesNothing(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	control, publication, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, make([]byte, launchdRuntimeStateMaxBytes+1), nil)
	if control != nil {
		_ = control.Close()
	}
	if !errors.Is(err, ErrLaunchdRuntimeStateTooLarge) || publication.Published {
		t.Fatalf("publication=%+v error=%v", publication, err)
	}
	names, readErr := base.ReadNames(ctx)
	if readErr != nil || len(names) != 0 {
		t.Fatalf("oversize initialization left names=%v err=%v", names, readErr)
	}
}

func TestLaunchdRuntimeControl_UnixPublishReportsVisibleNondurableState(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	control, _, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	gate, err := control.LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	_, digest, err := control.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("injected directory sync failure")
	backend := control.platform.(*unixLaunchdRuntimeControl)
	backend.store.root.backend = &installControlUnixFaultBackend{trustedBackend: backend.store.root.backend, syncErr: syncErr}
	publication, err := control.PublishState(ctx, digest, []byte(`{"generation":"next"}`))
	if !errors.Is(err, syncErr) || !publication.Published || publication.Durable {
		t.Fatalf("publication=%+v err=%v", publication, err)
	}
}

func TestLaunchdRuntimeControl_UnixRejectsStateNameReplacementAndStaleDigest(t *testing.T) {
	ctx := context.Background()
	base := installControlUnixTestRoot(t)
	control, _, err := initializeUnixLaunchdRuntimeControlAt(ctx, base, []byte(`{"generation":null}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, control.Close)
	gate, err := control.LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	_, digest, err := control.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.PublishState(ctx, installStateSHA256([]byte("stale")), []byte(`{"generation":"next"}`)); !errors.Is(err, ErrInstallStateChanged) {
		t.Fatalf("stale publish error=%v", err)
	}
	backend := control.platform.(*unixLaunchdRuntimeControl)
	if err = backend.store.root.WriteFile(ctx, "replacement", []byte(`{"generation":"attacker"}`), 0600, nil); err != nil {
		t.Fatal(err)
	}
	parent := backend.store.root.chain[len(backend.store.root.chain)-1].fd
	if err = unix.Renameat(parent, "replacement", parent, launchdRuntimeStateName); err != nil {
		t.Fatal(err)
	}
	if _, _, err = control.Read(ctx); !errors.Is(err, ErrInstallStateChanged) {
		t.Fatalf("replacement Read error=%v", err)
	}
	if _, err = control.PublishState(ctx, digest, []byte(`{"generation":"next"}`)); !errors.Is(err, ErrInstallStateChanged) {
		t.Fatalf("replacement PublishState error=%v", err)
	}
}

type launchdRuntimeCloseErrorBackend struct {
	trustedBackend
	err                error
	failingDescriptors map[int]bool
}

func (b *launchdRuntimeCloseErrorBackend) close(fd int) error {
	err := b.trustedBackend.close(fd)
	if b.failingDescriptors[fd] {
		return errors.Join(err, b.err)
	}
	return err
}
