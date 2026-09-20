//go:build linux || darwin

package core

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func updateFileIdentity(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || info.Mode().Perm()&0022 != 0 {
		return "", os.ErrPermission
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
func syncUpdateDirectory(r *os.Root, path string) error {
	dir, err := r.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
