package main

import (
	"os"
	"testing"
)

func TestInteractiveTerminal_RejectsRedirectedStreams(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	if isInteractiveTerminal(input, output) {
		t.Fatal("regular files must not be treated as an interactive terminal")
	}
}
