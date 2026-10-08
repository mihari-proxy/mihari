package ui

import (
	"errors"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// CopyKind is the observable result of one copy attempt.
type CopyKind uint8

const (
	// CopyConfirmed means a clipboard tool reported that the write succeeded.
	CopyConfirmed CopyKind = iota
	// CopySentToTerminal means the text was handed to the terminal with OSC 52.
	// The terminal does not report whether the clipboard changed.
	CopySentToTerminal
	// CopyFailed means the feedback path returned an error, or no terminal could accept OSC 52.
	CopyFailed
)

// CopySentNotice is the English status for an OSC 52 handoff.
// The terminal does not confirm that the clipboard changed.
const CopySentNotice = "Copy request sent to the terminal via OSC 52 (clipboard command)."

// CopyManualHint is shown with CopySentNotice when the clipboard change cannot be confirmed.
const CopyManualHint = "You may have to copy the path manually if your terminal is not receiving OSC 52."

// CopySentLines are the bright-yellow status lines for an unconfirmed OSC 52 handoff.
func CopySentLines() []string {
	return []string{CopySentNotice, CopyManualHint}
}

// RenderCopySentLines paints each unconfirmed-copy line in the supplied style.
func RenderCopySentLines(style lipgloss.Style) string {
	lines := CopySentLines()
	rendered := make([]string, len(lines))
	for i, line := range lines {
		rendered[i] = style.Render(line)
	}
	return strings.Join(rendered, "\n")
}

// ErrCopyNoTerminal is returned when an SSH session has no terminal to receive OSC 52.
var ErrCopyNoTerminal = errors.New("the terminal cannot receive a copy request")

// CopyRequest chooses between a feedback clipboard and an OSC 52 handoff.
type CopyRequest struct {
	Text     string
	SSH      bool
	Terminal bool
	Write    func(string) error
}

// CopyResult is what the TUI can honestly say about a copy.
type CopyResult struct {
	Kind    CopyKind
	Err     error
	Command tea.Cmd
}

// CopyPath reports a confirmed write, a terminal handoff, or a failure.
// An SSH session uses OSC 52 and does not call Write. Any other session uses Write.
func CopyPath(request CopyRequest) CopyResult {
	if request.SSH {
		if !request.Terminal {
			return CopyResult{Kind: CopyFailed, Err: ErrCopyNoTerminal}
		}
		text := request.Text
		return CopyResult{Kind: CopySentToTerminal, Command: tea.SetClipboard(text)}
	}
	if request.Write == nil {
		return CopyResult{Kind: CopyFailed, Err: errors.New("clipboard is unavailable")}
	}
	if err := request.Write(request.Text); err != nil {
		return CopyResult{Kind: CopyFailed, Err: err}
	}
	return CopyResult{Kind: CopyConfirmed}
}

// CopyText chooses the copy path for the current process.
func CopyText(text string) CopyResult {
	return CopyPath(CopyRequest{
		Text:     text,
		SSH:      sshSession(),
		Terminal: stdoutIsTerminal(),
		Write:    writeFeedbackClipboard,
	})
}

func sshSession() bool {
	if sshFromEnviron(os.Getenv) {
		return true
	}
	return sshAncestor()
}

func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func sshFromEnviron(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if strings.TrimSpace(getenv(key)) != "" {
			return true
		}
	}
	return false
}

// CopyFailureText joins a fixed summary with the associated error on one line.
func CopyFailureText(summary string, err error) string {
	summary = strings.TrimSpace(summary)
	if err == nil {
		return summary
	}
	detail := strings.ReplaceAll(diagnostics.EscapeTerminal(err.Error()), "\n", " ")
	detail = strings.ReplaceAll(detail, "\t", " ")
	detail = strings.Join(strings.Fields(detail), " ")
	if detail == "" {
		return summary
	}
	return summary + ": " + detail
}
