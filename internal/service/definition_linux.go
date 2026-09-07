package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"
)

// NewSystemdAdapter constructs the production Linux definition adapter.
func NewSystemdAdapter(runner CommandRunner, hook ActionHook) *SystemdAdapter {
	if runner == nil {
		runner = OSCommandRunner{}
	}
	return newSystemdAdapter(systemdConfig{
		Runner: runner,
		Files:  osDefinitionStore{},
		Tree:   linuxCgroupTree{root: defaultCgroupRoot},
		Clock:  realClock{},
		Hook:   hook,
		Paths:  DefaultSystemdPaths(),
	})
}

type linuxCgroupTree struct {
	root string
}

func (t linuxCgroupTree) Empty(_ context.Context, group string) (bool, error) {
	dir, err := t.dir(group)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return classifyCgroupEmpty(false, os.ErrNotExist, nil)
	}
	if err != nil {
		return false, invalidServiceState("service process tree is unknown")
	}
	raw, err := os.ReadFile(path.Join(dir, "cgroup.events"))
	return classifyCgroupEmpty(true, err, raw)
}

func (t linuxCgroupTree) SignalGroup(_ context.Context, group, signal string) error {
	dir, err := t.dir(group)
	if err != nil {
		return err
	}
	if signal == "KILL" {
		if err := os.WriteFile(path.Join(dir, "cgroup.kill"), []byte("1"), 0); err == nil {
			return nil
		}
	}
	syssig, err := unixSignal(signal)
	if err != nil {
		return err
	}
	procs := path.Join(dir, "cgroup.procs")
	pids, err := readCgroupPIDs(procs)
	if err != nil {
		return err
	}
	for _, pid := range pids {
		current, err := readCgroupPIDs(procs)
		if err != nil {
			return err
		}
		if !containsInt(current, pid) {
			continue
		}
		_ = syscall.Kill(pid, syssig)
	}
	return nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (t linuxCgroupTree) Identify(_ context.Context, pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, nil
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ProcessIdentity{}, invalidServiceState("service process identity is unknown")
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return ProcessIdentity{}, nil
	}
	if err != nil {
		return ProcessIdentity{}, invalidServiceState("service process identity is unknown")
	}
	start, err := parseProcStatStart(stat)
	if err != nil {
		return ProcessIdentity{}, err
	}
	return ProcessIdentity{
		PID:       pid,
		BootID:    strings.TrimSpace(string(boot)),
		StartUnix: start,
	}, nil
}

func parseProcStatStart(raw []byte) (int64, error) {
	cut := bytes.LastIndex(raw, []byte(")"))
	if cut < 0 {
		return 0, invalidServiceState("service process identity is unknown")
	}
	fields := strings.Fields(string(raw[cut+1:]))
	if len(fields) < 20 {
		return 0, invalidServiceState("service process identity is unknown")
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, invalidServiceState("service process identity is unknown")
	}
	return start, nil
}

func (t linuxCgroupTree) Lookup(ctx context.Context, id ProcessIdentity) (bool, error) {
	if id.PID <= 0 {
		return false, nil
	}
	if incompleteProcessIdentity(id) {
		return false, invalidServiceState("service process identity is unknown")
	}
	got, err := t.Identify(ctx, id.PID)
	if err != nil {
		return false, err
	}
	if got.PID == 0 {
		return false, nil
	}
	if got.BootID != id.BootID || got.StartUnix != id.StartUnix {
		return false, nil
	}
	return true, nil
}

func (t linuxCgroupTree) SignalIdentity(_ context.Context, id ProcessIdentity, signal string) error {
	alive, err := t.Lookup(context.Background(), id)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}
	syssig, err := unixSignal(signal)
	if err != nil {
		return err
	}
	return syscall.Kill(id.PID, syssig)
}

func (t linuxCgroupTree) dir(group string) (string, error) {
	if group == "" || strings.Contains(group, "..") || !strings.HasPrefix(group, "/") {
		return "", invalidServiceState("service process tree is unknown")
	}
	if !strings.HasSuffix(group, "/"+serviceUnitName) && group != "/"+serviceUnitName {
		return "", invalidServiceState("service process tree is unknown")
	}
	return path.Join(t.root, strings.TrimPrefix(group, "/")), nil
}

func readCgroupPIDs(name string) ([]int, error) {
	raw, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, invalidServiceState("service process tree is unknown")
	}
	var pids []int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil || pid <= 0 {
			return nil, invalidServiceState("service process tree is unknown")
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

func unixSignal(signal string) (syscall.Signal, error) {
	switch signal {
	case "TERM":
		return syscall.SIGTERM, nil
	case "KILL":
		return syscall.SIGKILL, nil
	default:
		return 0, invalidServiceState("service process tree is unknown")
	}
}
