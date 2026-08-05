package ui

import (
	"strings"
	"unicode/utf8"
)

// regional indicator symbol range (🇦 … 🇿) used to compose flag emojis.
const (
	regionalIndicatorFirst = 0x1F1E6
	regionalIndicatorLast  = 0x1F1FF
)

// DisplayProxyName prepares a proxy/group name for terminal rendering.
// Windows consoles often fail to draw flag emojis (two regional indicators);
// convert them to bracketed ISO codes so names stay readable and width-stable.
func DisplayProxyName(name string) string {
	if name == "" {
		return name
	}
	return replaceFlagEmojis(name)
}

func replaceFlagEmojis(input string) string {
	var b strings.Builder
	b.Grow(len(input))
	runes := []rune(input)
	for i := 0; i < len(runes); {
		r := runes[i]
		if isRegionalIndicator(r) && i+1 < len(runes) && isRegionalIndicator(runes[i+1]) {
			c1 := byte('A' + (r - regionalIndicatorFirst))
			c2 := byte('A' + (runes[i+1] - regionalIndicatorFirst))
			b.WriteByte('[')
			b.WriteByte(c1)
			b.WriteByte(c2)
			b.WriteByte(']')
			i += 2
			continue
		}
		b.WriteRune(r)
		i++
	}
	return b.String()
}

func isRegionalIndicator(r rune) bool {
	return r >= regionalIndicatorFirst && r <= regionalIndicatorLast
}

// RuneCount is a tiny helper for tests / width-sensitive UI.
func RuneCount(s string) int { return utf8.RuneCountInString(s) }
