package ui

import "testing"

func TestFormatPositionIndicator(t *testing.T) {
	tests := []struct {
		name    string
		focused bool
		pos     int
		total   int
		want    string
	}{
		{"focused row", true, 3, 50, "3/50"},
		{"not focused", false, 0, 50, "—/50"},
		{"empty list", false, 0, 0, "0/0"},
		{"empty list stays zero even if focused", true, 1, 0, "0/0"},
		{"full ten-thousand buffer", true, 10000, 10000, "10000/10000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatPositionIndicator(tt.focused, tt.pos, tt.total); got != tt.want {
				t.Fatalf("FormatPositionIndicator(%v, %d, %d) = %q, want %q",
					tt.focused, tt.pos, tt.total, got, tt.want)
			}
		})
	}
}
