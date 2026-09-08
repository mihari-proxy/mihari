package service

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// NewLaunchdAdapter constructs the production macOS definition adapter.
func NewLaunchdAdapter(runner CommandRunner, hook ActionHook) *LaunchdAdapter {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return newLaunchdAdapter(launchdConfig{
		Runner: runner,
		Files:  osDefinitionStore{},
		Tree:   darwinProcessTree{},
		Clock:  realClock{},
		Hook:   hook,
		Paths:  DefaultLaunchdPaths(),
	})
}

type darwinProcessTree struct{}

func (t darwinProcessTree) Empty(ctx context.Context, group string) (bool, error) {
	// x/sys uses Darwin's POSIX kill entry point. With signal zero, only
	// ESRCH proves the group is absent; EPERM is not an empty-group result.
	return observeLaunchdGroupExit(ctx, group, t.BootIdentity, observeDarwinProcess, func(pgid int) error { return unix.Kill(pgid, 0) })
}

func (darwinProcessTree) BootIdentity(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return darwinBootID()
}

func (darwinProcessTree) SignalGroup(context.Context, string, string) error {
	return invalidServiceState("service process tree is unknown")
}

func (t darwinProcessTree) Identify(ctx context.Context, pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, nil
	}
	return identifyLaunchdProcess(ctx, pid, observeDarwinProcess, func(pid int) ([]byte, error) {
		return readDarwinProcArgs(pid)
	})
}

// Use the same preserved Go/libSystem ABI as the platform fd wrappers, with a
// fixed buffer so a kernel-reported length cannot choose the allocation size.
//
//go:linkname serviceDarwinLibcCall6 syscall.syscall6
//go:uintptrescapes
func serviceDarwinLibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, syscall.Errno)

var libcServiceSysctlAddr uintptr

//go:cgo_import_dynamic mihari_service_sysctl sysctl "/usr/lib/libSystem.B.dylib"

func readDarwinProcArgs(pid int) ([]byte, error) {
	// KERN_PROCARGS2 is 49 in Apple's bsd/sys/sysctl.h. Unlike KERN_PROCARGS,
	// its first native int is argc, followed by executable path and argv.
	const kernProcArgs2 = 49
	mib := [3]int32{unix.CTL_KERN, kernProcArgs2, int32(pid)}
	raw := make([]byte, darwinProcArgsMax)
	size := uintptr(len(raw))
	_, _, errno := serviceDarwinLibcCall6(libcServiceSysctlAddr, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	runtime.KeepAlive(&mib)
	runtime.KeepAlive(raw)
	runtime.KeepAlive(&size)
	if errno != 0 || size > uintptr(len(raw)) || size < 5 {
		return nil, invalidServiceState("service process arguments are unknown")
	}
	return raw[:size], nil
}

func observeDarwinProcess(ctx context.Context, pid int) (darwinProcessObservation, error) {
	if err := ctx.Err(); err != nil {
		return darwinProcessObservation{}, err
	}
	if pid <= 0 {
		return darwinProcessObservation{}, nil
	}
	// A missing KERN_PROC_PID returns a successful zero-byte response. The
	// x/sys single-record convenience wrapper maps that to EIO, so retain the
	// fixed response length here rather than treating arbitrary EIO as exit.
	const kernProc, kernProcPID = 14, 1
	mib := [4]int32{unix.CTL_KERN, kernProc, kernProcPID, int32(pid)}
	var info unix.KinfoProc
	size := uintptr(unix.SizeofKinfoProc)
	_, _, errno := serviceDarwinLibcCall6(libcServiceSysctlAddr, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&size)), 0, 0)
	runtime.KeepAlive(&mib)
	runtime.KeepAlive(&info)
	runtime.KeepAlive(&size)
	if errno != 0 {
		if isDarwinMissingProc(errno) {
			return darwinProcessObservation{}, nil
		}
		return darwinProcessObservation{}, invalidServiceState("service process identity is unknown")
	}
	if size == 0 {
		return darwinProcessObservation{}, nil
	}
	if size != uintptr(unix.SizeofKinfoProc) {
		return darwinProcessObservation{}, invalidServiceState("service process identity is unknown")
	}
	boot, err := darwinBootID()
	if err != nil {
		return darwinProcessObservation{}, err
	}
	if int(info.Proc.P_pid) != pid {
		return darwinProcessObservation{}, invalidServiceState("service process identity changed")
	}
	start := info.Proc.P_starttime
	return darwinProcessObservation{identity: ProcessIdentity{
		PID:       pid,
		BootID:    boot,
		StartUnix: int64(start.Sec),
		StartUsec: uint32(start.Usec),
	}, group: int(info.Eproc.Pgid)}, nil
}

func darwinBootID() (string, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return "", invalidServiceState("service process identity is unknown")
	}
	return fmt.Sprintf("%d.%d", tv.Sec, tv.Usec), nil
}

func (t darwinProcessTree) Lookup(ctx context.Context, id ProcessIdentity) (bool, error) {
	return lookupProcessIdentity(ctx, id, t.Identify)
}

func (t darwinProcessTree) SignalIdentity(ctx context.Context, id ProcessIdentity, signal string) error {
	return signalDarwinIdentity(ctx, id, signal, t.Lookup, unix.Kill)
}

func signalDarwinIdentity(ctx context.Context, id ProcessIdentity, signal string, lookup func(context.Context, ProcessIdentity) (bool, error), kill func(int, unix.Signal) error) error {
	alive, err := lookup(ctx, id)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	syssig, err := darwinSignal(signal)
	if err != nil {
		return err
	}
	err = kill(id.PID, syssig)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func darwinSignal(signal string) (unix.Signal, error) {
	switch signal {
	case "TERM":
		return unix.SIGTERM, nil
	case "KILL":
		return unix.SIGKILL, nil
	default:
		return 0, invalidServiceState("service process tree is unknown")
	}
}

func isDarwinMissingProc(err error) bool {
	return errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT)
}
