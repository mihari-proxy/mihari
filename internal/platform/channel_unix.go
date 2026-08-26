//go:build unix

package platform

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

type userHome struct {
	Home string
	UID  int
	GID  int
}

var (
	lookupUserHome = lookupUserHomeOS
	effectiveUID   = os.Geteuid
	chownPath      = os.Chown
	lstatPath      = os.Lstat
)

func lookupUserHomeOS(username string) (userHome, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return userHome{}, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return userHome{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return userHome{}, fmt.Errorf("parse gid: %w", err)
	}
	if err := validateHome(u.HomeDir); err != nil {
		return userHome{}, err
	}
	return userHome{Home: u.HomeDir, UID: uid, GID: gid}, nil
}

func validateHome(home string) error {
	if home == "" {
		return fmt.Errorf("resolve sudo user home: empty home")
	}
	if !filepath.IsAbs(home) {
		return fmt.Errorf("resolve sudo user home: home is not absolute")
	}
	return nil
}

func channelDataRootPlatform() (string, error) {
	if effectiveUID() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			info, err := lookupUserHome(sudoUser)
			if err != nil {
				return "", fmt.Errorf("resolve mihari channel data root: %w", err)
			}
			if err := validateHome(info.Home); err != nil {
				return "", err
			}
			return filepath.Join(info.Home, ".mihari"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve mihari channel data root: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve mihari channel data root: empty home")
	}
	return filepath.Join(home, ".mihari"), nil
}

func ownChannelWrite(path string) error {
	if effectiveUID() != 0 {
		return nil
	}
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		return nil
	}
	info, err := lookupUserHome(sudoUser)
	if err != nil {
		return fmt.Errorf("own mihari channel write: %w", err)
	}
	if err := validateHome(info.Home); err != nil {
		return err
	}

	parent := filepath.Dir(path)
	if parent != "." && parent != string(filepath.Separator) && parent != info.Home {
		st, err := lstatPath(parent)
		if err == nil {
			stat, ok := st.Sys().(*syscall.Stat_t)
			if ok && int(stat.Uid) == effectiveUID() {
				if err := chownPath(parent, info.UID, info.GID); err != nil {
					return fmt.Errorf("chown mihari channel parent: %w", err)
				}
			}
		}
	}
	if err := chownPath(path, info.UID, info.GID); err != nil {
		return fmt.Errorf("chown mihari channel: %w", err)
	}
	return nil
}
