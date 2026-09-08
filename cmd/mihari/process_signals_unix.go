//go:build linux || darwin

package main

import (
	"os"
	"syscall"
)

func processSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
