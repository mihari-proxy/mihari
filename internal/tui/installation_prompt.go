package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// InstallationActions connects the TUI to the read-only inspector and explicit
// application installation manager. Execute runs only after TUI cleanup.
type InstallationActions struct {
	Inspect  func(context.Context) (protocol.InstallationStatus, error)
	Plan     func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error)
	Execute  func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error)
	Elevated func() bool
	Binary   string
}

type installationUI struct {
	actions    InstallationActions
	seen       bool
	visible    bool
	selected   int
	plan       *app.InstallationPlan
	loading    bool
	generation uint64
	mode       string
	cancel     context.CancelFunc
	scroll     int
}

type installationStatusMsg struct {
	status protocol.InstallationStatus
	err    error
}
type installationPlanMsg struct {
	plan       app.InstallationPlan
	err        error
	generation uint64
}

func (model *Model) setInstallationActions(actions InstallationActions) {
	model.installation = &installationUI{actions: actions}
}
func (model *Model) updateInstallation(message tea.Msg) (tea.Cmd, bool) {
	u := model.installation
	if u == nil {
		return nil, false
	}
	switch message := message.(type) {
	case installationStatusMsg:
		if message.err != nil || message.status.Validate() != nil || u.seen {
			return nil, true
		}
		if message.status.Kind == "permission_required" {
			model.modal = NewDetail("Installation status unavailable", "Run Mihari from an administrator terminal to check the installation status.")
			return nil, true
		}
		if message.status.Kind == "interrupted" {
			u.seen, u.visible, u.selected = true, true, 0
		}
		return nil, true
	case installationPlanMsg:
		if !u.visible || !u.loading || message.generation != u.generation {
			return nil, true
		}
		u.loading = false
		if u.cancel != nil {
			u.cancel()
			u.cancel = nil
		}
		if message.err != nil || message.plan.Mode != u.mode || app.VerifyInstallationPlan(message.plan) != nil {
			u.visible = false
			model.modal = NewDetail("Unable to prepare installation", "The installation state may have changed. Check it again before retrying. Reinstallation has not started.")
			return nil, true
		}
		u.plan, u.selected = &message.plan, 1 // Every preview defaults to cancel.
		return nil, true
	case tea.KeyPressMsg:
		if !u.visible {
			return nil, false
		}
		switch message.String() {
		case "pgdown":
			u.scroll += 8
		case "pgup":
			u.scroll = max(0, u.scroll-8)
		case "esc", "q":
			u.dismiss()
			return nil, true
		case "up", "left", "shift+tab":
			if !u.loading {
				count := 3
				if u.plan != nil {
					count = 2
				}
				u.selected = (u.selected + count - 1) % count
			}
		case "down", "right", "tab":
			if !u.loading {
				count := 3
				if u.plan != nil {
					count = 2
				}
				u.selected = (u.selected + 1) % count
			}
		case "enter":
			if u.loading {
				return nil, true
			}
			if u.plan != nil {
				if u.selected == 1 {
					u.dismiss()
					return nil, true
				}
				if model.preparedUpdate != nil {
					u.dismiss()
					return nil, true
				}
				model.preparedInstallation = &app.InstallationExecuteRequest{Plan: *u.plan, ResetConfirmed: u.plan.Mode == app.InstallationModeFresh}
				u.dismiss()
				return tea.Quit, true
			}
			if u.selected == 2 {
				u.dismiss()
				return nil, true
			}
			if u.actions.Elevated == nil || !u.actions.Elevated() {
				u.dismiss()
				model.modal = NewDetail("Administrator privileges required", "Run Mihari from an administrator terminal, then choose a reinstallation option.")
				return nil, true
			}
			if u.actions.Plan == nil || u.actions.Execute == nil {
				u.dismiss()
				model.modal = NewDetail("Use the installer", "Use a newly downloaded Mihari installer to choose a repair or fresh installation.")
				return nil, true
			}
			u.mode = app.InstallationModeRepair
			if u.selected == 1 {
				u.mode = app.InstallationModeFresh
			}
			u.loading = true
			u.generation++
			generation, mode, plan, binary := u.generation, u.mode, u.actions.Plan, u.actions.Binary
			ctx := model.pageCtx
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, u.cancel = context.WithCancel(ctx)
			return func() tea.Msg {
				result, err := plan(ctx, app.InstallationPlanRequest{Mode: mode, Binary: binary})
				return installationPlanMsg{plan: result, err: err, generation: generation}
			}, true
		}
		return nil, true
	}
	return nil, false
}

