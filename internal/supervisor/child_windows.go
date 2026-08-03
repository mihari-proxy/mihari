//go:build windows

package supervisor

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobOnce sync.Once
	job     windows.Handle
	jobErr  error
)

func prepareChild(*exec.Cmd) {}

func trackChild(command *exec.Cmd) error {
	if command.Process == nil {
		return fmt.Errorf("process not started")
	}
	handle, err := childJob()
	if err != nil {
		return err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		return fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(handle, process); err != nil {
		return fmt.Errorf("assign process to job: %w", err)
	}
	return nil
}

func childJob() (windows.Handle, error) {
	jobOnce.Do(func() {
		handle, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = fmt.Errorf("create job object: %w", err)
			return
		}
		information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE},
		}
		if _, err := windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&information)), uint32(unsafe.Sizeof(information))); err != nil {
			windows.CloseHandle(handle)
			jobErr = fmt.Errorf("configure job object: %w", err)
			return
		}
		job = handle
	})
	return job, jobErr
}

func terminateChild(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

func killChild(command *exec.Cmd) error { return terminateChild(command) }
