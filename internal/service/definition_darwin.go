package service

import (
	"context"
	"errors"
	"fmt"

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

func (darwinProcessTree) Empty(ctx context.Context, _ string) (bool, error) {
	return false, invalidServiceState("service process tree is unknown")
}

func (darwinProcessTree) SignalGroup(context.Context, string, string) error {
	return invalidServiceState("service process tree is unknown")
}

func (t darwinProcessTree) Identify(_ context.Context, pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, nil
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if isDarwinMissingProc(err) {
			return ProcessIdentity{}, nil
		}
		return ProcessIdentity{}, invalidServiceState("service process identity is unknown")
	}
	boot, err := darwinBootID()
	if err != nil {
		return ProcessIdentity{}, err
	}
	start := info.Proc.P_starttime
	return ProcessIdentity{
		PID:       pid,
		BootID:    boot,
		StartUnix: int64(start.Sec),
		StartUsec: uint32(start.Usec),
	}, nil
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
