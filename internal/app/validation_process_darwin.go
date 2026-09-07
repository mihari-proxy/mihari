package app

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
)

func identifyValidationProcess(ctx context.Context, pid int) (ProcessStartIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ProcessStartIdentity{}, err
	}
	boot, err := installBootIdentity()
	if err != nil {
		return ProcessStartIdentity{}, err
	}
	info, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
		return ProcessStartIdentity{}, nil
	}
	records := make([]ProcessStartIdentity, len(info))
	for i, record := range info {
		records[i] = ProcessStartIdentity{PID: int(record.Proc.P_pid), BootID: boot, StartUnix: int64(record.Proc.P_starttime.Sec), StartUsec: uint32(record.Proc.P_starttime.Usec)}
	}
	return validationProcessRecord(pid, records, err)
}
