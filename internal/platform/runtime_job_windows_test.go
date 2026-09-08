//go:build windows

package platform

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
)

func TestWindowsProcessIdentity_NativeLimitedQueryAccessReadsCurrentToken(t *testing.T) {
	process, err := OpenWindowsProcessIdentity(context.Background(), uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, process.Close)
	identity := process.Identity()
	if identity.PID != uint32(os.Getpid()) || identity.CreationFiletime == 0 || identity.ImagePath == "" || identity.SID == "" {
		t.Fatalf("incomplete native process identity=%+v", identity)
	}
}

func TestWindowsRuntimeJob_OwnerIdentityConcurrentClose(t *testing.T) {
	job, err := createWindowsRuntimeJob(context.Background(), "0123456789abcdef0123456789abcdef", newFakeWindowsRuntimeJobBackend())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		<-start
		for i := 0; i < 1000; i++ {
			identity, ok := job.OwnerIdentity()
			if ok && identity.PID != 41 {
				t.Error("changed immutable process identity")
				return
			}
		}
	}()
	close(start)
	if err := job.Close(); err != nil {
		t.Error(err)
	}
	readers.Wait()
}

func TestCreateWindowsRuntimeJob_BindsCurrentProcessBeforeReturn(t *testing.T) {
	backend := newFakeWindowsRuntimeJobBackend()
	job, err := createWindowsRuntimeJob(context.Background(), "0123456789abcdef0123456789abcdef", backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = job.Close() })

	if got, want := job.Name(), `Global\Mihari.Runtime.0123456789abcdef0123456789abcdef`; got != want {
		t.Fatalf("name=%q want %q", got, want)
	}
	identity, ok := job.OwnerIdentity()
	if !ok {
		t.Fatal("created job did not retain its owner process identity")
	}
	if identity.PID != 41 || identity.CreationFiletime != 132537600000000123 || identity.ImagePath != `C:\Program Files\Mihari\mihari.exe` || identity.SID != "S-1-5-18" {
		t.Fatalf("owner identity=%+v", identity)
	}
	if backend.createdInherit {
		t.Fatal("named job handle was inheritable")
	}
	if got, want := backend.configuredFlags, uint32(windowsJobLimitKillOnClose); got != want {
		t.Fatalf("limit flags=%#x want %#x", got, want)
	}
	if got, want := backend.events, []string{"create-job", "configure-job", "open-current-process", "assign-process", "is-process-in-job"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("native order=%v want %v", got, want)
	}
}

