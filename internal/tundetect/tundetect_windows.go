//go:build windows

package tundetect

import (
	"context"
	"fmt"
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

// enumerateWintunAdapters lists network adapters whose description or friendly
// name marks them as wintun tunnels. Real wintun adapters (as created by
// mihomo, WireGuard, sing-box, …) report a description containing "Wintun".
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
	var adapters []string
	for a := addr; a != nil; a = a.Next {
		desc := windows.UTF16PtrToString(a.Description)
		friendly := windows.UTF16PtrToString(a.FriendlyName)
		if !isWintun(desc, friendly) {
			continue
		}
		name := friendly
		if name == "" {
			name = desc
		}
		adapters = append(adapters, name)
	}
	return adapters, nil
}

// isWintun reports whether an adapter's description or friendly name identifies
// it as a wintun tunnel (case-insensitive). wintun descriptions look like
// "Wintun Userspace Tunnel (Wintun)".
func isWintun(desc, friendly string) bool {
	return strings.Contains(strings.ToLower(desc), "wintun") ||
		strings.Contains(strings.ToLower(friendly), "wintun")
}

// enumerateMihomoProcesses lists running processes whose executable name
// contains "mihomo" (case-insensitive).
func enumerateMihomoProcesses() ([]string, error) {
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
	var procs []string
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.Contains(strings.ToLower(exe), "mihomo") {
			procs = append(procs, fmt.Sprintf("%s (%d)", exe, entry.ProcessID))
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
