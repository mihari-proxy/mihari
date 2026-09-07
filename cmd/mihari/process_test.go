package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mihari-proxy/mihari/internal/cli"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
)

func TestProcess_HelpAndVersionDoNotAssemble(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"self", "version"}, {"--json", "self", "version"}, {"service", "--help"}} {
		var out bytes.Buffer
		calls := 0
		code := executeWithAssembly(context.Background(), args, &out, &out, cli.Dependencies{}, func(context.Context) (cli.Dependencies, error) { calls++; return cli.Dependencies{}, nil })
		if calls != 0 || code != 0 {
			t.Fatalf("args=%v assembly calls=%d exit=%d", args, calls, code)
		}
	}
}
func TestProcess_InheritedValidationAuthenticatesBeforeAssembly(t *testing.T) {
	var out bytes.Buffer
	calls, validated := 0, false
	pure := cli.Dependencies{RunInstallValidation: func(context.Context, string) error { validated = true; return nil }}
	code := executeWithAssembly(context.Background(), []string{"daemon", "--install-validation", "test-transaction"}, &out, &out, pure, func(context.Context) (cli.Dependencies, error) { calls++; return pure, nil })
	if code != 0 || calls != 0 || !validated {
		t.Fatalf("inherited validation reached ordinary assembly: code=%d calls=%d validated=%v", code, calls, validated)
	}
}

func TestProcess_SetupPreservesClassifiedError(t *testing.T) {
	var out bytes.Buffer
	want := protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid process layout"}
	code := executeWithAssembly(context.Background(), []string{"status", "--json"}, &out, &out, cli.Dependencies{}, func(context.Context) (cli.Dependencies, error) { return cli.Dependencies{}, want })
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if code != 2 || envelope.Error.Code != want.Code || envelope.Error.Message != want.Message {
		t.Fatalf("setup classification changed: exit=%d error=%+v", code, envelope.Error)
	}
}
