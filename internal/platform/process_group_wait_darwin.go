//go:build darwin

package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// DarwinWaitChildExit waits for an owned child to exit without reaping it. The
// caller must retain the child and exclude every other wait/reap until this
// returns. After success it must close its signal admission before Cmd.Wait.
// An error does not prove exit. Cancellation belongs to the child's owner,
// which may still signal the unreaped child while this call blocks.
func DarwinWaitChildExit(pid int) (resultErr error) {
	if pid <= 1 || int64(pid) > 1<<31-1 {
		return errProcessGroupUnknown
	}
	queue, err := unix.Kqueue()
	if err != nil {
		return fmt.Errorf("observe child exit: %w", err)
	}
	unix.CloseOnExec(queue)
	defer func() { resultErr = errors.Join(resultErr, unix.Close(queue)) }()
	// NOTE_EXIT is independent of SIGSTOP/SIGCONT. Darwin's historical waitid
	// WEXITED behavior is unsuitable for an opaque siginfo success check.
	change := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT | unix.EV_RECEIPT, Fflags: unix.NOTE_EXIT}}
	var events [1]unix.Kevent_t
	for {
		n, err := unix.Kevent(queue, change, events[:], nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.ESRCH) {
			// No concurrent reaper is permitted: a disappeared live proc can
			// only be our already-exited, still-unreaped child, not a reused PID.
			return nil
		}
		if err != nil {
			return fmt.Errorf("register child exit observation: %w", err)
		}
		if n != 1 || events[0].Ident != uint64(pid) || events[0].Filter != unix.EVFILT_PROC || events[0].Flags&unix.EV_ERROR == 0 {
			return errProcessGroupUnknown
		}
		if events[0].Data == int64(unix.ESRCH) {
			return nil
		}
		if events[0].Data != 0 {
			return fmt.Errorf("register child exit observation: %w", unix.Errno(events[0].Data))
		}
		break
	}
	for {
		n, err := unix.Kevent(queue, nil, events[:], nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("wait for child exit: %w", err)
		}
		if n != 1 || events[0].Ident != uint64(pid) || events[0].Filter != unix.EVFILT_PROC || events[0].Flags&unix.EV_ERROR != 0 || events[0].Fflags&unix.NOTE_EXIT == 0 {
			return errProcessGroupUnknown
		}
		return nil
	}
}
