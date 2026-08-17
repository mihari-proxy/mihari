//go:build darwin

package tundetect

import (
	"context"
	"os/exec"
	"strconv"
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

// enumerateDarwinMihomo lists mihomo processes via "ps -eo comm=,pid=". The
// comm column may carry a full path; the trailing field is the pid.
func enumerateDarwinMihomo(ctx context.Context) ([]Process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "comm=,pid=").Output()
	if err != nil {
		return nil, err
	}
	var procs []Process
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		comm := fields[0]
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(comm), "mihomo") {
			procs = append(procs, Process{Name: comm, PID: pid})
		}
	}
	return procs, nil
}
