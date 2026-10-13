package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func findRunuser(lookup func(string) (string, error)) (string, error) {
	var failures []error
	for _, path := range []string{
		"/usr/sbin/runuser", "/usr/bin/runuser", "/sbin/runuser", "/bin/runuser",
		"/usr/local/sbin/runuser", "/usr/local/bin/runuser",
		"/run/current-system/sw/bin/runuser",
		"/run/current-system/profile/sbin/runuser", "/run/current-system/profile/bin/runuser",
	} {
		found, err := lookup(path)
		if err == nil {
			return found, nil
		}
		failures = append(failures, err)
	}
	return "", errors.Join(failures...)
}

// sshAncestorFrom reports whether pid or one of its parents is sshd.
func sshAncestorFrom(pid int, read func(int) (string, int, error)) bool {
	if read == nil {
		return false
	}
	seen := map[int]struct{}{}
	for range 32 {
		if pid <= 1 {
			return false
		}
		if _, ok := seen[pid]; ok {
			return false
		}
		seen[pid] = struct{}{}
		comm, ppid, err := read(pid)
		if err != nil {
			return false
		}
		if comm == "sshd" {
			return true
		}
		pid = ppid
	}
	return false
}

func validNumericID(value string) bool {
	if value == "" || len(value) > 10 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validAccountName(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

func waylandDisplay(runtimeDir string) string {
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return ""
	}
	preferred := os.Getenv("WAYLAND_DISPLAY")
	var display string
	var sockets int
	for _, entry := range entries {
		name := entry.Name()
		if !validWaylandDisplay(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		if name == preferred || filepath.Join(runtimeDir, name) == preferred {
			return name
		}
		sockets++
		display = name
	}
	if sockets == 1 {
		return display
	}
	return ""
}

func validWaylandDisplay(name string) bool {
	rest, ok := strings.CutPrefix(name, "wayland-")
	return ok && validNumericID(rest)
}

// userWaylandArgs builds a root-to-user wl-copy command. ok is false when the
// session cannot be addressed, and the caller should use the direct clipboard.
func userWaylandArgs(euid int, uid, user, display, wlCopy, runuser string) ([]string, bool) {
	if euid != 0 || !validNumericID(uid) || !validAccountName(user) || !validWaylandDisplay(display) || wlCopy == "" || runuser == "" {
		return nil, false
	}
	return []string{
		runuser, "-u", user, "--", "env",
		"XDG_RUNTIME_DIR=/run/user/" + uid,
		"WAYLAND_DISPLAY=" + display,
		wlCopy,
	}, true
}
