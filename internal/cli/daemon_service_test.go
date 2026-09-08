package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestDaemon_SystemServiceUsesDedicatedGate(t *testing.T) {
	var out bytes.Buffer
	calls := 0
	deps := Dependencies{RunDaemon: func(context.Context) error { t.Fatal("service used foreground bootstrap"); return nil }, RunSystemServiceDaemon: func(context.Context) error { calls++; return nil }}
	code := Execute(context.Background(), []string{"daemon", "--system-service"}, &out, &out, deps)
	if code != 0 || calls != 1 {
		t.Fatalf("service gate calls=%d code=%d out=%s", calls, code, out.String())
	}
}

func TestDaemon_ServiceValidationCombinationRejected(t *testing.T) {
	var out bytes.Buffer
	calls := 0
	deps := Dependencies{RunInstallValidation: func(context.Context, string) error { calls++; return nil }, RunSystemServiceDaemon: func(context.Context) error { calls++; return nil }}
	code := Execute(context.Background(), []string{"daemon", "--system-service", "--install-validation", "id"}, &out, &out, deps)
	if code != 2 || calls != 0 {
		t.Fatalf("mixed startup modes: calls=%d code=%d out=%s", calls, code, out.String())
	}
}

func TestDaemon_ServiceFalseDoesNotFallBackToForeground(t *testing.T) {
	var out bytes.Buffer
	calls := 0
	deps := Dependencies{RunDaemon: func(context.Context) error { calls++; return nil }, RunSystemServiceDaemon: func(context.Context) error { calls++; return nil }}
	code := Execute(context.Background(), []string{"daemon", "--system-service=false"}, &out, &out, deps)
	if code != 2 || calls != 0 {
		t.Fatalf("false marker switched startup mode: calls=%d code=%d", calls, code)
	}
}
