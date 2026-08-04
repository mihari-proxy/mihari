package platform

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser launches the OS default browser for a local URL.
// Callers must not log the URL when it may contain one-shot access tokens.
func OpenBrowser(url string) error {
	if url == "" {
		return fmt.Errorf("browser url is required")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
