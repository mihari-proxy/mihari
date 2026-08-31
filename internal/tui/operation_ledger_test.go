package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type fakeProxyOutcome struct {
	err    error
	status protocol.SystemProxyStatus
}

func (f fakeProxyOutcome) Err() error { return f.err }
func (f fakeProxyOutcome) ProxyStatus() protocol.SystemProxyStatus {
	return f.status
}

func TestNewOperationRecord_SystemProxySuccess(t *testing.T) {
	at := time.Date(2026, 8, 31, 14, 32, 1, 0, time.Local)
	status := protocol.SystemProxyStatus{
		Target: "127.0.0.1:7890",
		Observed: protocol.SystemProxyObserved{
			Enabled: true, Server: "127.0.0.1:7890", Owned: true,
		},
	}
	tests := []struct {
		name   string
		action ui.Action
		wantA  string
		wantD  string
	}{
		{
			name:   "enable",
			action: ui.ActionEnableSystemProxy,
			wantA:  ui.EnableSystemProxyLabel,
			wantD:  "127.0.0.1:7890 · " + ui.PortOwned,
		},
		{
			name:   "force",
			action: ui.ActionForceSystemProxy,
			wantA:  ui.ForceEnableSystemProxyLabel,
			wantD:  fmt.Sprintf(ui.LedgerOverwroteForeignFmt, "127.0.0.1:7890"),
		},
		{
			name:   "disable",
			action: ui.ActionDisableSystemProxy,
			wantA:  ui.DisableSystemProxyLabel,
			wantD:  ui.LedgerCleared,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newOperationRecord(ui.ActionIntentMsg{
				Action: test.action, Object: ui.SystemProxyLabel, Key: "system:" + string(test.action),
			}, fakeProxyOutcome{status: status}, at)
			if got.Object != ui.SystemProxyLabel || got.Action != test.wantA || got.Detail != test.wantD || got.State != ui.SucceededLabel {
				t.Fatalf("record=%+v want action=%q detail=%q", got, test.wantA, test.wantD)
			}
			if !got.At.Equal(at) {
				t.Fatalf("At=%v want %v", got.At, at)
			}
		})
	}
}

func TestNewOperationRecord_SystemProxyFailure(t *testing.T) {
	at := time.Unix(1, 0)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "conflict",
			err: protocol.APIError{
				Code: protocol.CodeSystemProxyConflict, Message: "system proxy is managed by another application",
				Details: map[string]any{"current_server": "10.0.0.1:8080"},
			},
			want: ui.LedgerForeignProxyInUse,
		},
		{
			name: "not owned",
			err:  protocol.APIError{Code: protocol.CodeSystemProxyNotOwned, Message: ui.SystemProxyNotOwnedMessage},
			want: ui.LedgerForeignProxyInUse,
		},
		{
			name: "permission",
			err:  protocol.APIError{Code: protocol.CodePermissionDenied, Message: "administrator privileges are required"},
			want: ui.ServiceNotElevatedLabel,
		},
		{
			name: "revision",
			err:  protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "state revision changed"},
			want: ui.SystemChangedMessage,
		},
		{
			name: "api message",
			err:  protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "enable system proxy"},
			want: "enable system proxy",
		},
		{
			name: "raw error",
			err:  errors.New("The operation completed successfully. (registry)"),
			want: ui.SystemProxyActionFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newOperationRecord(ui.ActionIntentMsg{
				Action: ui.ActionEnableSystemProxy, Object: ui.SystemProxyLabel,
			}, fakeProxyOutcome{err: test.err}, at)
			if got.State != ui.FailedLabel || got.Action != ui.EnableSystemProxyLabel || got.Detail != test.want {
				t.Fatalf("record=%+v want detail=%q", got, test.want)
			}
			if strings.Contains(strings.ToLower(got.Detail), "registry") {
				t.Fatalf("leaked raw error: %q", got.Detail)
			}
			if strings.Contains(got.Detail, "10.0.0.1:8080") {
				t.Fatalf("leaked current_server: %q", got.Detail)
			}
		})
	}
}

func TestNewOperationRecord_SystemProxyFailureUsesLastError(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionEnableSystemProxy, Object: ui.SystemProxyLabel,
	}, fakeProxyOutcome{
		err:    errors.New("ignored raw"),
		status: protocol.SystemProxyStatus{LastError: "enable system proxy"},
	}, time.Unix(1, 0))
	if got.Detail != "enable system proxy" {
		t.Fatalf("detail=%q want last_error", got.Detail)
	}
}

type fakeTunOutcome struct {
	err    error
	status protocol.TunStatus
}

func (f fakeTunOutcome) Err() error                    { return f.err }
func (f fakeTunOutcome) TunStatus() protocol.TunStatus { return f.status }

func TestNewOperationRecord_TunSuccess(t *testing.T) {
	liveOn := true
	liveOff := false
	at := time.Unix(2, 0)
	on := protocol.TunStatus{Stack: "gVisor", LiveEnable: &liveOn}
	off := protocol.TunStatus{Stack: "gVisor", LiveEnable: &liveOff}
	live := ui.LiveLabel + " " + ui.OnLabel
	dead := ui.LiveLabel + " " + ui.OffLabel
	tests := []struct {
		name   string
		action ui.Action
		status protocol.TunStatus
		wantA  string
		wantD  string
	}{
		{"enable", ui.ActionEnableTun, on, ui.EnableTunLabel, "gVisor · " + live},
		{"force", ui.ActionForceTun, on, ui.ForceEnableSystemProxyLabel, "gVisor · " + live},
		{"disable", ui.ActionDisableTun, off, ui.DisableTunLabel, dead},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := newOperationRecord(ui.ActionIntentMsg{
				Action: test.action, Object: ui.TUNLabel,
			}, fakeTunOutcome{status: test.status}, at)
			if got.Action != test.wantA || got.Detail != test.wantD || got.State != ui.SucceededLabel {
				t.Fatalf("record=%+v want action=%q detail=%q", got, test.wantA, test.wantD)
			}
		})
	}
}

func TestNewOperationRecord_TunConflictUsesInterfaceName(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionEnableTun, Object: ui.TUNLabel,
	}, fakeTunOutcome{err: protocol.APIError{
		Code: protocol.CodeTunConflict, Message: "other TUN adapters detected",
		Details: map[string]any{"other_tun_interfaces": []any{"Meta Tunnel", "wintun"}},
	}}, time.Unix(3, 0))
	want := fmt.Sprintf(ui.LedgerOtherTunInUseFmt, "Meta Tunnel")
	if got.State != ui.FailedLabel || got.Detail != want {
		t.Fatalf("record=%+v want %q", got, want)
	}
}

func TestNewOperationRecord_TunConflictWithoutNames(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionEnableTun, Object: ui.TUNLabel,
	}, fakeTunOutcome{err: protocol.APIError{Code: protocol.CodeTunConflict, Message: "other TUN adapters detected"}}, time.Unix(3, 0))
	if got.Detail != ui.LedgerOtherTunInUse {
		t.Fatalf("detail=%q", got.Detail)
	}
}

func TestNewOperationRecord_IgnoresUnrelatedActions(t *testing.T) {
	got := newOperationRecord(ui.ActionIntentMsg{
		Action: ui.ActionRestartCore, Object: "mihomo", Key: "restart-core",
	}, outcomeResultMsg{}, time.Unix(4, 0))
	if got.Action != "" || got.Detail != "" || got.Object != "mihomo" || got.State != ui.SucceededLabel {
		t.Fatalf("record=%+v", got)
	}
}
