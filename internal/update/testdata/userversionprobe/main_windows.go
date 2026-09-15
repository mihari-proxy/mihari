// This fixture only queries its own token and isolated environment.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func main() {
	if windows.GetCurrentProcessToken().IsElevated() || os.Getenv("MIHARI_PROBE_TEST_SECRET") != "" {
		os.Exit(2)
	}
	if !slices.Equal(os.Args[1:], []string{"self", "version", "--json"}) {
		os.Exit(3)
	}
	dir, err := os.Getwd()
	if err != nil || os.Getenv("MIHARI_DATA") != filepath.Join(dir, "data") || os.Getenv("USERPROFILE") != dir {
		os.Exit(4)
	}
	expectedUser, err := os.ReadFile(filepath.Join(dir, "expected-user"))
	user, userErr := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || userErr != nil || user.User.Sid.String() != string(expectedUser) {
		os.Exit(5)
	}
	mode, err := os.ReadFile(filepath.Join(dir, "probe-mode"))
	if err != nil {
		os.Exit(6)
	}
	switch string(mode) {
	case "stdout-overflow":
		fmt.Print(strings.Repeat("x", 8192))
		return
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 8192))
		return
	case "wait":
		time.Sleep(time.Minute)
		return
	}
	fmt.Println(`{"schema":"mihari/v1","version":"v1.2.3"}`)
}
