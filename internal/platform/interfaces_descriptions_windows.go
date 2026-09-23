//go:build windows

package platform

import (
	"context"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func interfaceDescriptions(ctx context.Context) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// FriendlyName is filled unless GAA_FLAG_SKIP_FRIENDLY_NAME is set.
	const flags = windows.GAA_FLAG_INCLUDE_ALL_INTERFACES
	var size uint32
	if err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, nil, &size); err != nil && err != windows.ERROR_BUFFER_OVERFLOW {
		return nil, err
	}
	if size == 0 {
		return map[string]string{}, nil
	}
	buf := make([]byte, size)
	addr := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, addr, &size); err != nil {
		return nil, err
	}
	descriptions := map[string]string{}
	for adapter := addr; adapter != nil; adapter = adapter.Next {
		name := strings.TrimSpace(windows.UTF16PtrToString(adapter.FriendlyName))
		description := strings.TrimSpace(windows.UTF16PtrToString(adapter.Description))
		if name == "" || description == "" {
			continue
		}
		descriptions[name] = description
	}
	return descriptions, nil
}
