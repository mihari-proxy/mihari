package app

import "testing"

func TestDecodeTUIBytes_LogLevels(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		valid       bool
	}{
		{"legacy", "", true},
		{"selected", `,"log_levels":["debug","warn"]`, true},
		{"empty", `,"log_levels":[]`, false},
		{"unknown", `,"log_levels":["silent"]`, false},
		{"duplicate", `,"log_levels":["warn","warn"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeTUIBytes([]byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"]` + tc.field + `}`))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
