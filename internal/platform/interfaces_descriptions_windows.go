//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
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
	size := uint32(15 * 1024)
	var addr *windows.IpAdapterAddresses
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if size == 0 {
			return map[string]string{}, nil
		}
		buf := make([]byte, size)
		addr = (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, addr, &size)
		if err == nil {
			break
		}
		if errors.Is(err, windows.ERROR_NO_DATA) {
			return map[string]string{}, nil
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) || attempt >= 3 {
			return nil, fmt.Errorf("read adapter descriptions: %w", err)
		}
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
