//go:build linux

package ui

import (
	"errors"
	"fmt"
	"io"
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
	return runUserClipboard(argv, text), true
}

// runUserClipboard runs a clipboard command that may fork a server after the
// parent exits. Stdout and stderr go to a file, not a pipe: wl-copy's
// background server keeps the inherited write end open, and waiting on that
// pipe would block until some other client replaces the clipboard.
func runUserClipboard(argv []string, text string) error {
	if len(argv) == 0 {
		return errors.New("clipboard command is missing")
	}
	output, err := os.CreateTemp("", "mihari-clipboard-*")
	if err != nil {
		return err
	}
	name := output.Name()
	defer func() {
		_ = output.Close()
		_ = os.Remove(name)
	}()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := cmd.Run()
	if _, seekErr := output.Seek(0, io.SeekStart); seekErr != nil {
		if runErr != nil {
			return runErr
		}
		return seekErr
	}
	raw, readErr := io.ReadAll(output)
	detail := strings.TrimSpace(string(raw))
	if runErr != nil {
		if detail == "" {
			return runErr
		}
		return fmt.Errorf("%w: %s", runErr, detail)
	}
	return readErr
}
