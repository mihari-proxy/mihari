//go:build windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func terminateChild(c *exec.Cmd) error {
	if c.Process == nil {
		return nil
	}
	return c.Process.Kill()
}
func killChild(c *exec.Cmd) error { return terminateChild(c) }

type windowsChild struct {
	*processChild
	mu           sync.Mutex
	job, process windows.Handle
	treeErr      error
}

func launchCommand(command *exec.Cmd, _ bool) (_ Child, err error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var process windows.Handle
	started := false
	defer func() {
		if err != nil {
			if started {
				_ = command.Process.Kill()
			}
			_ = windows.CloseHandle(job)
			if started {
				_ = command.Wait()
			}
			if process != 0 {
				_ = windows.CloseHandle(process)
			}
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, err
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err = command.Start(); err != nil {
		return nil, fmt.Errorf("start suspended mihomo: %w", err)
	}
	started = true
	process, err = windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		return nil, err
	}
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		return nil, fmt.Errorf("assign suspended mihomo to job: %w", err)
	}
	if err = resumeOnlyThread(uint32(command.Process.Pid)); err != nil {
		return nil, err
	}
	return &windowsChild{processChild: &processChild{command: command}, job: job, process: process}, nil
}

func resumeOnlyThread(pid uint32) (err error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(snapshot)) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err = windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	var id, count uint32
	for {
		if entry.OwnerProcessID == pid {
			id = entry.ThreadID
			count++
		}
		e := windows.Thread32Next(snapshot, &entry)
		if errors.Is(e, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if e != nil {
			return e
		}
	}
	if count != 1 {
		return fmt.Errorf("suspended mihomo has %d threads, expected one", count)
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME|windows.THREAD_QUERY_LIMITED_INFORMATION, false, id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(thread)) }()
	threadPID, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessIdOfThread").Call(uintptr(thread))
	if threadPID == 0 {
		return callErr
	}
	if uint32(threadPID) != pid {
		return errors.New("suspended thread identity changed")
	}
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return err
	}
	if previous != 1 {
		return fmt.Errorf("unexpected suspended thread count %d", previous)
	}
	return nil
}

func (c *windowsChild) Terminate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job == 0 {
		return c.treeErr
	}
	return windows.TerminateJobObject(c.job, 1)
}
func (c *windowsChild) Kill() error { return c.Terminate() }

func (c *windowsChild) Wait() error {
	// Observe the direct process before exec.Cmd waits on inherited output pipes.
	// Closing its remaining tree also releases pipes inherited by grandchildren.
	_, waitErr := windows.WaitForSingleObject(c.process, windows.INFINITE)
	terminateErr := c.Terminate()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	treeErr := c.WaitDescendants(ctx)
	cancel()
	c.mu.Lock()
	c.treeErr = errors.Join(treeErr, terminateErr, waitErr)
	c.treeErr = errors.Join(c.treeErr, windows.CloseHandle(c.job), windows.CloseHandle(c.process))
	c.job, c.process = 0, 0
	result := c.treeErr
	c.mu.Unlock()
	return errors.Join(c.processChild.Wait(), result)
}

// WaitDescendants confirms that this core generation's Job has no live members.
func (c *windowsChild) WaitDescendants(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.Lock()
		if c.job == 0 {
			err := c.treeErr
			c.mu.Unlock()
			return err
		}
		var info struct {
			User, Kernel, PeriodUser, PeriodKernel int64
			Faults, Total, Active, Terminated      uint32
		}
		err := windows.QueryInformationJobObject(c.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if info.Active == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
