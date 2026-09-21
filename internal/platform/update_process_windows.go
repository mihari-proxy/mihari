//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var restartManager = windows.NewLazySystemDLL("rstrtmgr.dll")
var rmStart = restartManager.NewProc("RmStartSession")
var rmEnd = restartManager.NewProc("RmEndSession")
var rmRegister = restartManager.NewProc("RmRegisterResources")
var rmList = restartManager.NewProc("RmGetList")

type rmProcess struct {
	PID     uint32
	Started windows.Filetime
}
type rmProcessInfo struct {
	Process     rmProcess
	AppName     [256]uint16
	ServiceName [64]uint16
	AppType     uint32
	AppStatus   uint32
	SessionID   uint32
	Restartable int32
}

// WindowsBinaryUsers retains processes actually reported as using the given executable resources.
// Restart Manager supplies a creation time, so stale PIDs cannot authorize termination.
func WindowsBinaryUsers(ctx context.Context, paths []string) (out []*WindowsProcessIdentity, err error) {
	var session uint32
	var key [33]uint16
	code, _, _ := rmStart.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0])))
	if code != 0 {
		return nil, windows.Errno(code)
	}
	defer func() {
		code, _, _ := rmEnd.Call(uintptr(session))
		if code != 0 {
			err = errors.Join(err, windows.Errno(code))
		}
		if err != nil {
			for _, p := range out {
				err = errors.Join(err, p.Close())
			}
			out = nil
		}
	}()
	names := make([]*uint16, 0, len(paths))
	for _, path := range paths {
		p, e := windows.UTF16PtrFromString(path)
		if e != nil {
			return nil, e
		}
		names = append(names, p)
	}
	if len(names) == 0 {
		return nil, nil
	}
	code, _, _ = rmRegister.Call(uintptr(session), uintptr(len(names)), uintptr(unsafe.Pointer(&names[0])), 0, 0, 0, 0)
	runtime.KeepAlive(names)
	if code != 0 {
		return nil, windows.Errno(code)
	}
	var infos []rmProcessInfo
	for tries := 0; tries < 4; tries++ {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		var needed, reasons uint32
		count := uint32(len(infos))
		var pointer uintptr
		if count > 0 {
			pointer = uintptr(unsafe.Pointer(&infos[0]))
		}
		code, _, _ = rmList.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), pointer, uintptr(unsafe.Pointer(&reasons)))
		runtime.KeepAlive(infos)
		if code == 0 {
			infos = infos[:count]
			break
		}
		if code != uintptr(windows.ERROR_MORE_DATA) || needed > 4096 {
			return out, fmt.Errorf("enumerate binary users: %w", windows.Errno(code))
		}
		infos = make([]rmProcessInfo, needed)
		if tries == 3 {
			return out, errors.New("binary users changed repeatedly")
		}
	}
	for _, info := range infos {
		if info.Process.PID == uint32(os.Getpid()) {
			continue
		}
		p, e := OpenWindowsProcessIdentity(ctx, info.Process.PID)
		if errors.Is(e, windows.ERROR_INVALID_PARAMETER) {
			continue
		} // The observed process already exited.
		if e != nil {
			return out, e
		}
		birth := uint64(info.Process.Started.HighDateTime)<<32 | uint64(info.Process.Started.LowDateTime)
		if p.Identity().CreationFiletime != birth {
			err = errors.Join(ErrIdentityMismatch, p.Close())
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

func clientExitEventName(id WindowsProcessIdentityValue) string {
	return fmt.Sprintf(`Local\Mihari.UpdateClient.%d.%d`, id.PID, id.CreationFiletime)
}

// WindowsClientExitSignal lets an updater request normal client cancellation without a console broadcast.
func WindowsClientExitSignal(ctx context.Context, cancel context.CancelFunc) (func() error, error) {
	self, err := OpenWindowsProcessIdentity(ctx, uint32(os.Getpid()))
	if err != nil {
		return nil, err
	}
	id := self.Identity()
	elevated, tokenErr := self.updateTokenElevated()
	if err = errors.Join(tokenErr, self.Close()); err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(clientExitEventName(id))
	if err != nil {
		return nil, err
	}
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + id.SID + ")"
	if elevated {
		sddl = windowsRuntimeJobSDDL
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	event, err := windows.CreateEvent(&sa, 1, 0, name)
	if err != nil {
		if event != 0 {
			_ = windows.CloseHandle(event)
		}
		return nil, err
	}
	ownerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ownerCtx.Done():
				return
			case <-ticker.C:
				state, e := windows.WaitForSingleObject(event, 0)
				if e != nil {
					return
				}
				if state == windows.WAIT_OBJECT_0 {
					cancel()
					return
				}
			}
		}
	}()
	return func() error { stop(); <-done; return windows.CloseHandle(event) }, nil
}

// OpenClientExitSignal proves that this process registered as a cancellable client.
func (p *WindowsProcessIdentity) OpenClientExitSignal() (_ windows.Handle, err error) {
	name, err := windows.UTF16PtrFromString(clientExitEventName(p.Identity()))
	if err != nil {
		return 0, err
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE|windows.READ_CONTROL, false, name)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, windows.CloseHandle(h))
		}
	}()
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return 0, err
	}
	elevated, err := p.updateTokenElevated()
	if err != nil {
		return 0, err
	}
	if elevated {
		trusted, e := windowsRuntimeProtectedPolicy(sd, windows.EVENT_ALL_ACCESS)
		if e != nil {
			return 0, e
		}
		if !trusted {
			return 0, ErrUnsafeComponent
		}
	} else {
		sid, e := windows.StringToSid(p.Identity().SID)
		if e != nil {
			return 0, e
		}
		if !replacementWindowsExecutionTrust(sd, false, sid, false) {
			return 0, ErrUnsafeComponent
		}
	}
	return h, nil
}

// Terminate ends exactly the retained process identity and confirms its exit.
func (p *WindowsProcessIdentity) Terminate(ctx context.Context) (err error) {
	if exited, err := p.Exited(ctx); err != nil || exited {
		return err
	}
	identity := p.Identity()
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, identity.PID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	actual, err := nativeWindowsProcessMetadata(ctx, h, identity.PID)
	if err != nil {
		return err
	}
	if actual != identity {
		return ErrIdentityMismatch
	}
	if err = windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	return p.WaitExit(ctx)
}

// WaitExit waits for the held process to exit without resolving its PID again.
func (p *WindowsProcessIdentity) WaitExit(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		exited, err := p.Exited(ctx)
		if err != nil {
			return err
		}
		if exited {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
