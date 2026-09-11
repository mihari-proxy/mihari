//go:build windows

package main

import "os"

func processSignals() []os.Signal { return []os.Signal{os.Interrupt} }
