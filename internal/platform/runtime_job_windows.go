//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsRuntimeJobPrefix         = `Global\Mihari.Runtime.`
	windowsRuntimeJobSDDL           = "O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)"
	windowsJobLimitBreakaway        = 0x00000800
	windowsJobLimitSilentBreakaway  = 0x00001000
	windowsJobLimitKillOnClose      = 0x00002000
	windowsJobQuery                 = 0x0004
	windowsJobTerminate             = 0x0008
	windowsDaemonProcessAccess      = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE | windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE
	windowsObservedProcessAccess    = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE
	windowsProcessImageBufferLength = 32768
)

var (
	// ErrWindowsRuntimeJobCollision indicates that the generation's protected
	// Job name was already present and therefore cannot prove this daemon's tree.
	ErrWindowsRuntimeJobCollision = errors.New("windows runtime job name already exists")
	// ErrWindowsRuntimeJobMembership indicates that a held process identity was
	// not observed in the held runtime Job.
	ErrWindowsRuntimeJobMembership   = errors.New("process is not a member of the windows runtime job")
	errWindowsNativeCapabilityClosed = fmt.Errorf("windows native capability: %w", os.ErrClosed)

	kernel32RuntimeJob                               = windows.NewLazySystemDLL("kernel32.dll")
	procCreateJobObjectW                             = kernel32RuntimeJob.NewProc("CreateJobObjectW")
	procOpenJobObjectW                               = kernel32RuntimeJob.NewProc("OpenJobObjectW")
	procIsProcessInJob                               = kernel32RuntimeJob.NewProc("IsProcessInJob")
	nativeRuntimeJobBackend windowsRuntimeJobBackend = windowsRuntimeJobNativeBackend{}
)

type windowsJobHandle uintptr
type windowsProcessHandle uintptr

type windowsNativeCapabilityLifetime struct {
	mu        sync.Mutex
	closed    bool
	operation chan struct{}
	closing   chan struct{}
	closeDone chan struct{}
}

func (l *windowsNativeCapabilityLifetime) begin(ctx context.Context) (func(), error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errWindowsNativeCapabilityClosed
	}
	if l.operation == nil {
		l.operation = make(chan struct{}, 1)
		l.operation <- struct{}{}
		l.closing = make(chan struct{})
	}
	operation, closing := l.operation, l.closing
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-closing:
		return nil, errWindowsNativeCapabilityClosed
	case <-operation:
	}
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		operation <- struct{}{}
		return nil, errWindowsNativeCapabilityClosed
	}
	if err := ctx.Err(); err != nil {
		operation <- struct{}{}
		return nil, err
	}
	return func() { operation <- struct{}{} }, nil
}

func (l *windowsNativeCapabilityLifetime) closeWith(closeNative func() error) error {
	l.mu.Lock()
	if l.closed {
		done := l.closeDone
		l.mu.Unlock()
		<-done
		return nil
	}
	l.closed = true
	l.closeDone = make(chan struct{})
	done, operation := l.closeDone, l.operation
	if l.closing != nil {
		close(l.closing)
	}
	l.mu.Unlock()
	if operation != nil {
		<-operation
	}
	err := closeNative()
	close(done)
	return err
}

// WindowsProcessIdentityValue is immutable identity evidence sampled from a
// held native process handle.
type WindowsProcessIdentityValue struct {
	PID              uint32
	CreationFiletime uint64
	ImagePath        string
	SID              string
}

// WindowsProcessIdentity owns a native process handle so PID reuse cannot
// replace the process represented by Identity.
type WindowsProcessIdentity struct {
	lifetime windowsNativeCapabilityLifetime
	backend  windowsRuntimeJobBackend
	handle   windowsProcessHandle
	identity WindowsProcessIdentityValue
}

// Identity returns the metadata sampled from the held process handle.
func (p *WindowsProcessIdentity) Identity() WindowsProcessIdentityValue {
	if p == nil {
		return WindowsProcessIdentityValue{}
	}
	return p.identity
}

