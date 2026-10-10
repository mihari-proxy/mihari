package platform

import (
	"fmt"
	"net/url"
	"strings"
)

func nativeFileURI(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	if strings.HasPrefix(path, "//") {
		parts := strings.SplitN(strings.TrimPrefix(path, "//"), "/", 2)
		return (&url.URL{Scheme: "file", Host: parts[0], Path: "/" + parts[1]}).String()
	}
	return (&url.URL{Scheme: "file", Path: "/" + path}).String()
}

func nativeFilePath(u *url.URL) (string, error) {
	if strings.Contains(u.Path, `\`) || strings.Contains(u.Host, ":") {
		return "", fmt.Errorf("invalid Windows file URI")
	}
	if u.Host != "" && u.Host != "localhost" {
		return `\\` + u.Host + strings.ReplaceAll(u.Path, "/", `\`), nil
	}
	if len(u.Path) < 4 || u.Path[0] != '/' || u.Path[2] != ':' || u.Path[3] != '/' {
		return "", fmt.Errorf("file URI requires an absolute drive path")
	}
	return strings.ReplaceAll(u.Path[1:], "/", `\`), nil
}

func validateNativeFilePath(path string) error {
	if strings.HasPrefix(path, `\\.\`) || strings.HasPrefix(path, `\\?\`) {
		return fmt.Errorf("device namespace paths are not YAML file paths")
	}
	if strings.HasPrefix(path, `\\`) {
		parts := strings.Split(strings.TrimPrefix(path, `\\`), `\`)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("UNC path requires a server and share")
		}
	}
	return nil
}
