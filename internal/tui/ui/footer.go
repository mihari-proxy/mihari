package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// FitFooter builds a single-line footer that prefers dropping middle shortcut
// segments before truncating. Protected tokens (? help, q quit) and the optional
// global segment (spinner / status) are kept as long as width allows.
func FitFooter(hints, global string, width int) string {
	hints = strings.TrimRight(hints, "\n")
	global = strings.TrimSpace(global)
	if width <= 0 {
		if global == "" {
			return hints
		}
		if hints == "" {
			return global
		}
		return hints + "  ·  " + global
	}

	join := func(h, g string) string {
		h = strings.TrimSpace(h)
		if g == "" {
			return h
		}
		if h == "" {
			return g
		}
		return h + "  ·  " + g
	}

	parts := splitFooterHints(hints)
	for {
		candidate := join(strings.Join(parts, "  "), global)
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
		drop := footerDropIndex(parts)
		if drop < 0 {
			break
		}
		parts = append(parts[:drop], parts[drop+1:]...)
	}

	// Last resort: keep global (spinner/status) and hard-clamp any remaining hints.
	if global != "" {
		gWidth := lipgloss.Width(global)
		if gWidth >= width {
			return lipgloss.NewStyle().MaxWidth(width).Render(global)
		}
		remain := strings.Join(parts, "  ")
		sep := "  ·  "
		avail := width - gWidth - lipgloss.Width(sep)
		if avail <= 0 {
			return lipgloss.NewStyle().MaxWidth(width).Render(global)
		}
		if remain == "" {
			return global
		}
		return lipgloss.NewStyle().MaxWidth(avail).Render(remain) + sep + global
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(strings.Join(parts, "  "))
}

func splitFooterHints(hints string) []string {
	hints = strings.TrimSpace(hints)
	if hints == "" {
		return nil
	}
	raw := strings.Split(hints, "  ")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func footerDropIndex(parts []string) int {
	// Drop from the rightmost non-protected segment so leading navigation stays.
	for i := len(parts) - 1; i >= 0; i-- {
		if footerProtected(parts[i]) {
			continue
		}
		return i
	}
	return -1
}

func footerProtected(part string) bool {
	lower := strings.ToLower(strings.TrimSpace(part))
	if lower == "" {
		return false
	}
	// Keep help and quit affordances preferred by the status-shell design.
	if strings.Contains(lower, "?") {
		return true
	}
	if lower == "q" || strings.HasPrefix(lower, "q ") || strings.Contains(lower, "q quit") {
		return true
	}
	return false
}
