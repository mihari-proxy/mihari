//go:build windows

package main

import (
	"bytes"
	"testing"
)

func TestWindowsUpdate_ReturnsPromptWithoutLaunching(t *testing.T) {
	var out bytes.Buffer
	if err := finishWindowsUpdate(&out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "请重新输入 mihari\n" {
		t.Fatalf("prompt %q", got)
	}
}
