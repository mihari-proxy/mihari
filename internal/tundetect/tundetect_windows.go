//go:build windows

package tundetect

import (
	"context"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detect enumerates wintun adapters (signal A) and mihomo processes (signal B).
func detect(ctx context.Context) (Detection, error) {
	if err := ctx.Err(); err != nil {
		return Detection{}, err
	}
	tun, err := enumerateWintunAdapters()
	if err != nil {
		return Detection{}, err
	}
	mihomo, err := enumerateMihomoProcesses()
	if err != nil {
		return Detection{}, err
	}
	return Detection{TunInterfaces: tun, MihomoProcesses: mihomo}, nil
}

// enumerateWintunAdapters lists Up Windows TUN tunnels (wintun, Meta Tunnel /
// mihomo, WireGuard). Down leftover adapters cannot take routes and are omitted.
func enumerateWintunAdapters() ([]string, error) {
	var size uint32
	if err := windows.GetAdaptersAddresses(0, 0x20 /* GAA_FLAG_INCLUDE_FRIENDLY_NAME — fill FriendlyName for friendlier display */, 0, nil, &size); err != nil && err != windows.ERROR_BUFFER_OVERFLOW {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	addr := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(0, 0x20 /* GAA_FLAG_INCLUDE_FRIENDLY_NAME */, 0, addr, &size); err != nil {
		return nil, err
	}
	var candidates []windowsTunCandidate
	for a := addr; a != nil; a = a.Next {
		candidates = append(candidates, windowsTunCandidate{
			desc:       windows.UTF16PtrToString(a.Description),
			friendly:   windows.UTF16PtrToString(a.FriendlyName),
			operStatus: a.OperStatus,
		})
	}
	return collectWindowsTunNames(candidates), nil
}

// enumerateMihomoProcesses lists running processes whose executable name
// contains "mihomo" (case-insensitive).
func enumerateMihomoProcesses() ([]Process, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var procs []Process
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.Contains(strings.ToLower(exe), "mihomo") {
			procs = append(procs, Process{
				Name:      exe,
				PID:       int(entry.ProcessID),
				ParentPID: int(entry.ParentProcessID),
				Path:      windowsProcessPath(entry.ProcessID),
			})
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, err
		}
	}
	return procs, nil
}

func windowsProcessPath(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}