func TestCreateWindowsRuntimeJob_RejectsCollisionAndClosesReturnedHandle(t *testing.T) {
	closeErr := errors.New("close collided job")
	backend := newFakeWindowsRuntimeJobBackend()
	backend.createCollision = true
	backend.closeJobErr = closeErr

	job, err := createWindowsRuntimeJob(context.Background(), "0123456789abcdef0123456789abcdef", backend)
	if job != nil || !errors.Is(err, ErrWindowsRuntimeJobCollision) || !errors.Is(err, closeErr) {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if got := backend.closedJobs; !reflect.DeepEqual(got, []windowsJobHandle{11}) {
		t.Fatalf("closed jobs=%v", got)
	}
}

func TestCreateWindowsRuntimeJob_RejectsInvalidGenerationWithoutNativeCalls(t *testing.T) {
	for _, generation := range []string{"", "ABCDEF0123456789ABCDEF0123456789", "0123456789abcdef0123456789abcdeg", "0123"} {
		t.Run(generation, func(t *testing.T) {
			backend := newFakeWindowsRuntimeJobBackend()
			if job, err := createWindowsRuntimeJob(context.Background(), generation, backend); job != nil || err == nil {
				t.Fatalf("job=%v err=%v", job, err)
			}
			if len(backend.events) != 0 {
				t.Fatalf("invalid generation reached native backend: %v", backend.events)
			}
		})
	}
}

func TestCreateWindowsRuntimeJob_RejectsUnverifiedMembership(t *testing.T) {
	backend := newFakeWindowsRuntimeJobBackend()
	backend.inJob = false
	job, err := createWindowsRuntimeJob(context.Background(), "0123456789abcdef0123456789abcdef", backend)
	if job != nil || !errors.Is(err, ErrWindowsRuntimeJobMembership) {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if got := backend.closedProcesses; !reflect.DeepEqual(got, []windowsProcessHandle{21}) {
		t.Fatalf("closed processes=%v", got)
	}
	if got := backend.closedJobs; !reflect.DeepEqual(got, []windowsJobHandle{11}) {
		t.Fatalf("closed jobs=%v", got)
	}
}

func TestCreateWindowsRuntimeJob_RejectsIncompleteCurrentProcessIdentity(t *testing.T) {
	backend := newFakeWindowsRuntimeJobBackend()
	backend.identity.SID = ""
	job, err := createWindowsRuntimeJob(context.Background(), "0123456789abcdef0123456789abcdef", backend)
	if job != nil || err == nil {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if got := backend.closedProcesses; !reflect.DeepEqual(got, []windowsProcessHandle{21}) {
		t.Fatalf("closed processes=%v", got)
	}
	if got := backend.closedJobs; !reflect.DeepEqual(got, []windowsJobHandle{11}) {
		t.Fatalf("closed jobs=%v", got)
	}
}

func TestOpenWindowsRuntimeJob_QueriesActiveProcessesAndPropagatesNotFound(t *testing.T) {
	backend := newFakeWindowsRuntimeJobBackend()
	backend.active = 3
	job, err := openWindowsRuntimeJob(context.Background(), `Global\Mihari.Runtime.0123456789abcdef0123456789abcdef`, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, job.Close)
	active, err := job.ActiveProcesses(context.Background())
	if err != nil || active != 3 {
		t.Fatalf("active=%d err=%v", active, err)
	}

	notFound := errors.New("job not found")
	backend = newFakeWindowsRuntimeJobBackend()
	backend.openJobErr = notFound
	job, err = openWindowsRuntimeJob(context.Background(), `Global\Mihari.Runtime.0123456789abcdef0123456789abcdef`, backend)
	if job != nil || !errors.Is(err, notFound) {
		t.Fatalf("missing named job was treated as an empty tree: job=%v err=%v", job, err)
	}
}

func TestOpenWindowsRuntimeJob_RejectsUnsafeLimits(t *testing.T) {
	unsafeErr := errors.New("job permits breakaway")
	backend := newFakeWindowsRuntimeJobBackend()
	backend.verifyJobErr = unsafeErr
	job, err := openWindowsRuntimeJob(context.Background(), `Global\Mihari.Runtime.0123456789abcdef0123456789abcdef`, backend)
	if job != nil || !errors.Is(err, unsafeErr) {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if got := backend.closedJobs; !reflect.DeepEqual(got, []windowsJobHandle{12}) {
		t.Fatalf("closed jobs=%v", got)
	}
}

func TestWindowsRuntimeJob_VerifiedCapabilityAloneCanTerminate(t *testing.T) {
	backend := newFakeWindowsRuntimeJobBackend()
	job, err := openWindowsRuntimeJob(context.Background(), `Global\Mihari.Runtime.0123456789abcdef0123456789abcdef`, backend)
	if err != nil {
		t.Fatal(err)
	}
	process, err := openWindowsProcessIdentity(context.Background(), 41, backend)
	if err != nil {
		t.Fatal(err)
	}

	backend.inJob = false
	capability, err := job.VerifyMember(context.Background(), process)
	if capability != nil || !errors.Is(err, ErrWindowsRuntimeJobMembership) {
		t.Fatalf("unverified capability=%v err=%v", capability, err)
	}
	if backend.terminateCalls != 0 {
		t.Fatal("membership rejection terminated a job")
	}

	backend.inJob = true
	capability, err = job.VerifyMember(context.Background(), process)
	if err != nil {
		t.Fatal(err)
	}
	// The capability owns duplicated handles, so the observations used to
	// establish it may be released without degrading termination to a name lookup.
	if err := errors.Join(job.Close(), process.Close()); err != nil {
		t.Fatal(err)
	}
	if err := capability.Terminate(context.Background(), 23); err != nil {
		t.Fatal(err)
	}
	if backend.terminatedJob != 101 || backend.terminateExitCode != 23 || backend.terminateCalls != 1 {
		t.Fatalf("termination job=%d code=%d calls=%d", backend.terminatedJob, backend.terminateExitCode, backend.terminateCalls)
	}
	if err := capability.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsProcessIdentity_RetainsMetadataAndExitObservation(t *testing.T) {
	backend := newFakeWindowsRuntimeJobBackend()
	process, err := openWindowsProcessIdentity(context.Background(), 41, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, process.Close)
	if got := process.Identity(); got.PID != 41 || got.CreationFiletime != 132537600000000123 || got.ImagePath != `C:\Program Files\Mihari\mihari.exe` || got.SID != "S-1-5-18" {
		t.Fatalf("identity=%+v", got)
	}
	backend.processExited = true
	exited, err := process.Exited(context.Background())
	if err != nil || !exited {
		t.Fatalf("exited=%v err=%v", exited, err)
	}
}

func TestWindowsRuntimeCapabilities_CloseJoinsNativeErrors(t *testing.T) {
	jobCloseErr := errors.New("close job")
	processCloseErr := errors.New("close process")
	backend := newFakeWindowsRuntimeJobBackend()
	backend.closeJobErr = jobCloseErr
	backend.closeProcessErr = processCloseErr
	job, err := createWindowsRuntimeJob(context.Background(), "0123456789abcdef0123456789abcdef", backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := job.Close(); !errors.Is(err, jobCloseErr) || !errors.Is(err, processCloseErr) {
		t.Fatalf("close error=%v", err)
	}
	if err := job.Close(); err != nil {
		t.Fatalf("repeat close=%v", err)
	}
}

func TestWindowsRuntimeJob_CanceledContextDoesNotReachNativeBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := newFakeWindowsRuntimeJobBackend()
	job, err := createWindowsRuntimeJob(ctx, "0123456789abcdef0123456789abcdef", backend)
	if job != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if len(backend.events) != 0 {
		t.Fatalf("canceled create reached native backend: %v", backend.events)
	}
}

type fakeWindowsRuntimeJobBackend struct {
	events                []string
	createCollision       bool
	createdInherit        bool
	configuredFlags       uint32
	inJob                 bool
	active                uint32
	processExited         bool
	openJobErr            error
	verifyJobErr          error
	closeJobErr           error
	closeProcessErr       error
	closedJobs            []windowsJobHandle
	closedProcesses       []windowsProcessHandle
	terminatedJob         windowsJobHandle
	terminateExitCode     uint32
	terminateCalls        int
	nextDuplicatedJob     windowsJobHandle
	nextDuplicatedProcess windowsProcessHandle
	identity              WindowsProcessIdentityValue
}

func newFakeWindowsRuntimeJobBackend() *fakeWindowsRuntimeJobBackend {
	return &fakeWindowsRuntimeJobBackend{inJob: true, nextDuplicatedJob: 101, nextDuplicatedProcess: 201, identity: fakeWindowsProcessIdentityValue()}
}

func (f *fakeWindowsRuntimeJobBackend) createProtectedJob(_ context.Context, _ string, inherit bool) (windowsJobHandle, bool, error) {
	f.events = append(f.events, "create-job")
	f.createdInherit = inherit
	return 11, f.createCollision, nil
}

func (f *fakeWindowsRuntimeJobBackend) openJob(_ context.Context, _ string) (windowsJobHandle, error) {
	f.events = append(f.events, "open-job")
	return 12, f.openJobErr
}

func (f *fakeWindowsRuntimeJobBackend) configureJob(_ context.Context, handle windowsJobHandle, flags uint32) error {
	f.events = append(f.events, "configure-job")
	f.configuredFlags = flags
	if handle != 11 {
		return errors.New("wrong job handle")
	}
	if flags&windowsJobLimitKillOnClose == 0 || flags&(windowsJobLimitBreakaway|windowsJobLimitSilentBreakaway) != 0 {
		return errors.New("unsafe breakaway limits")
	}
	return nil
}

func (f *fakeWindowsRuntimeJobBackend) verifyJob(_ context.Context, _ windowsJobHandle) error {
	f.events = append(f.events, "verify-job")
	return f.verifyJobErr
}

func (f *fakeWindowsRuntimeJobBackend) openCurrentProcess(context.Context) (windowsProcessHandle, WindowsProcessIdentityValue, error) {
	f.events = append(f.events, "open-current-process")
	return 21, f.identity, nil
}

func (f *fakeWindowsRuntimeJobBackend) openProcess(_ context.Context, pid uint32) (windowsProcessHandle, WindowsProcessIdentityValue, error) {
	f.events = append(f.events, "open-process")
	identity := f.identity
	identity.PID = pid
	return 22, identity, nil
}

func fakeWindowsProcessIdentityValue() WindowsProcessIdentityValue {
	return WindowsProcessIdentityValue{
		PID:              41,
		CreationFiletime: 132537600000000123,
		ImagePath:        `C:\Program Files\Mihari\mihari.exe`,
		SID:              "S-1-5-18",
	}
}

func (f *fakeWindowsRuntimeJobBackend) assignProcess(_ context.Context, _ windowsJobHandle, _ windowsProcessHandle) error {
	f.events = append(f.events, "assign-process")
	return nil
}

func (f *fakeWindowsRuntimeJobBackend) isProcessInJob(_ context.Context, _ windowsProcessHandle, _ windowsJobHandle) (bool, error) {
	f.events = append(f.events, "is-process-in-job")
	return f.inJob, nil
}

func (f *fakeWindowsRuntimeJobBackend) activeProcesses(_ context.Context, _ windowsJobHandle) (uint32, error) {
	f.events = append(f.events, "active-processes")
	return f.active, nil
}

func (f *fakeWindowsRuntimeJobBackend) processHasExited(_ context.Context, _ windowsProcessHandle) (bool, error) {
	f.events = append(f.events, "process-exited")
	return f.processExited, nil
}

func (f *fakeWindowsRuntimeJobBackend) duplicateJob(_ context.Context, _ windowsJobHandle) (windowsJobHandle, error) {
	f.events = append(f.events, "duplicate-job")
	return f.nextDuplicatedJob, nil
}

func (f *fakeWindowsRuntimeJobBackend) duplicateProcess(_ context.Context, _ windowsProcessHandle) (windowsProcessHandle, error) {
	f.events = append(f.events, "duplicate-process")
	return f.nextDuplicatedProcess, nil
}

func (f *fakeWindowsRuntimeJobBackend) terminateJob(_ context.Context, handle windowsJobHandle, exitCode uint32) error {
	f.events = append(f.events, "terminate-job")
	f.terminatedJob = handle
	f.terminateExitCode = exitCode
	f.terminateCalls++
	return nil
}

func (f *fakeWindowsRuntimeJobBackend) closeJob(handle windowsJobHandle) error {
	f.closedJobs = append(f.closedJobs, handle)
	return f.closeJobErr
}

func (f *fakeWindowsRuntimeJobBackend) closeProcess(handle windowsProcessHandle) error {
	f.closedProcesses = append(f.closedProcesses, handle)
	return f.closeProcessErr
}
