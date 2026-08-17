package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestNewDegradedStoreUsesPortOccupantMessage(t *testing.T) {
	err := protocol.APIError{
		Code: protocol.CodeInvalidState, Message: "managed port is unavailable",
		Details: map[string]any{"setting": "mixed-addr", "address": "127.0.0.1:7890", "pid": 4321, "process": "clash.exe"},
	}
	store := NewDegradedStore("v-test", err)
	got := store.Load()
	if got.Version != "v-test" || got.Health != state.HealthDegraded {
		t.Fatalf("got=%#v", got)
	}
	if got.LastError != "managed port mixed-addr 127.0.0.1:7890 is held by clash.exe (pid 4321)" {
		t.Fatalf("LastError=%q", got.LastError)
	}
}

func TestFormatStartupErrorOmitsSecretAndFallsBack(t *testing.T) {
	err := protocol.APIError{
		Code: protocol.CodeDataFailure, Message: "create mihari data directories",
		Details: map[string]any{"secret": "should-not-appear", "token": "also-hidden"},
	}
	got := FormatStartupError(err)
	if got != "create mihari data directories" {
		t.Fatalf("got=%q", got)
	}
	if strings.Contains(got, "should-not-appear") || strings.Contains(got, "also-hidden") {
		t.Fatalf("leaked secret: %q", got)
	}
	if got := FormatStartupError(errors.New("open /x?token=abc")); got != "daemon startup failed" {
		t.Fatalf("got=%q", got)
	}
}
