//go:build linux

package tundetect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func detect(ctx context.Context) (Detection, error) {
	if err := ctx.Err(); err != nil {
		return Detection{}, err
	}
	tun, err := enumerateLinuxTun()
	if err != nil {
		return Detection{}, err
	}
	mihomo, err := enumerateLinuxMihomo()
	if err != nil {
		return Detection{}, err
	}
	return Detection{TunInterfaces: tun, MihomoProcesses: mihomo}, nil
}

// enumerateLinuxTun lists net devices that expose tun_flags, i.e. adapters
// created by the kernel tun/tap driver which mihomo uses on Linux.
func enumerateLinuxTun() ([]string, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, err
	}
	var tun []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join("/sys/class/net", e.Name(), "tun_flags")); err == nil {
			tun = append(tun, e.Name())
		}
	}
	return tun, nil
}

// enumerateLinuxMihomo lists processes whose comm reads as mihomo.
func enumerateLinuxMihomo() ([]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var procs []string
	for _, e := range entries {
		if !e.IsDir() || !isAllDigits(e.Name()) {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if strings.Contains(strings.ToLower(name), "mihomo") {
			procs = append(procs, name+" ("+e.Name()+")")
		}
	}
	return procs, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
