package ui

import "testing"

func TestFormatSubscriptionTraffic(t *testing.T) {
	if got := FormatSubscriptionTraffic(0, 0, 0); got != "" {
		t.Fatalf("empty=%q", got)
	}
	// 10 GiB used of 80 GiB
	got := FormatSubscriptionTraffic(5<<30, 5<<30, 80<<30)
	if got != "10.0 GiB/80.0 GiB" {
		t.Fatalf("got %q", got)
	}
	if got := FormatSubscriptionLabel("kanata", 5<<30, 5<<30, 80<<30); got != "kanata · 10.0 GiB/80.0 GiB" {
		t.Fatalf("label=%q", got)
	}
	if got := FormatSubscriptionLabel("kanata", 0, 0, 0); got != "kanata" {
		t.Fatalf("name only=%q", got)
	}
}
