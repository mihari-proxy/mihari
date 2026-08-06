package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/panel/archive"
)

// InstallRequest describes a local zip install into the versioned web tree.
type InstallRequest struct {
	PanelID    string
	Build      string
	Archive    string
	StagingDir string
	WebRoot    string
}

// PanelBuildDir returns web/{panelID}/{build}/ under webRoot.
func PanelBuildDir(webRoot, panelID, build string) string {
	return filepath.Join(webRoot, panelID, build)
}

// InstallFromZip extracts a validated panel archive into staging, then atomically
// promotes it to web/{panelID}/{build}/. The caller must supply a unique build id.
func InstallFromZip(request InstallRequest) (string, error) {
	if err := validateInstallRequest(request); err != nil {
		return "", err
	}
	if err := os.MkdirAll(request.StagingDir, 0o700); err != nil {
		return "", fmt.Errorf("create panel staging directory: %w", err)
	}
	if err := os.MkdirAll(request.WebRoot, 0o700); err != nil {
		return "", fmt.Errorf("create web root: %w", err)
	}

	stagingName := request.PanelID + "-" + sanitizeBuild(request.Build)
	stagingDir := filepath.Join(request.StagingDir, stagingName)
	_ = os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", fmt.Errorf("create panel staging candidate: %w", err)
	}

	if err := archive.ExtractZip(request.Archive, stagingDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", err
	}
	// GitHub zipballs wrap contents in a single top-level directory; hoist so the
	// build root itself is servable (index.html at web/{panel}/{build}/).
	if err := hoistSingleRootDir(stagingDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", fmt.Errorf("normalize panel archive root: %w", err)
	}

	finalDir := PanelBuildDir(request.WebRoot, request.PanelID, request.Build)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o700); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", fmt.Errorf("create panel install directory: %w", err)
	}
	// Replace any previous incomplete install of the same build.
	_ = os.RemoveAll(finalDir)
	if err := os.Rename(stagingDir, finalDir); err != nil {
		// Cross-device rename fallback: copy is not implemented; fail closed.
		_ = os.RemoveAll(stagingDir)
		return "", protocol.APIError{
			Code:    protocol.CodeDataFailure,
			Message: "promote panel install candidate",
		}
	}
	return finalDir, nil
}

func validateInstallRequest(request InstallRequest) error {
	if request.PanelID == "" || request.Build == "" {
		return protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "panel install requires panel id and build",
		}
	}
	if request.Archive == "" || request.StagingDir == "" || request.WebRoot == "" {
		return protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "panel install requires archive, staging, and web root",
		}
	}
	if strings.Contains(request.PanelID, "..") || strings.Contains(request.Build, "..") ||
		strings.ContainsAny(request.PanelID, `/\`) || strings.ContainsAny(request.Build, `/\`) {
		return protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "panel id and build must be path-safe",
		}
	}
	return nil
}

func sanitizeBuild(build string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, build)
}
