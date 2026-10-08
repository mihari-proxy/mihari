//go:build !linux

package ui

import "github.com/atotto/clipboard"

func sshAncestor() bool { return false }

func writeFeedbackClipboard(text string) error {
	return clipboard.WriteAll(text)
}
