package platform

import (
	"os"
	"path/filepath"
)

func defaultInstallRoot() string {
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		return filepath.Join(pf, "Mihari")
	}
	return filepath.Join(`C:\Program Files`, "Mihari")
}
