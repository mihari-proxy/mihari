//go:build darwin

package tundetect

import (
	"context"
	"os/exec"
	"strings"
)

func detect(ctx context.Context) (Detection, error) {
	if err := ctx.Err(); err != nil {
		return Detection{}, err
	}
	tun, err := enumerateDarwinTun(ctx)
	if err != nil {
		return Detection{}, err
	}
	mihomo, err := enumerateDarwinMihomo(ctx)
	if err != nil {
		return Detection{}, err
	}
	return Detection{TunInterfaces: tun, MihomoProcesses: mihomo}, nil
}

// enumerateDarwinTun lists utun interfaces from "ifconfig -l".
func enumerateDarwinTun(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "ifconfig", "-l").Output()
	if err != nil {
		return nil, err
	}
	var tun []string
	for _, name := range strings.Fields(string(out)) {
		if strings.HasPrefix(name, "utun") {
			tun = append(tun, name)
		}
	}
	return tun, nil
}

// enumerateDarwinMihomo lists mihomo processes via "ps -eo pid=,ppid=,comm=".
// PID and parent PID precede comm so command-path whitespace is preserved.
func enumerateDarwinMihomo(ctx context.Context) ([]Process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	var procs []Process
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, pid, ppid, ok := parseDarwinProcessLine(line)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(name), "mihomo") {
			procs = append(procs, Process{Name: name, PID: pid, ParentPID: ppid, Path: darwinProcessPath(name)})
		}
	}
	return procs, nil
}

func darwinProcessPath(comm string) string {
	if strings.Contains(comm, "/") {
		return comm
	}
	return ""
}
