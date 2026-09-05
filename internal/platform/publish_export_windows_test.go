//go:build windows

package platform

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// InjectPublishTempFailureForTest exposes the factory fault boundary to external
// exporter tests without adding injection APIs to production builds.
func InjectPublishTempFailureForTest(prefix string, hardenErr, cleanupErr error) func() {
	originalHarden, originalDelete := publishWindowsHardenTempFn, publishWindowsDeleteCreatedFn
	publishWindowsHardenTempFn = func(h windows.Handle, sddl string) error {
		path, err := finalPathFromHandle(h)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(path), prefix) {
			return hardenErr
		}
		return originalHarden(h, sddl)
	}
	publishWindowsDeleteCreatedFn = func(h windows.Handle) error {
		if cleanupErr != nil {
			return cleanupErr
		}
		return originalDelete(h)
	}
	return func() { publishWindowsHardenTempFn, publishWindowsDeleteCreatedFn = originalHarden, originalDelete }
}
