package subscription

import (
	"context"
	"strings"
	"testing"
)

func sudokuPolicyInput(extra string) PolicyInput {
	return simpleProxyInput("sudoku", "    server: example.test\n    port: 443\n    key: fixture-key\n"+extra)
}

func TestRootPolicy_SudokuFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"aead-method", "none", "NONE"}, {"padding-min", "100", "101"}, {"padding-max", "0", "-1"},
		{"table-type", "up_prefer_ascii_down_entropy", "unknown"}, {"enable-pure-downlink", "false", "1"},
		{"http-mask", "false", "1"}, {"http-mask-mode", "ws", "unknown"}, {"http-mask-tls", "true", "1"},
		{"http-mask-host", "public.example.test:443", `"host\r\nInjected: value"`},
		{"path-root", "'/network-path/'", "../filesystem"}, {"multiplex", "on", "unknown"},
		{"http-mask-multiplex", "auto", "unknown"}, {"custom-table", "xpxvvpvv", "xxxxxxxx"},
		{"custom-tables", "[xpxvvpvv, xxppvvvv]", "[invalid]"},
		{"httpmask", "{disable: false, mode: stream, tls: true, host: example.test, path-root: /data/, multiplex: auto}", "{disable: 1}"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), sudokuPolicyInput("    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid Sudoku field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("Sudoku field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), sudokuPolicyInput("    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid Sudoku field accepted")
			}
		})
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"    padding-min: 90\n    padding-max: 10\n", false},
		{"    padding-min: null\n    padding-max: 0\n    httpmask: null\n", true},
		{"    http-mask-mode: ignored-invalid\n    httpmask: {mode: poll}\n", true},
		{"    multiplex: ignored-invalid\n    http-mask-multiplex: on\n", true},
		{"    path-root: /first/\n    httpmask: {path-root: ''}\n", true},
		{"    custom-table: ignored-invalid\n    custom-tables: [xxppvvvv]\n", true},
		{"    table-type: prefer_ascii\n    custom-table: ignored-invalid\n", true},
		{"    table-type: up_ascii_down_entropy\n    custom-table: invalid\n", false},
		{"    custom-table: 'X X P P V V V V'\n", true},
		{"    httpmask: {mode: invalid}\n", false},
		{"    httpmask: {host: []}\n", false},
		{"    httpmask: {path-root: /a/b}\n", false},
		{"    httpmask: {multiplex: invalid}\n", false},
		{"    httpmask: {tls: 1}\n", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), sudokuPolicyInput(tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("Sudoku default/precedence boundary mismatch: %v", err)
		}
	}
}
