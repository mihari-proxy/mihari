package cli

import (
	"fmt"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
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
	root.AddCommand(newServiceActionCommand("uninstall", "Remove the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Uninstall() }))
	root.AddCommand(newServiceActionCommand("reinstall", "Re-register the OS service from this binary (upgrade path)", dependencies, options, true, func(c ServiceController) error { return c.Reinstall() }))
	root.AddCommand(newServiceActionCommand("start", "Start the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Start() }))
	root.AddCommand(newServiceActionCommand("stop", "Stop the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Stop() }))
	root.AddCommand(newServiceActionCommand("restart", "Restart the Mihari OS service", dependencies, options, true, func(c ServiceController) error { return c.Restart() }))
	root.AddCommand(newServiceStatusCommand(dependencies, options))
	return root
}

func serviceController(dependencies Dependencies) (ServiceController, error) {
	if dependencies.ServiceController == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "service controller is unavailable"}
	}
	return dependencies.ServiceController, nil
}

func newServiceActionCommand(use, short string, dependencies Dependencies, options *runOptions, requireAdmin bool, action func(ServiceController) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if requireAdmin {
			if err := elevate.RequireElevated(); err != nil {
				return err
			}
		}
		controller, err := serviceController(dependencies)
		if err != nil {
			return err
		}
		if err := action(controller); err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "action": use, "ok": true})
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "service %s ok\n", use)
		return err
	}}
}

func newServiceStatusCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show OS service status", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		controller, err := serviceController(dependencies)
		if err != nil {
			return err
		}
		status, err := controller.Status()
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "status": string(status)})
		}
		_, err = fmt.Fprintln(command.OutOrStdout(), string(status))
		return err
	}}
}
