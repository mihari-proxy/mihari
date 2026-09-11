package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/spf13/cobra"
)

// ServiceController is the OS service surface used by CLI commands.
type ServiceController interface {
	Install() error
	Uninstall() error
	Reinstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (service.StatusKind, error)
}

func newServiceCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "service", Aliases: []string{"svc"}, Short: "Install and control the OS service"}
	root.AddCommand(newServiceActionCommand("install", "Register Mihari as an OS service", dependencies, options, true, func(c ServiceController) error { return c.Install() }))
	root.AddCommand(newServiceUninstallCommand(dependencies, options))
	root.AddCommand(newServiceActionCommand("reinstall", "Re-register the OS service from this binary (upgrade path)", dependencies, options, true, func(c ServiceController) error { return c.Reinstall() }))
	root.AddCommand(newServiceActionCommand("start", "Start the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Start() }))
	root.AddCommand(newServiceActionCommand("stop", "Stop the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Stop() }))
	root.AddCommand(newServiceActionCommand("restart", "Restart the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Restart() }))
	root.AddCommand(newServiceStatusCommand(dependencies, options))
	addInstallationCommands(root, dependencies, options)
	if dependencies.ServiceApply != nil {
		root.AddCommand(newServiceApplyCommand(dependencies, options, os.Geteuid))
	}
	return root
}

func serviceController(dependencies Dependencies) (ServiceController, error) {
	if dependencies.ServiceController == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "service controller is unavailable"}
	}
	return dependencies.ServiceController, nil
}

func newServiceUninstallCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var purge, yes, force bool
	command := &cobra.Command{Use: "uninstall", Short: "Remove the Mihari OS service", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if force && !purge {
			return invalidArgument("--force requires --purge")
		}
		if !purge {
			return runServiceAction(command, "uninstall", dependencies, options, true, func(c ServiceController) error { return c.Uninstall() })
		}
		if !yes {
			return invalidArgument("--purge requires --yes")
		}
		if err := elevate.RequireElevated(); err != nil {
			return err
		}
		if dependencies.Uninstaller == nil {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "complete uninstall is unavailable"}
		}
		if dependencies.CloseForPurgeUninstall != nil {
			if err := dependencies.CloseForPurgeUninstall(); err != nil {
				return protocol.APIError{Code: protocol.CodeInvalidState, Message: "close Mihari local resources before uninstall"}
			}
		}
		ctx := localTaskContext(command.Context(), dependencies, "service.uninstall.purge")
		progress := func(message string) {
			if !options.json {
				_, _ = fmt.Fprintln(command.ErrOrStderr(), message)
			}
		}
		run := dependencies.Uninstaller.Run
		if force {
			run = dependencies.Uninstaller.RunForce
		}
		if err := run(ctx, progress); err != nil {
			reportLocalTaskFailure(ctx, dependencies, "service.uninstall.purge.failed", err)
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: err.Error()}
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "action": "uninstall", "ok": true})
		}
		_, err := fmt.Fprintln(command.OutOrStdout(), "Mihari has been completely uninstalled")
		return err
	}}
	command.Flags().BoolVar(&purge, "purge", false, "remove the Mihari service and recognized Mihari files")
	command.Flags().BoolVar(&yes, "yes", false, "confirm complete uninstall")
	command.Flags().BoolVar(&force, "force", false, "delete entire target folders without checking recognized files (requires --purge --yes)")
	return command
}

func newServiceActionCommand(use, short string, dependencies Dependencies, options *runOptions, requireAdmin bool, action func(ServiceController) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		return runServiceAction(command, use, dependencies, options, requireAdmin, action)
	}}
}

func runServiceAction(command *cobra.Command, use string, dependencies Dependencies, options *runOptions, requireAdmin bool, action func(ServiceController) error) error {
	if requireAdmin {
		if err := elevate.RequireElevated(); err != nil {
			return err
		}
	}
	ctx := localTaskContext(command.Context(), dependencies, "service."+use)
	var err error
	if dependencies.ServiceAction != nil {
		err = dependencies.ServiceAction(ctx, use)
	} else {
		var controller ServiceController
		controller, err = serviceController(dependencies)
		if err == nil {
			err = action(controller)
		}
	}
	if err != nil {
		reportLocalTaskFailure(ctx, dependencies, "service."+use+".failed", err)
		return classifyRuntimeError(err)
	}
	if options.json {
		return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "action": use, "ok": true})
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "service %s ok\n", use)
	return err
}

func newServiceStatusCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show OS service status", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		ctx := localTaskContext(command.Context(), dependencies, "service.status")
		controller, err := serviceController(dependencies)
		if err != nil {
			reportLocalTaskFailure(ctx, dependencies, "service.status.failed", err)
			return err
		}
		status, err := controller.Status()
		if err != nil {
			reportLocalTaskFailure(ctx, dependencies, "service.status.failed", err)
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "status": string(status)})
		}
		_, err = fmt.Fprintln(command.OutOrStdout(), string(status))
		return err
	}}
}

// Local commands borrow diagnostics without changing their output or file ownership.
func localTaskContext(ctx context.Context, dependencies Dependencies, name string) context.Context {
	operation, bound := logging.OperationFromContext(ctx)
	if !bound || operation.Name != name && operation.Name != "" {
		operation = logging.OperationMetadata{}
		id, err := operationID(dependencies)
		if err == nil {
			operation.ID = id
		} else if dependencies.DiagnosticReporter != nil {
			dependencies.DiagnosticReporter(logging.WithOperation(ctx, logging.OperationMetadata{Name: name}), diagnostics.Record{Component: "cli", Event: "local_task.id_generation_failed", Level: slog.LevelWarn, Err: err})
		}
	}
	operation.Name = name
	return logging.WithOperation(ctx, operation)
}

func reportLocalTaskFailure(ctx context.Context, dependencies Dependencies, event string, err error) {
	if dependencies.DiagnosticReporter == nil || diagnostics.AlreadyReported(err) {
		return
	}
	if level, report := diagnostics.FailureLevel(ctx, err); report {
		dependencies.DiagnosticReporter(ctx, diagnostics.Record{Component: "cli", Event: event, Level: level, Err: err})
	}
}
