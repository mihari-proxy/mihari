package tui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	setuppage "github.com/mihari-proxy/mihari/internal/tui/pages/setup"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type endpointRestartUI struct {
	request    ui.ApplySavedEndpointsMsg
	before     protocol.Status
	pending    bool
	result     app.EndpointRestartResult
	generation uint64
	modal      *Modal
}

type endpointRestartResultMsg struct {
	generation uint64
	result     app.EndpointRestartResult
	err        error
}

func (m endpointRestartResultMsg) Err() error { return m.err }

func endpointRestartTask(worker *installationWorker, opts app.EndpointRestartOptions) func(context.Context, protocol.OnboardingStatus) (app.EndpointRestartResult, error) {
	return func(ctx context.Context, target protocol.OnboardingStatus) (result app.EndpointRestartResult, err error) {
		err = worker.run(ctx, "endpoints.restart", func(ctx context.Context) error {
			result, err = app.RestartSavedEndpoints(ctx, target, opts)
			return err
		})
		return result, err
	}
}

func (model *Model) applySavedEndpoints(request ui.ApplySavedEndpointsMsg) tea.Cmd {
	previous := model.endpointRestart
	if previous != nil {
		if previous.pending {
			// A status poll can enter automatic setup before the save result
			// reaches System. Retain the explicit edit's return destination.
			if sameSavedEndpoints(previous.request.Status, request.Status) && request.Page == ui.PageSystem {
				previous.request.Page = ui.PageSystem
			}
			return nil
		}
		if !request.Retry && sameDaemonOwner(previous.before, model.status) && sameSavedEndpoints(previous.request.Status, request.Status) {
			return nil
		}
	}
	generation := uint64(1)
	if previous != nil {
		generation = previous.generation + 1
	}
	progress := NewDetail("Applying saved ports", "Ports are saved. Restarting the Mihari service and waiting for the new daemon to apply them.")
	model.endpointRestart = &endpointRestartUI{request: request, before: model.status, pending: true, generation: generation, modal: progress}
	model.modal = progress
	if page, ok := model.pages[ui.PageSetup].(*setuppage.Model); ok {
		page.SetEndpointRestartState(true, "")
	}
	ctx := model.pageCtx
	if ctx == nil {
		ctx = context.Background()
	}
	apply := model.endpointRestartApply
	return func() tea.Msg {
		var result app.EndpointRestartResult
		var err error
		if apply != nil {
			result, err = apply(ctx, request.Status)
		} else {
			err = protocol.APIError{Code: protocol.CodeInvalidState, Message: "Ports are saved. Automatic service restart is unavailable; restart the daemon manually"}
		}
		return endpointRestartResultMsg{generation: generation, result: result, err: err}
	}
}

func (model *Model) finishEndpointRestart(result endpointRestartResultMsg) tea.Cmd {
	u := model.endpointRestart
	if u == nil || !u.pending || u.generation != result.generation {
		return nil
	}
	u.pending = false
	if model.modal == u.modal {
		model.modal = nil
	}
	intent := ui.ActionIntentMsg{Action: ui.ActionServiceRestart, Page: u.request.Page, Title: "Apply saved ports"}
	model.recordActionOutcome(intent, result)
	if result.err != nil {
		message := "Ports are saved. Automatic service restart did not complete; see F2 for details.\nRun mihari service restart from an administrator/root terminal, or stop and restart the foreground daemon."
		var api protocol.APIError
		if errors.As(result.err, &api) {
			message = diagnostics.EscapeTerminal(api.Message)
		}
		if page, ok := model.pages[ui.PageSetup].(*setuppage.Model); ok {
			page.SetEndpointRestartState(false, message)
		}
		model.modal = NewDetail("Ports saved; restart required", message)
		return model.loadRootServiceStatus()
	}
	u.result = result.result
	if !sameDaemonOwner(model.status, result.result.Status) || model.status.Revision <= result.result.Status.Revision {
		model.status = result.result.Status
	}
	model.setupObserved = true
	model.syncSystem()
	model.syncOverview()
	if page, ok := model.pages[ui.PageSystem].(*systempage.Model); ok {
		page.SetOnboarding(result.result.Ports)
	}
	if page, ok := model.pages[ui.PageSetup].(*setuppage.Model); ok {
		page.SetEndpointRestartState(false, "")
		if u.request.Page == ui.PageSetup {
			return tea.Batch(setupCommand(page.Load()), model.loadRootServiceStatus())
		}
		if model.active == ui.PageSetup && page.Automatic() {
			model.active = ui.PageSystem
			model.railIndex = slices.Index(model.rail, ui.PageSystem)
			model.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSystem}
		}
	}
	return model.loadRootServiceStatus()
}

func sameDaemonOwner(a, b protocol.Status) bool {
	return a.PID == b.PID && a.StartedAt.Equal(b.StartedAt)
}

func sameSavedEndpoints(a, b protocol.OnboardingStatus) bool {
	return a.MixedAddr == b.MixedAddr && a.ControllerAddr == b.ControllerAddr && a.WebAddr == b.WebAddr
}

func (model *Model) ignoresPreRestartStatus(status protocol.Status) bool {
	u := model.endpointRestart
	return u != nil && !u.pending && u.result.Status.PID > 0 && !sameDaemonOwner(u.before, u.result.Status) && sameDaemonOwner(status, u.before)
}
