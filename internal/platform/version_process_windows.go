package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var createVersionProcessWithToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateProcessWithTokenW")

// runWindowsVersionWithToken keeps the updater as parent. Using Explorer as
// parent would trigger older Mihari binaries' Cobra double-click guard instead
// of self version. This API uses the caller's existing impersonation privilege;
// it neither enables privileges nor falls back to elevated execution.
func runWindowsVersionWithToken(ctx context.Context, cmd *exec.Cmd, token windows.Token) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeVersionFiles(stdin)) }()
	stdout, stdoutChild, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeVersionFiles(stdout, stdoutChild)) }()
	stderr, stderrChild, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeVersionFiles(stderr, stderrChild)) }()
	process, err := startWindowsVersionWithToken(cmd, token, stdin, stdoutChild, stderrChild)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(process)) }()
	closeErr := closeVersionFiles(stdin, stdoutChild, stderrChild)
	// Both copy workers are joined, including after cancellation or inherited
	// pipe timeout. The caller's writers enforce the fixed output size limits.
	copies := make(chan error, 2)
	for _, stream := range []struct {
		source      *os.File
		destination io.Writer
	}{{stdout, cmd.Stdout}, {stderr, cmd.Stderr}} {
		go func() {
			destination := stream.destination
			if destination == nil {
				destination = io.Discard
			}
			_, copyErr := io.Copy(destination, stream.source)
			copies <- copyErr
		}()
	}
	waited := make(chan error, 1)
	go func() {
		_, waitErr := windows.WaitForSingleObject(process, windows.INFINITE)
		waited <- waitErr
	}()
	select {
	case err = <-waited:
	case <-ctx.Done():
		// ERROR_ACCESS_DENIED also means the process already exited; Wait below
		// resolves that race before its handle or pipes can be released.
		killErr := windows.TerminateProcess(process, 1)
		if errors.Is(killErr, windows.ERROR_ACCESS_DENIED) {
			killErr = nil
		}
		err = errors.Join(ctx.Err(), killErr, <-waited)
	}
	var exitCode uint32
	err = errors.Join(err, closeErr, windows.GetExitCodeProcess(process, &exitCode))
	if exitCode != 0 {
		err = errors.Join(err, fmt.Errorf("version process exited with code %d", exitCode))
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	for remaining := 2; remaining > 0; remaining-- {
		select {
		case copyErr := <-copies:
			err = errors.Join(err, copyErr)
		case <-timer.C:
			err = errors.Join(err, exec.ErrWaitDelay, closeVersionFiles(stdout, stderr))
			for ; remaining > 0; remaining-- {
				err = errors.Join(err, <-copies)
			}
		}
	}
	return err
}

func closeVersionFiles(files ...*os.File) error {
	var result error
	for _, file := range files {
		// Deferred failure cleanup overlaps the explicit pipe-end closures.
		if err := file.Close(); !errors.Is(err, os.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func startWindowsVersionWithToken(cmd *exec.Cmd, token windows.Token, files ...*os.File) (process windows.Handle, err error) {
	application, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return 0, err
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return 0, err
	}
	directory, err := windows.UTF16PtrFromString(cmd.Dir)
	if err != nil {
		return 0, err
	}
	env := slices.Clone(cmd.Env)
	for _, value := range env {
		if strings.ContainsRune(value, 0) {
			return 0, os.ErrInvalid
		}
	}
	slices.SortFunc(env, func(a, b string) int { return strings.Compare(strings.ToUpper(a), strings.ToUpper(b)) })
	block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	var handles []windows.Handle
	defer func() {
		for _, handle := range handles {
			err = errors.Join(err, windows.CloseHandle(handle))
		}
		if err != nil && process != 0 {
			killErr := windows.TerminateProcess(process, 1)
			_, waitErr := windows.WaitForSingleObject(process, windows.INFINITE)
			err = errors.Join(err, killErr, waitErr, windows.CloseHandle(process))
			process = 0
		}
	}()
	for _, file := range files {
		var handle windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &handle, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return 0, err
		}
		handles = append(handles, handle)
	}
	// CreateProcessWithTokenW transfers the three standard handles without
	// general handle inheritance. It does not support HANDLE_LIST attributes.
	startup := windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW
	startup.ShowWindow = windows.SW_HIDE
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	var info windows.ProcessInformation
	ok, _, callErr := createVersionProcessWithToken.Call(uintptr(token), 0, uintptr(unsafe.Pointer(application)), uintptr(unsafe.Pointer(commandLine)), windows.CREATE_NO_WINDOW|windows.CREATE_UNICODE_ENVIRONMENT, uintptr(unsafe.Pointer(&block[0])), uintptr(unsafe.Pointer(directory)), uintptr(unsafe.Pointer(&startup)), uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(handles)
	runtime.KeepAlive(block)
	if ok == 0 {
		return 0, fmt.Errorf("start version process with non-admin token: %w", callErr)
	}
	if err := windows.CloseHandle(info.Thread); err != nil {
		killErr := windows.TerminateProcess(info.Process, 1)
		_, waitErr := windows.WaitForSingleObject(info.Process, windows.INFINITE)
		return 0, errors.Join(err, killErr, waitErr, windows.CloseHandle(info.Process))
	}
	return info.Process, nil
}
