//go:build unix_security && (linux || darwin)

package app

import (
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"os/exec"
)

func configureValidationTestDefaults(cmd *exec.Cmd) (func() error, error) {
	defaults := platform.SystemLayoutDefaults() // validates parent-only root markers
	if os.Geteuid() != 0 || os.Getenv("MIHARI_ISOLATED_SECURITY_CI") != "1" || len(cmd.ExtraFiles) != 1 {
		return nil, os.ErrPermission
	}
	mode := os.Getenv("MIHARI_SECURITY_VALIDATION_FIXTURE")
	if mode != "" && mode != "hold-after-eof" {
		return nil, os.ErrInvalid
	}
	raw, err := json.Marshal(struct {
		Schema            string                  `json:"schema"`
		Defaults          platform.LayoutDefaults `json:"defaults"`
		ValidationFixture string                  `json:"validation_fixture,omitempty"`
	}{"mihari.unix-security-defaults/v1", defaults, mode})
	if err != nil {
		return nil, err
	}
	// A complete frame fits the POSIX minimum pipe capacity. No writer goroutine
	// or unbounded pre-Start write can outlive a failed child launch.
	if len(raw) > 512 {
		return nil, os.ErrInvalid
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	_, writeErr := write.Write(raw)
	if err := errors.Join(writeErr, write.Close()); err != nil {
		return nil, errors.Join(err, read.Close())
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, read) // FD3 remains the validation lease
	cmd.Env = append(cmd.Env, "MIHARI_UNIX_SECURITY_VALIDATION=1")
	return read.Close, nil
}
