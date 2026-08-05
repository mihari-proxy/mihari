//go:build !windows && !darwin && !linux

package sysproxy

import "fmt"

func get() (State, error) {
	return State{}, fmt.Errorf("sysproxy: unsupported platform")
}

func enable(string, int) error {
	return fmt.Errorf("sysproxy: unsupported platform")
}

func disable() error {
	return fmt.Errorf("sysproxy: unsupported platform")
}
