//go:build windows

package sysproxy

import "fmt"

// Task 1 stub: real WinINET backend lands in Task 2.
func get() (State, error) {
	return State{}, fmt.Errorf("sysproxy: not implemented")
}

func enable(string, int) error {
	return fmt.Errorf("sysproxy: not implemented")
}

func disable() error {
	return fmt.Errorf("sysproxy: not implemented")
}
