package ui

import "testing"

func TestDisplayProxyName_ReplacesFlagEmojiWithISO(t *testing.T) {
	// 🇯🇵 = regional indicators J + P
	jp := string([]rune{0x1F1EF, 0x1F1F5})
	got := DisplayProxyName(jp + " Tokyo-01")
	if got != "[JP] Tokyo-01" {
		t.Fatalf("got %q", got)
	}
	// 🇺🇸
	us := string([]rune{0x1F1FA, 0x1F1F8})
	if got := DisplayProxyName("US " + us); got != "US [US]" {
		t.Fatalf("got %q", got)
	}
	if got := DisplayProxyName("DIRECT"); got != "DIRECT" {
		t.Fatalf("plain name=%q", got)
	}
}
