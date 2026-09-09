// This fixture only exercises version probing; it never starts Mihari.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

func main() {
	mode, err := os.ReadFile(filepath.Join(os.Getenv("MIHARI_DATA"), "probe-mode"))
	if err != nil && !os.IsNotExist(err) {
		os.Exit(2)
	}
	switch string(mode) {
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", 8192))
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 8192))
	case "wait":
		time.Sleep(time.Minute)
	default:
		fmt.Printf("{\"schema\":\"mihari/v1\",\"version\":%q}\n", version)
	}
}