// Exited reports whether the held process handle is signaled.
func (p *WindowsProcessIdentity) Exited(ctx context.Context) (bool, error) {
	if p == nil {
		return false, fmt.Errorf("windows process identity is nil")
	}
	finish, err := p.lifetime.begin(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	exited, err := p.backend.processHasExited(ctx, p.handle)
	if err != nil {
		return false, fmt.Errorf("query held process exit: %w", err)
	}
	return exited, nil
}

// Close releases the held native process handle. Repeat calls return nil.
func (p *WindowsProcessIdentity) Close() error {
	if p == nil {
		return nil
	}
	return p.lifetime.closeWith(func() error {
		if p.handle == 0 {
			return nil
		}
		err := p.backend.closeProcess(p.handle)
		p.handle = 0
		return err
	})
}

// WindowsRuntimeJob owns a protected named Job handle and, for a newly
// created daemon Job, the daemon's process identity handle.
type WindowsRuntimeJob struct {
	lifetime windowsNativeCapabilityLifetime
	backend  windowsRuntimeJobBackend
	handle   windowsJobHandle
	name     string
	owner    *WindowsProcessIdentity
}

// CreateWindowsRuntimeJob creates and binds the current process to its
// generation's protected outer Job before returning.
func CreateWindowsRuntimeJob(ctx context.Context, generation string) (*WindowsRuntimeJob, error) {
	return createWindowsRuntimeJob(ctx, generation, nativeRuntimeJobBackend)
}

// OpenWindowsRuntimeJob opens a registered protected Job. A missing name is
// returned as an error and never interpreted as an empty process tree.
func OpenWindowsRuntimeJob(ctx context.Context, name string) (*WindowsRuntimeJob, error) {
	return openWindowsRuntimeJob(ctx, name, nativeRuntimeJobBackend)
}

// OpenWindowsProcessIdentity opens and retains identity evidence for pid.
func OpenWindowsProcessIdentity(ctx context.Context, pid uint32) (*WindowsProcessIdentity, error) {
	return openWindowsProcessIdentity(ctx, pid, nativeRuntimeJobBackend)
}

func createWindowsRuntimeJob(ctx context.Context, generation string, backend windowsRuntimeJobBackend) (*WindowsRuntimeJob, error) {
	if !isLowerHex32(generation) {
		return nil, fmt.Errorf("invalid windows runtime generation")
	}
	if backend == nil {
		return nil, fmt.Errorf("windows runtime job backend is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name := windowsRuntimeJobPrefix + generation
	handle, collision, err := backend.createProtectedJob(ctx, name, false)
	if err != nil {
		return nil, fmt.Errorf("create protected windows runtime job: %w", err)
	}
	if collision {
		return nil, errors.Join(ErrWindowsRuntimeJobCollision, backend.closeJob(handle))
	}
	closeJobOnError := func(cause error) error {
		return errors.Join(cause, backend.closeJob(handle))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeJobOnError(err)
	}
	if err := backend.configureJob(ctx, handle, windowsJobLimitKillOnClose); err != nil {
		return nil, closeJobOnError(fmt.Errorf("configure windows runtime job: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeJobOnError(err)
	}
	processHandle, identity, err := backend.openCurrentProcess(ctx)
	if err != nil {
		return nil, closeJobOnError(fmt.Errorf("open current process identity: %w", err))
	}
	owner := &WindowsProcessIdentity{backend: backend, handle: processHandle, identity: identity}
	closeAllOnError := func(cause error) error {
		return errors.Join(cause, owner.Close(), backend.closeJob(handle))
	}
	if !validWindowsProcessIdentity(identity) {
		return nil, closeAllOnError(fmt.Errorf("current windows process identity is incomplete"))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeAllOnError(err)
	}
	if err := backend.assignProcess(ctx, handle, processHandle); err != nil {
		return nil, closeAllOnError(fmt.Errorf("assign daemon to windows runtime job: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeAllOnError(err)
	}
	inJob, err := backend.isProcessInJob(ctx, processHandle, handle)
	if err != nil {
		return nil, closeAllOnError(fmt.Errorf("verify daemon windows runtime job: %w", err))
	}
	if !inJob {
		return nil, closeAllOnError(ErrWindowsRuntimeJobMembership)
	}
	return &WindowsRuntimeJob{backend: backend, handle: handle, name: name, owner: owner}, nil
}

func openWindowsRuntimeJob(ctx context.Context, name string, backend windowsRuntimeJobBackend) (*WindowsRuntimeJob, error) {
	if !isWindowsRuntimeJobName(name) {
		return nil, fmt.Errorf("invalid windows runtime job name")
	}
	if backend == nil {
		return nil, fmt.Errorf("windows runtime job backend is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, err := backend.openJob(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("open protected windows runtime job: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, backend.closeJob(handle))
	}
	if err := backend.verifyJob(ctx, handle); err != nil {
		return nil, errors.Join(fmt.Errorf("verify protected windows runtime job: %w", err), backend.closeJob(handle))
	}
	return &WindowsRuntimeJob{backend: backend, handle: handle, name: name}, nil
}

func openWindowsProcessIdentity(ctx context.Context, pid uint32, backend windowsRuntimeJobBackend) (*WindowsProcessIdentity, error) {
	if pid == 0 {
		return nil, fmt.Errorf("invalid windows process id")
	}
	if backend == nil {
		return nil, fmt.Errorf("windows runtime job backend is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	handle, identity, err := backend.openProcess(ctx, pid)
	if err != nil {
		return nil, fmt.Errorf("open windows process identity: %w", err)
	}
	if identity.PID != pid || !validWindowsProcessIdentity(identity) {
		return nil, errors.Join(fmt.Errorf("windows process identity is incomplete"), backend.closeProcess(handle))
	}
	return &WindowsProcessIdentity{backend: backend, handle: handle, identity: identity}, nil
}

func validWindowsProcessIdentity(identity WindowsProcessIdentityValue) bool {
	return identity.PID != 0 && identity.CreationFiletime != 0 && identity.ImagePath != "" && identity.SID != ""
}

// Name returns the exact global Job name bound to this capability.
func (j *WindowsRuntimeJob) Name() string {
	if j == nil {
		return ""
	}
	return j.name
}

// OwnerIdentity returns the current-process identity retained by a Job created
// with CreateWindowsRuntimeJob. Opened observer Jobs return ok=false.
// The immutable observation remains available after Close and grants no handle authority.
func (j *WindowsRuntimeJob) OwnerIdentity() (identity WindowsProcessIdentityValue, ok bool) {
	if j == nil || j.owner == nil {
		return WindowsProcessIdentityValue{}, false
	}
	return j.owner.Identity(), true
}

// ActiveProcesses queries the kernel's current ActiveProcesses count.
func (j *WindowsRuntimeJob) ActiveProcesses(ctx context.Context) (uint32, error) {
	if j == nil {
		return 0, fmt.Errorf("windows runtime job is nil")
	}
	finish, err := j.lifetime.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer finish()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	active, err := j.backend.activeProcesses(ctx, j.handle)
	if err != nil {
		return 0, fmt.Errorf("query windows runtime job active processes: %w", err)
	}
	return active, nil
}

// VerifyMember derives a termination capability from duplicated held handles
// after the kernel confirms that process is a member of this Job.
func (j *WindowsRuntimeJob) VerifyMember(ctx context.Context, process *WindowsProcessIdentity) (*WindowsJobTermination, error) {
	if j == nil || process == nil {
		return nil, fmt.Errorf("windows runtime verification capability is nil")
	}
	finishJob, err := j.lifetime.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finishJob()
	finishProcess, err := process.lifetime.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finishProcess()
	if j.backend != process.backend {
		return nil, fmt.Errorf("windows runtime capabilities use different backends")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	jobHandle, err := j.backend.duplicateJob(ctx, j.handle)
	if err != nil {
		return nil, fmt.Errorf("duplicate windows runtime job handle: %w", err)
	}
	closeJobOnError := func(cause error) error {
		return errors.Join(cause, j.backend.closeJob(jobHandle))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeJobOnError(err)
	}
	processHandle, err := j.backend.duplicateProcess(ctx, process.handle)
	if err != nil {
		return nil, closeJobOnError(fmt.Errorf("duplicate windows process identity handle: %w", err))
	}
	closeAllOnError := func(cause error) error {
		return errors.Join(cause, j.backend.closeProcess(processHandle), j.backend.closeJob(jobHandle))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeAllOnError(err)
	}
	inJob, err := j.backend.isProcessInJob(ctx, processHandle, jobHandle)
	if err != nil {
		return nil, closeAllOnError(fmt.Errorf("verify windows runtime job member: %w", err))
	}
	if !inJob {
		return nil, closeAllOnError(ErrWindowsRuntimeJobMembership)
	}
	return &WindowsJobTermination{backend: j.backend, job: jobHandle, process: processHandle}, nil
}

// Close releases all native handles owned by the Job. A daemon-owned Job must
// remain open for the daemon lifetime because its close policy terminates all
// remaining members. Repeat calls return nil.
func (j *WindowsRuntimeJob) Close() error {
	if j == nil {
		return nil
	}
	return j.lifetime.closeWith(func() error {
		var ownerErr error
		if j.owner != nil {
			ownerErr = j.owner.Close()
		}
		var jobErr error
		if j.handle != 0 {
			jobErr = j.backend.closeJob(j.handle)
			j.handle = 0
		}
		return errors.Join(ownerErr, jobErr)
	})
}

// WindowsJobTermination owns duplicated Job and process handles that were
// verified together. It is the only API that can terminate a runtime Job.
type WindowsJobTermination struct {
	lifetime windowsNativeCapabilityLifetime
	backend  windowsRuntimeJobBackend
	job      windowsJobHandle
	process  windowsProcessHandle
}

// Terminate terminates every process in the verified held Job.
func (c *WindowsJobTermination) Terminate(ctx context.Context, exitCode uint32) error {
	if c == nil {
		return fmt.Errorf("windows job termination capability is nil")
	}
	finish, err := c.lifetime.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err := ctx.Err(); err != nil {
		return err
	}
	inJob, err := c.backend.isProcessInJob(ctx, c.process, c.job)
	if err != nil {
		return fmt.Errorf("reverify windows runtime job member: %w", err)
	}
	if !inJob {
		return ErrWindowsRuntimeJobMembership
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.backend.terminateJob(ctx, c.job, exitCode); err != nil {
		return fmt.Errorf("terminate verified windows runtime job: %w", err)
	}
	return nil
}

// Close releases the duplicated Job and process handles. Repeat calls return nil.
func (c *WindowsJobTermination) Close() error {
	if c == nil {
		return nil
	}
	return c.lifetime.closeWith(func() error {
		var processErr, jobErr error
		if c.process != 0 {
			processErr = c.backend.closeProcess(c.process)
			c.process = 0
		}
		if c.job != 0 {
			jobErr = c.backend.closeJob(c.job)
			c.job = 0
		}
		return errors.Join(processErr, jobErr)
	})
}

func isWindowsRuntimeJobName(name string) bool {
	return strings.HasPrefix(name, windowsRuntimeJobPrefix) && isLowerHex32(strings.TrimPrefix(name, windowsRuntimeJobPrefix))
}

func isLowerHex32(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

type windowsRuntimeJobBackend interface {
	createProtectedJob(context.Context, string, bool) (windowsJobHandle, bool, error)
	openJob(context.Context, string) (windowsJobHandle, error)
	configureJob(context.Context, windowsJobHandle, uint32) error
	verifyJob(context.Context, windowsJobHandle) error
	openCurrentProcess(context.Context) (windowsProcessHandle, WindowsProcessIdentityValue, error)
	openProcess(context.Context, uint32) (windowsProcessHandle, WindowsProcessIdentityValue, error)
	assignProcess(context.Context, windowsJobHandle, windowsProcessHandle) error
	isProcessInJob(context.Context, windowsProcessHandle, windowsJobHandle) (bool, error)
	activeProcesses(context.Context, windowsJobHandle) (uint32, error)
	processHasExited(context.Context, windowsProcessHandle) (bool, error)
	duplicateJob(context.Context, windowsJobHandle) (windowsJobHandle, error)
	duplicateProcess(context.Context, windowsProcessHandle) (windowsProcessHandle, error)
	terminateJob(context.Context, windowsJobHandle, uint32) error
	closeJob(windowsJobHandle) error
	closeProcess(windowsProcessHandle) error
}

type windowsRuntimeJobNativeBackend struct{}

func (windowsRuntimeJobNativeBackend) createProtectedJob(ctx context.Context, name string, inherit bool) (windowsJobHandle, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	sd, err := windows.SecurityDescriptorFromString(windowsRuntimeJobSDDL)
	if err != nil {
		return 0, false, err
	}
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	if inherit {
		attributes.InheritHandle = 1
	}
	handle, _, callErr := procCreateJobObjectW.Call(uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(namePointer)))
	runtime.KeepAlive(sd)
	runtime.KeepAlive(namePointer)
	if handle == 0 {
		return 0, false, normalizeWindowsCallError(callErr)
	}
	return windowsJobHandle(handle), errors.Is(callErr, windows.ERROR_ALREADY_EXISTS), nil
}

func (windowsRuntimeJobNativeBackend) openJob(ctx context.Context, name string) (windowsJobHandle, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	handle, _, callErr := procOpenJobObjectW.Call(windowsJobQuery|windowsJobTerminate|windows.READ_CONTROL, 0, uintptr(unsafe.Pointer(namePointer)))
	runtime.KeepAlive(namePointer)
	if handle == 0 {
		return 0, normalizeWindowsCallError(callErr)
	}
	return windowsJobHandle(handle), nil
}

func (windowsRuntimeJobNativeBackend) configureJob(ctx context.Context, handle windowsJobHandle, flags uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: flags},
	}
	if _, err := windows.SetInformationJobObject(windows.Handle(handle), windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
		return err
	}
	return windowsRuntimeJobNativeBackend{}.verifyJob(ctx, handle)
}

func (windowsRuntimeJobNativeBackend) verifyJob(ctx context.Context, handle windowsJobHandle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(handle), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	protected, err := windowsRuntimeProtectedPolicy(sd, 0x1f003f) // JOB_OBJECT_ALL_ACCESS.
	if err != nil {
		return err
	}
	if !protected {
		return fmt.Errorf("windows runtime job has unsafe permissions")
	}
	var observed windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(windows.Handle(handle), int32(windows.JobObjectExtendedLimitInformation), uintptr(unsafe.Pointer(&observed)), uint32(unsafe.Sizeof(observed)), nil); err != nil {
		return err
	}
	observedFlags := observed.BasicLimitInformation.LimitFlags
	if observedFlags&windowsJobLimitKillOnClose == 0 || observedFlags&(windowsJobLimitBreakaway|windowsJobLimitSilentBreakaway) != 0 {
		return fmt.Errorf("windows runtime job has unsafe limit flags %#x", observedFlags)
	}
	return nil
}

func (b windowsRuntimeJobNativeBackend) openCurrentProcess(ctx context.Context) (windowsProcessHandle, WindowsProcessIdentityValue, error) {
	if err := ctx.Err(); err != nil {
		return 0, WindowsProcessIdentityValue{}, err
	}
	return b.openProcessWithAccess(ctx, windows.GetCurrentProcessId(), windowsDaemonProcessAccess)
}

func (b windowsRuntimeJobNativeBackend) openProcess(ctx context.Context, pid uint32) (windowsProcessHandle, WindowsProcessIdentityValue, error) {
	return b.openProcessWithAccess(ctx, pid, windowsObservedProcessAccess)
}

func (windowsRuntimeJobNativeBackend) openProcessWithAccess(ctx context.Context, pid, access uint32) (windowsProcessHandle, WindowsProcessIdentityValue, error) {
	if err := ctx.Err(); err != nil {
		return 0, WindowsProcessIdentityValue{}, err
	}
	handle, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		return 0, WindowsProcessIdentityValue{}, err
	}
	identity, err := nativeWindowsProcessMetadata(ctx, handle, pid)
	if err != nil {
		return 0, WindowsProcessIdentityValue{}, errors.Join(err, windows.CloseHandle(handle))
	}
	return windowsProcessHandle(handle), identity, nil
}

func (windowsRuntimeJobNativeBackend) assignProcess(ctx context.Context, job windowsJobHandle, process windowsProcessHandle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return windows.AssignProcessToJobObject(windows.Handle(job), windows.Handle(process))
}

func (windowsRuntimeJobNativeBackend) isProcessInJob(ctx context.Context, process windowsProcessHandle, job windowsJobHandle) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var result uint32
	ok, _, callErr := procIsProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		return false, normalizeWindowsCallError(callErr)
	}
	return result != 0, nil
}

type windowsJobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func (windowsRuntimeJobNativeBackend) activeProcesses(ctx context.Context, job windowsJobHandle) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var information windowsJobBasicAccountingInformation
	if err := windows.QueryInformationJobObject(windows.Handle(job), int32(windows.JobObjectBasicAccountingInformation), uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information)), nil); err != nil {
		return 0, err
	}
	return information.ActiveProcesses, nil
}

func (windowsRuntimeJobNativeBackend) processHasExited(ctx context.Context, process windowsProcessHandle) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	result, err := windows.WaitForSingleObject(windows.Handle(process), 0)
	if err != nil {
		return false, err
	}
	switch result {
	case windows.WAIT_OBJECT_0:
		return true, nil
	case uint32(windows.WAIT_TIMEOUT):
		return false, nil
	default:
		return false, fmt.Errorf("unexpected process wait result %#x", result)
	}
}

func (b windowsRuntimeJobNativeBackend) duplicateJob(ctx context.Context, job windowsJobHandle) (windowsJobHandle, error) {
	handle, err := b.duplicateHandle(ctx, windows.Handle(job))
	return windowsJobHandle(handle), err
}

func (b windowsRuntimeJobNativeBackend) duplicateProcess(ctx context.Context, process windowsProcessHandle) (windowsProcessHandle, error) {
	handle, err := b.duplicateHandle(ctx, windows.Handle(process))
	return windowsProcessHandle(handle), err
}

func (windowsRuntimeJobNativeBackend) duplicateHandle(ctx context.Context, source windows.Handle) (windows.Handle, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	current := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(current, source, current, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return 0, err
	}
	return duplicate, nil
}

func (windowsRuntimeJobNativeBackend) terminateJob(ctx context.Context, job windowsJobHandle, exitCode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return windows.TerminateJobObject(windows.Handle(job), exitCode)
}

func (windowsRuntimeJobNativeBackend) closeJob(job windowsJobHandle) error {
	return windows.CloseHandle(windows.Handle(job))
}

func (windowsRuntimeJobNativeBackend) closeProcess(process windowsProcessHandle) error {
	return windows.CloseHandle(windows.Handle(process))
}

func nativeWindowsProcessMetadata(ctx context.Context, handle windows.Handle, pid uint32) (WindowsProcessIdentityValue, error) {
	if err := ctx.Err(); err != nil {
		return WindowsProcessIdentityValue{}, err
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return WindowsProcessIdentityValue{}, err
	}
	if err := ctx.Err(); err != nil {
		return WindowsProcessIdentityValue{}, err
	}
	buffer := make([]uint16, windowsProcessImageBufferLength)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return WindowsProcessIdentityValue{}, err
	}
	if err := ctx.Err(); err != nil {
		return WindowsProcessIdentityValue{}, err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return WindowsProcessIdentityValue{}, err
	}
	userToken, userErr := token.GetTokenUser()
	closeErr := token.Close()
	if userErr != nil || closeErr != nil {
		return WindowsProcessIdentityValue{}, errors.Join(userErr, closeErr)
	}
	if userToken == nil || userToken.User.Sid == nil {
		return WindowsProcessIdentityValue{}, fmt.Errorf("windows process token has no user SID")
	}
	return WindowsProcessIdentityValue{
		PID:              pid,
		CreationFiletime: uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime),
		ImagePath:        windows.UTF16ToString(buffer[:size]),
		SID:              userToken.User.Sid.String(),
	}, nil
}

func normalizeWindowsCallError(err error) error {
	if err == nil || err == syscall.Errno(0) {
		return syscall.EINVAL
	}
	return err
}
