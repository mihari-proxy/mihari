//go:build !windows

package platform

import (
	"fmt"
	"net/url"
	"strings"
)

func nativeFileURI(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }
func nativeFilePath(u *url.URL) (string, error) {
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", fmt.Errorf("non-local file URI authority is unsupported: %s", u.Host)
	}
	return u.Path, nil
}

func validateNativeFilePath(path string) error { return nil }
