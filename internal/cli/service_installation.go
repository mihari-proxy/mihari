package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/spf13/cobra"
)

func addInstallationCommands(root *cobra.Command, deps Dependencies, options *runOptions) {
	if deps.InstallationInspect != nil {
		root.AddCommand(&cobra.Command{Use: "install-status", Short: "Inspect installation completeness without changing files", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := localTaskContext(cmd.Context(), deps, "installation.inspect")
			status, err := deps.InstallationInspect(ctx)
			if err != nil {
				reportLocalTaskFailure(ctx, deps, "installation.inspect.failed", err)
				return classifyInstallationError(err)
			}
			if options.json {
				return renderJSON(cmd.OutOrStdout(), status)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "installation %s; service %s", status.Kind, status.ServiceState)
			if err != nil {
				return err
			}
			if status.Reason != "" {
				if _, err = fmt.Fprintf(cmd.OutOrStdout(), " (%s)", status.Reason); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout())
			return err
		}})
	}
	if deps.InstallationPlan != nil {
		root.AddCommand(newInstallationCommand("install-plan", deps, options))
		if deps.InstallationExecute != nil {
			root.AddCommand(newInstallationCommand("repair", deps, options), newInstallationCommand("fresh", deps, options))
		}
	}
}

func newInstallationCommand(name string, deps Dependencies, options *runOptions) *cobra.Command {
	var binary, mode, digest string
	var enable, start, yes bool
	short := "Repair installation while preserving existing data"
	if name == "fresh" {
		short = "Rebuild confirmed managed data; preserve unknown files and the local control token"
	}
	if name == "install-plan" {
		short = "Preview a repair or fresh installation and its confirmation digest"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs}
	if name == "repair" {
		cmd.Long = short + ". Configuration errors may still require manual correction."
	}
	cmd.Flags().StringVar(&binary, "binary", "", "Absolute path to a complete new Mihari binary (default: this executable)")
	cmd.Flags().BoolVar(&enable, "enable", false, "Set automatic service startup; omitted preserves the selected policy")
	cmd.Flags().BoolVar(&start, "start", false, "Start after installation; omitted preserves the selected policy")
	if name == "install-plan" {
		cmd.Flags().StringVar(&mode, "mode", "", "Installation mode: repair or fresh")
	} else {
		mode = name
	}
	if name == "fresh" {
		cmd.Flags().BoolVar(&yes, "yes", false, "Confirm exactly the plan identified by --plan-sha256")
		cmd.Flags().StringVar(&digest, "plan-sha256", "", "Confirmation digest from service install-plan")
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if mode != app.InstallationModeRepair && mode != app.InstallationModeFresh {
			return invalidArgument("installation mode must be repair or fresh")
		}
		if binary != "" && (!filepath.IsAbs(binary) || strings.ContainsRune(binary, 0)) {
			return invalidArgument("installation binary must be an absolute path")
		}
		if name == "fresh" && (yes || digest != "" || !deps.Interactive || options.json) {
			if !yes || !installationDigest(digest) {
				return invalidArgument("fresh installation requires --yes and --plan-sha256 from service install-plan")
			}
		}
		if err := elevate.RequireElevated(); err != nil {
			return err
		}
		request := app.InstallationPlanRequest{Mode: mode, Binary: binary}
		if cmd.Flags().Changed("enable") {
			request.Enable = &enable
		}
		if cmd.Flags().Changed("start") {
			request.Start = &start
		}
		ctx := localTaskContext(cmd.Context(), deps, "installation.plan")
		plan, err := deps.InstallationPlan(ctx, request)
		if err != nil {
			reportLocalTaskFailure(ctx, deps, "installation.plan.failed", err)
			return classifyInstallationError(err)
		}
		if name == "install-plan" {
			if options.json {
				return renderJSON(cmd.OutOrStdout(), plan)
			}
			return renderInstallationPlan(cmd, plan)
		}
		if name == "fresh" {
			if yes {
				if digest != plan.PlanSHA256 {
					return protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation plan changed; review a new plan before confirming"}
				}
			} else {
				if err := renderInstallationPlan(cmd, plan); err != nil {
					return err
				}
				if _, err := fmt.Fprint(cmd.OutOrStdout(), "Type 'reinstall' to confirm resetting the managed data listed above, or anything else to cancel: "); err != nil {
					return err
				}
				if err := cmd.Context().Err(); err != nil {
					return err
				}
				scanner := bufio.NewScanner(cmd.InOrStdin())
				scanner.Buffer(make([]byte, 256), 256)
				confirmed := scanner.Scan() && strings.TrimSpace(scanner.Text()) == "reinstall"
				if err := cmd.Context().Err(); err != nil {
					return err
				}
				if err := scanner.Err(); err != nil {
					return invalidArgument("could not read installation confirmation")
				}
				if !confirmed {
					return protocol.APIError{Code: protocol.CodeInvalidState, Message: "fresh installation canceled"}
				}
			}
		}
		ctx = localTaskContext(cmd.Context(), deps, "installation.execute")
		outcome, err := deps.InstallationExecute(ctx, app.InstallationExecuteRequest{Plan: plan, ResetConfirmed: name == "fresh"})
		if err != nil {
			reportLocalTaskFailure(ctx, deps, "installation.execute.failed", err)
			return classifyInstallationError(err)
		}
		if outcome.Schema != app.InstallationOutcomeSchema || !outcome.InstallationComplete || outcome.StartFailed || outcome.ServiceState != "running" && outcome.ServiceState != "stopped" {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation result is invalid"}
		}
		if options.json {
			return renderJSON(cmd.OutOrStdout(), outcome)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "installation complete; service %s\n", outcome.ServiceState)
		return err
	}
	return cmd
}

func installationDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func classifyInstallationError(err error) error {
	var api protocol.APIError
	if errors.As(err, &api) {
		return api
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, app.ErrInstallationPermissionRequired) {
		return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "installation permission required"}
	}
	if errors.Is(err, app.ErrInstallationObservationUnknown) || errors.Is(err, platform.ErrInstallControlBusy) || errors.Is(err, platform.ErrInstallStateChanged) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation state could not be confirmed; inspect again"}
	}
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "installation operation failed"}
}

func renderInstallationPlan(cmd *cobra.Command, plan app.InstallationPlan) error {
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Preserve:"); err != nil {
		return err
	}
	for _, entry := range plan.Preserve {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %q\n", entry.Path); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Reset managed data:"); err != nil {
		return err
	}
	for _, entry := range plan.Delete {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %q\n", entry.Path); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Plan SHA-256: %s\n", plan.PlanSHA256)
	return err
}
