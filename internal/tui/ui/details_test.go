package ui

import (
	"slices"
	"testing"
)

func TestDetailStringsHandlesDecodedArrays(t *testing.T) {
	tests := []struct {
		name    string
		details map[string]any
		want    []string
	}{
		{"nil map", nil, nil},
		{"missing key", map[string]any{}, nil},
		{"nil value", map[string]any{"other_tun_interfaces": nil}, nil},
		{"string slice (in-process)", map[string]any{"other_tun_interfaces": []string{"Wintun0"}}, []string{"Wintun0"}},
		{"any slice (HTTP-decoded)", map[string]any{"other_tun_interfaces": []any{"Wintun0", "Wintun1"}}, []string{"Wintun0", "Wintun1"}},
		{"any slice skips non-string items", map[string]any{"other_tun_interfaces": []any{"Wintun0", 42, true}}, []string{"Wintun0"}},
		{"unrelated type", map[string]any{"other_tun_interfaces": "Wintun0"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetailStrings(tt.details, "other_tun_interfaces")
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
