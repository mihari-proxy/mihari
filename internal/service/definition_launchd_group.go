package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"syscall"
)

const darwinGroupPrefix = "darwin-pgid-v1:"
const darwinProcArgsMax = 1 << 20

func darwinGroupToken(id ProcessIdentity) (string, error) {
	if id.PID <= 1 || id.StartUnix <= 0 || id.StartUsec >= 1000000 || id.BootID == "" || len(id.BootID) > 64 {
		return "", invalidServiceState("service process group identity is unknown")
	}
	for _, ch := range id.BootID {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '.' || ch == '-' || ch == '_') {
			return "", invalidServiceState("service process group identity is unknown")
		}
	}
	return darwinGroupPrefix + id.BootID + ":" + strconv.Itoa(id.PID) + ":" + strconv.FormatInt(id.StartUnix, 10) + ":" + strconv.FormatUint(uint64(id.StartUsec), 10), nil
}

func parseDarwinGroup(token string) (ProcessIdentity, error) {
	if len(token) > 160 || !strings.HasPrefix(token, darwinGroupPrefix) {
		return ProcessIdentity{}, invalidServiceState("service process group identity is unknown")
	}
	parts := strings.Split(strings.TrimPrefix(token, darwinGroupPrefix), ":")
	if len(parts) != 4 {
		return ProcessIdentity{}, invalidServiceState("service process group identity is unknown")
	}
	pid, e1 := strconv.ParseInt(parts[1], 10, 32)
	seconds, e2 := strconv.ParseInt(parts[2], 10, 64)
	micros, e3 := strconv.ParseUint(parts[3], 10, 32)
	id := ProcessIdentity{PID: int(pid), BootID: parts[0], StartUnix: seconds, StartUsec: uint32(micros), Group: token}
	canonical, err := darwinGroupToken(id)
	if e1 != nil || e2 != nil || e3 != nil || err != nil || canonical != token {
		return ProcessIdentity{}, invalidServiceState("service process group identity is unknown")
	}
	return id, nil
}

func validateDarwinGroup(id ProcessIdentity) error {
	parsed, err := parseDarwinGroup(id.Group)
	if err != nil || !sameLaunchdProcess(parsed, id) {
		return invalidServiceState("service process group identity is unknown")
	}
	return nil
}

func sameLaunchdProcess(a, b ProcessIdentity) bool {
	return a.PID == b.PID && a.BootID == b.BootID && a.StartUnix == b.StartUnix && a.StartUsec == b.StartUsec
}

// parseDarwinProcArgs reads exactly argc argv strings. The trailing environment
// is deliberately not searched for a marker or included in returned errors.
func parseDarwinProcArgs(raw []byte) ([]string, error) {
	if len(raw) < 5 || len(raw) > darwinProcArgsMax {
		return nil, invalidServiceState("service process arguments are unknown")
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc < 1 || argc > 8 {
		return nil, invalidServiceState("service process arguments are unknown")
	}
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end <= 0 {
		return nil, invalidServiceState("service process arguments are unknown")
	}
	rest = bytes.TrimLeft(rest[end+1:], "\x00")
	argv := make([]string, 0, argc)
	for range argc {
		end = bytes.IndexByte(rest, 0)
		if end <= 0 {
			return nil, invalidServiceState("service process arguments are unknown")
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv, nil
}

type darwinProcessObservation struct {
	identity ProcessIdentity
	group    int
}

func identifyLaunchdProcess(ctx context.Context, pid int, observe func(context.Context, int) (darwinProcessObservation, error), arguments func(int) ([]byte, error)) (ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ProcessIdentity{}, err
	}
	before, err := observe(ctx, pid)
	if err != nil || before.identity.PID == 0 {
		return before.identity, err
	}
	if before.identity.PID != pid {
		return ProcessIdentity{}, invalidServiceState("service process identity changed")
	}
	raw, err := arguments(pid)
	if err != nil {
		return ProcessIdentity{}, invalidServiceState("service process arguments are unknown")
	}
	argv, err := parseDarwinProcArgs(raw)
	if err != nil {
		return ProcessIdentity{}, err
	}
	after, err := observe(ctx, pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if !sameLaunchdProcess(before.identity, after.identity) || before.group != after.group {
		return ProcessIdentity{}, invalidServiceState("service process identity changed")
	}
	if err := ctx.Err(); err != nil {
		return ProcessIdentity{}, err
	}
	id := before.identity
	if before.group == pid && len(argv) == 4 && argv[3] == "--launchd-process-group" && rejectStrangeLaunchdExec(argv) == nil {
		id.Group, err = darwinGroupToken(id)
	}
	return id, err
}

func observeLaunchdGroupExit(ctx context.Context, group string, boot func(context.Context) (string, error), observe func(context.Context, int) (darwinProcessObservation, error), probe func(int) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, err := parseDarwinGroup(group)
	if err != nil {
		return false, err
	}
	currentBoot, err := boot(ctx)
	if err != nil {
		return false, err
	}
	if currentBoot == "" {
		return false, invalidServiceState("service process boot is unknown")
	}
	if currentBoot != id.BootID {
		return true, nil
	}
	current, err := observe(ctx, id.PID)
	if err != nil {
		return false, err
	}
	if current.identity.PID != 0 && (!sameLaunchdProcess(current.identity, id) || current.group != id.PID) {
		return false, invalidServiceState("service process group identity changed")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err = probe(-id.PID)
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	if err != nil {
		return false, invalidServiceState("service process group is unknown")
	}
	return false, nil
}
