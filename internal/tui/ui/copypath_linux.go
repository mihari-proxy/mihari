//go:build linux

package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	argv, err := linuxClipboardCommand(os.Getenv("WAYLAND_DISPLAY") != "", exec.LookPath)
	if err != nil {
		return err
	}
	return runUserClipboard(argv, text)
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
	runuser, runErr := findRunuser(exec.LookPath)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return runUserClipboardContext(ctx, argv, text)
}

func runUserClipboardContext(ctx context.Context, argv []string, text string) error {
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
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := cmd.Run()
	if runErr != nil && ctx.Err() != nil {
		runErr = errors.Join(runErr, ctx.Err())
	}
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

// linuxClipboardCommand preserves the pinned clipboard backend preference.
// Selection is injected so tests never invoke the user's desktop utilities.
func linuxClipboardCommand(wayland bool, lookup func(string) (string, error)) ([]string, error) {
	type backend struct {
		args      []string
		companion string
	}
	backends := []backend{}
	if wayland {
		backends = append(backends, backend{[]string{"wl-copy"}, "wl-paste"})
	}
	backends = append(backends, backend{[]string{"xclip", "-in", "-selection", "clipboard"}, ""}, backend{[]string{"xsel", "--input", "--clipboard"}, ""}, backend{[]string{"termux-clipboard-set"}, "termux-clipboard-get"}, backend{[]string{"clip.exe"}, "powershell.exe"})
	for _, b := range backends {
		path, err := lookup(b.args[0])
		if err != nil {
			continue
		}
		if b.companion != "" {
			if _, err := lookup(b.companion); err != nil {
				continue
			}
		}
		argv := append([]string(nil), b.args...)
		argv[0] = path
		return argv, nil
	}
	return nil, errors.New("no clipboard utilities available; install xclip, xsel or wl-clipboard")
}
