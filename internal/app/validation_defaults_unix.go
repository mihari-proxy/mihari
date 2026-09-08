//go:build !unix_security && (linux || darwin)

package app

import "os/exec"

func configureValidationTestDefaults(_ *exec.Cmd) (func() error, error) {
	return func() error { return nil }, nil
}
