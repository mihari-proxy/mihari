package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type proxyOutcome interface {
	Err() error
	ProxyStatus() protocol.SystemProxyStatus
}

type tunOutcome interface {
	Err() error
	TunStatus() protocol.TunStatus
}

func newOperationRecord(intent ui.ActionIntentMsg, result tea.Msg, at time.Time) ui.OperationRecord {
	state := ui.SucceededLabel
	if res, ok := result.(resultErr); ok && res.Err() != nil {
		state = ui.FailedLabel
	}
	object := intent.Object
	if object == "" {
		object = intent.Title
	}
	return ui.OperationRecord{
		ID: intent.Key, Object: object, Action: ledgerAction(intent.Action),
		Detail: ledgerDetail(intent.Action, result, state == ui.FailedLabel),
		State:  state, At: at,
	}
}

func ledgerAction(action ui.Action) string {
	switch action {
	case ui.ActionEnableSystemProxy, ui.ActionEnableTun:
		return ui.EnableSystemProxyLabel
	case ui.ActionDisableSystemProxy, ui.ActionDisableTun:
		return ui.DisableSystemProxyLabel
	case ui.ActionForceSystemProxy, ui.ActionForceTun:
		return ui.ForceEnableSystemProxyLabel
	default:
		return ""
	}
}

func ledgerDetail(action ui.Action, result tea.Msg, failed bool) string {
	switch action {
	case ui.ActionEnableSystemProxy, ui.ActionForceSystemProxy, ui.ActionDisableSystemProxy:
		status := protocol.SystemProxyStatus{}
		if outcome, ok := result.(proxyOutcome); ok {
			status = outcome.ProxyStatus()
		}
		if failed {
			return proxyFailureDetail(result, status)
		}
		return proxySuccessDetail(action, status)
	case ui.ActionEnableTun, ui.ActionForceTun, ui.ActionDisableTun:
		status := protocol.TunStatus{}
		if outcome, ok := result.(tunOutcome); ok {
			status = outcome.TunStatus()
		}
		if failed {
			return tunFailureDetail(result, status)
		}
		return tunSuccessDetail(action, status)
	default:
		return ""
	}
}

func proxySuccessDetail(action ui.Action, status protocol.SystemProxyStatus) string {
	server := strings.TrimSpace(status.Observed.Server)
	if server == "" {
		server = strings.TrimSpace(status.Target)
	}
	switch action {
	case ui.ActionDisableSystemProxy:
		return ui.LedgerCleared
	case ui.ActionForceSystemProxy:
		if server == "" {
			return strings.TrimSpace(fmt.Sprintf(ui.LedgerOverwroteForeignFmt, ""))
		}
		return fmt.Sprintf(ui.LedgerOverwroteForeignFmt, server)
	default:
		if server == "" {
			return ui.PortOwned
		}
		return server + " · " + ui.PortOwned
	}
}

func proxyFailureDetail(result tea.Msg, status protocol.SystemProxyStatus) string {
	if msg := mappedAPIErrorDetail(result); msg != "" {
		return msg
	}
	if text := strings.TrimSpace(status.LastError); text != "" {
		return text
	}
	return ui.SystemProxyActionFailed
}

func mappedAPIErrorDetail(result tea.Msg) string {
	res, ok := result.(resultErr)
	if !ok || res.Err() == nil {
		return ""
	}
	var apiError protocol.APIError
	if !errors.As(res.Err(), &apiError) {
		return ""
	}
	switch apiError.Code {
	case protocol.CodeSystemProxyConflict, protocol.CodeSystemProxyNotOwned:
		return ui.LedgerForeignProxyInUse
	case protocol.CodeTunConflict:
		return tunConflictDetail(apiError.Details)
	case protocol.CodePermissionDenied:
		return ui.ServiceNotElevatedLabel
	case protocol.CodeRevisionConflict:
		return ui.SystemChangedMessage
	}
	if msg := strings.TrimSpace(apiError.Message); msg != "" {
		return msg
	}
	return ""
}

func tunConflictDetail(details map[string]any) string {
	names := detailStrings(details, "other_tun_interfaces")
	if len(names) == 0 || strings.TrimSpace(names[0]) == "" {
		return ui.LedgerOtherTunInUse
	}
	return fmt.Sprintf(ui.LedgerOtherTunInUseFmt, strings.TrimSpace(names[0]))
}

func tunSuccessDetail(action ui.Action, status protocol.TunStatus) string {
	live := ui.LiveLabel + " " + ui.OffLabel
	if status.LiveEnable != nil && *status.LiveEnable {
		live = ui.LiveLabel + " " + ui.OnLabel
	}
	if action == ui.ActionDisableTun {
		return live
	}
	stack := strings.TrimSpace(status.Stack)
	if stack == "" {
		return live
	}
	return stack + " · " + live
}

func tunFailureDetail(result tea.Msg, status protocol.TunStatus) string {
	if msg := mappedAPIErrorDetail(result); msg != "" {
		return msg
	}
	if text := strings.TrimSpace(status.LastError); text != "" {
		return text
	}
	return ui.TunActionFailed
}

func detailStrings(details map[string]any, key string) []string {
	if details == nil {
		return nil
	}
	value, ok := details[key]
	if !ok || value == nil {
		return nil
	}
	switch slice := value.(type) {
	case []string:
		return slice
	case []any:
		out := make([]string, 0, len(slice))
		for _, item := range slice {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}
