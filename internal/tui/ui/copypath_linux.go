//go:build linux

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/atotto/clipboard"
)

func sshAncestor() bool {
	return sshAncestorFrom(os.Getpid(), readProcIdentity)
}

func readProcIdentity(pid int) (string, int, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, err
	}
	text := string(raw)
	open := strings.IndexByte(text, '(')
	close := strings.LastIndexByte(text, ')')
	if open < 0 || close <= open || close+2 >= len(text) {
		return "", 0, fmt.Errorf("unreadable process stat")
	}
	fields := strings.Fields(text[close+2:])
	if len(fields) < 2 {
		return "", 0, fmt.Errorf("unreadable process stat")
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, err
	}
	return text[open+1 : close], ppid, nil
}

func writeFeedbackClipboard(text string) error {
	if err, handled := writeInvokingUserClipboard(text); handled {
		return err
	}
	return clipboard.WriteAll(text)
}

func writeInvokingUserClipboard(text string) (error, bool) {
	uid := strings.TrimSpace(os.Getenv("SUDO_UID"))
	user := strings.TrimSpace(os.Getenv("SUDO_USER"))
	if !validNumericID(uid) || !validAccountName(user) || os.Geteuid() != 0 {
		return nil, false
	}
	runtimeDir := "/run/user/" + uid
	display := waylandDisplay(runtimeDir)
	wlCopy, wlErr := exec.LookPath("wl-copy")
	runuser, runErr := exec.LookPath("runuser")
	argv, ok := userWaylandArgs(os.Geteuid(), uid, user, display, wlCopy, runuser)
	if wlErr != nil || runErr != nil || !ok {
		return nil, false
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return err, true
		}
		return fmt.Errorf("%w: %s", err, detail), true
	}
	return nil, true
}
