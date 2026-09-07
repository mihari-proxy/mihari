//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// CaptureLayout captures native process inputs once. It never creates business
// or diagnostic directories. An untrusted user home disables file diagnostics.
func CaptureLayout(ctx context.Context) (ResolvedLayout, uint32, error) {
	uid := uint32(os.Geteuid())
	cwd, err := os.Getwd()
	if err != nil {
		return ResolvedLayout{}, uid, err
	}
	input := LayoutInput{CWD: cwd, EUID: uid, Data: os.Getenv("MIHARI_DATA"), InstallRoot: os.Getenv("MIHARI_INSTALL_ROOT"), Endpoint: os.Getenv("MIHARI_CONTROL_ENDPOINT"), Credential: os.Getenv("MIHARI_CONTROL_CREDENTIAL"), XDGState: os.Getenv("XDG_STATE_HOME")}
	defaults := platformLayoutDefaults("")
	home := nativeRootHome()
	if uid != 0 && input.Data == "" {
		home = trustedUserHome(ctx, uid, os.Getenv("HOME"))
		if home == "" {
			account, lookupErr := user.LookupId(strconv.FormatUint(uint64(uid), 10))
			if lookupErr == nil && account.Uid == strconv.FormatUint(uint64(uid), 10) {
				home = trustedUserHome(ctx, uid, account.HomeDir)
			}
		}
	}
	defaults.TrustedHome = home

	layout, err := ResolveLayout(input, defaults)
	if err != nil {
		return layout, uid, errors.Join(os.ErrInvalid, err)
	}
	return layout, uid, nil
}

func trustedUserHome(ctx context.Context, uid uint32, path string) string {
	if !filepath.IsAbs(path) || strings.IndexByte(path, 0) >= 0 {
		return ""
	}
	root, err := openTrustedParent(ctx, filepath.Clean(path), uid)
	if err != nil {
		return ""
	}
	if err := root.Close(); err != nil {
		return ""
	}
	return filepath.Clean(path)
}

// OpenClientLogFS owns only the current user's diagnostic tree. Existing user
// ancestors are verified without chmod; missing descendants are created only
// below a verified, current-user-owned anchor. Failure never falls back to D.
func OpenClientLogFS(ctx context.Context, layout ResolvedLayout) (result *PrivateFS, err error) {
	paths := layout.ClientLogs
	uid := uint32(os.Geteuid())
	if paths.Root == "" || !filepath.IsAbs(paths.Root) {
		return nil, os.ErrPermission
	}
	currentPath := filepath.Clean(paths.Root)
	missing := []string{}
	var anchor *TrustedRoot
	for {
		anchor, err = openTrustedParent(ctx, currentPath, uid)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		parent := filepath.Dir(currentPath)
		if parent == currentPath {
			return nil, os.ErrPermission
		}
		missing = append(missing, filepath.Base(currentPath))
		currentPath = parent
	}
	defer func() {
		err = errors.Join(err, anchor.Close())
		if err != nil && result != nil {
			err = errors.Join(err, result.Close())
			result = nil
		}
	}()
	// For root the only permissible diagnostic creation anchor is its fixed home
	// or a descendant. This forbids creating the account home itself.
	if layout.Mode == SystemMode && uid == 0 && currentPath != nativeRootHome() && !strings.HasPrefix(currentPath, nativeRootHome()+string(os.PathSeparator)) {
		return nil, os.ErrPermission
	}
	for n := len(missing) - 1; n >= 0; n-- {
		child, openErr := anchor.OpenDir(ctx, missing[n], RootPolicy{Owner: uid, Mode: 0700, AllowCreate: true})
		if openErr != nil {
			return nil, openErr
		}
		if closeErr := anchor.Close(); closeErr != nil {
			return nil, errors.Join(closeErr, child.Close())
		}
		anchor = child
	}
	// Reopen the final application root with exact 0700; do not repair an unsafe
	// existing root. The returned PrivateFS takes this independent capability.
	root, err := OpenTrustedRoot(ctx, paths.Root, RootPolicy{Owner: uid, Mode: 0700})
	if err != nil {
		return nil, err
	}
	fs, err := NewPrivateFSFromRoot(root)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return fs, nil
}

// LegacyRootSourcePresent is a conservative bootstrap blocker only. It never
// selects migration data; source authority belongs to explicit install input.
func LegacyRootSourcePresent(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := os.Lstat(filepath.Join(nativeRootHome(), ".mihari"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
