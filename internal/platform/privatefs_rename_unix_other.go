//go:build unix && !linux && !darwin

package platform

import "errors"

func renameatNoReplace(dirfd int, oldName, newName string) error {
	return errors.New("atomic no-replace rename is unsupported on this platform")
}
