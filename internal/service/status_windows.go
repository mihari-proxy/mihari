//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// platformQueryStatus uses limited SCM rights so non-elevated callers can read
// install/run state. kardianos/service often opens with broader access and gets
// "Access is denied" for Status() under a normal user token.
//
// On some locked-down Windows setups even mgr.Connect/OpenService is denied to
// non-admins; fall back to `sc query`, which still works for standard users.
func platformQueryStatus(name string) (StatusKind, error) {
	if st, err := queryStatusViaMgr(name); err == nil {
		return st, nil
	}
	return queryStatusViaSC(name)
}

func queryStatusViaMgr(name string) (StatusKind, error) {
	m, err := mgr.Connect()
	if err != nil {
		return StatusUnknown, err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		if isNotInstalledError(err) || isServiceDoesNotExist(err) {
			return StatusNotInstalled, nil
		}
		return StatusUnknown, err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return StatusUnknown, err
	}
	switch st.State {
	case svc.Running, svc.StartPending, svc.ContinuePending:
		return StatusRunning, nil
	case svc.Stopped, svc.StopPending:
		return StatusStopped, nil
	default:
		return StatusUnknown, nil
	}
}

func queryStatusViaSC(name string) (StatusKind, error) {
	cmd := exec.Command("sc", "query", name)
	// Hide console flash on Windows.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	text := string(out)
	lower := strings.ToLower(text)
	if err != nil {
		// sc returns non-zero when the service does not exist (1060).
		if strings.Contains(lower, "1060") ||
			strings.Contains(lower, "does not exist") ||
			strings.Contains(lower, "specified service does not exist") {
			return StatusNotInstalled, nil
		}
		return StatusUnknown, fmt.Errorf("sc query %s: %w: %s", name, err, strings.TrimSpace(text))
	}
	// STATE              : 1  STOPPED
	// STATE              : 4  RUNNING
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "STATE") {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.Contains(upper, "RUNNING"):
			return StatusRunning, nil
		case strings.Contains(upper, "STOPPED"):
			return StatusStopped, nil
		case strings.Contains(upper, "START_PENDING"), strings.Contains(upper, "CONTINUE_PENDING"):
			return StatusRunning, nil
		case strings.Contains(upper, "STOP_PENDING"):
			return StatusStopped, nil
		default:
			return StatusUnknown, nil
		}
	}
	return StatusUnknown, fmt.Errorf("sc query %s: no STATE line", name)
}

func isServiceDoesNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errno, ok := err.(windows.Errno); ok && errno == windows.ERROR_SERVICE_DOES_NOT_EXIST {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "the specified service does not exist")
}

func isAccessDeniedError(err error) bool {
	if err == nil {
		return false
	}
	if errno, ok := err.(windows.Errno); ok && errno == windows.ERROR_ACCESS_DENIED {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "access is denied") || strings.Contains(lower, "access denied")
}
