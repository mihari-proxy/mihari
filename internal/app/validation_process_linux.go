package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func identifyValidationProcess(ctx context.Context, pid int) (ProcessStartIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ProcessStartIdentity{}, err
	}
	boot, err := installBootIdentity()
	if err != nil {
		return ProcessStartIdentity{}, err
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return ProcessStartIdentity{}, nil
	}
	if err != nil {
		return ProcessStartIdentity{}, err
	}
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return ProcessStartIdentity{}, errValidationHandshake
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return ProcessStartIdentity{}, errValidationHandshake
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return ProcessStartIdentity{}, err
	}
	return ProcessStartIdentity{PID: pid, BootID: boot, StartUnix: start}, nil
}
