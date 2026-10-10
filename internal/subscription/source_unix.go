//go:build !windows

package subscription

import (
	"os"
	"syscall"
)

func openSourceFile(path string) (*os.File, error) {
	// Nonblocking open prevents a replaced FIFO from waiting for a writer.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