func (u *installationUI) dismiss() {
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	u.visible, u.loading, u.plan = false, false, nil
	u.generation++
}

func (model Model) inspectInstallation() tea.Cmd {
	if model.installation == nil || model.installation.actions.Inspect == nil {
		return nil
	}
	inspect := model.installation.actions.Inspect
	ctx := model.pageCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg { status, err := inspect(ctx); return installationStatusMsg{status: status, err: err} }
}

func (u *installationUI) view(width, height int) string {
	body := "An interrupted installation was detected. Reinstallation is recommended.\n\n"
	if u.loading {
		body += "Preparing the installation preview…\nEsc to cancel"
	} else if u.plan == nil {
		for i, label := range []string{"Repair installation (keep data, recommended)", "Fresh installation (reset managed data)", "Cancel"} {
			prefix := "  "
			if i == u.selected {
				prefix = "› "
			}
			body += prefix + label + "\n"
		}
	} else {
		body = "Review the installation changes\n\nPreserve:\n"
		for _, entry := range u.plan.Preserve {
			body += "  " + strconv.Quote(entry.Path) + "\n"
		}
		if u.plan.Mode == app.InstallationModeFresh {
			body += "\nReset the following managed data:\n"
			for _, entry := range u.plan.Delete {
				body += "  " + strconv.Quote(entry.Path) + "\n"
			}
			body += "\nA later repair cannot restore deleted data. Unknown files and control credentials will be preserved.\n"
		} else {
			body += "\nExisting data will be preserved. Configuration errors may still require manual correction.\n"
		}
		labels := []string{"Confirm reinstallation", "Cancel"}
		for i, label := range labels {
			prefix := "  "
			if i == u.selected {
				prefix = "› "
			}
			body += prefix + label + "\n"
		}
	}
	// Reuse the existing scrollable dialog so every path remains reviewable.
	dialog := NewHelp("Reinstall Mihari · PgUp/PgDn to scroll", strings.TrimSpace(body))
	dialog.scroll = u.scroll
	view := dialog.View(width, height)
	u.scroll = dialog.scroll
	return view
}
func finishInstallationRun(ctx context.Context, final tea.Model, runErr error, out io.Writer, cleanup func(tea.Model) error, execute func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error)) error {
	model, ok := final.(Model)
	if pointer, pointerOK := final.(*Model); pointerOK && pointer != nil {
		model, ok = *pointer, true
	}
	var cleanupErr error
	if cleanup != nil {
		cleanupErr = cleanup(final)
	}
	if err := errors.Join(runErr, cleanupErr, ctx.Err()); err != nil {
		return err
	}
	if !ok || model.preparedInstallation == nil || model.preparedUpdate != nil || execute == nil {
		return errors.New("installation execution unavailable")
	}
	outcome, err := execute(ctx, *model.preparedInstallation)
	if err != nil {
		var apiErr protocol.APIError
		if out != nil && errors.As(err, &apiErr) && apiErr.Code == protocol.CodeInvalidState && apiErr.Details["installation_complete"] == true && apiErr.Details["reason"] == app.InstallationReasonServiceStartFailed {
			_, writeErr := fmt.Fprintln(out, "Installation completed, but the service failed to start. Run 'mihari service start' to retry.")
			return errors.Join(err, writeErr)
		}
		return err
	}
	if outcome.Schema != app.InstallationOutcomeSchema || !outcome.InstallationComplete || outcome.StartFailed || (outcome.ServiceState != app.InstallServiceRunning && outcome.ServiceState != app.InstallServiceStopped) {
		return errors.New("installation result is invalid")
	}
	if out != nil {
		_, err = fmt.Fprintln(out, "Reinstallation completed.")
	}
	return err
}

func installationCleanup(closeCandidates func() error, cleanup func(tea.Model) error) func(tea.Model) error {
	return func(final tea.Model) error {
		var candidateErr, cleanupErr error
		if closeCandidates != nil {
			candidateErr = closeCandidates()
		}
		if cleanup != nil {
			cleanupErr = cleanup(final)
		}
		return errors.Join(candidateErr, cleanupErr)
	}
}
