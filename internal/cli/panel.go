package cli

import (
	"context"
	"fmt"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/platform"
	"github.com/spf13/cobra"
)

// PanelClient is the typed control surface used by panel CLI commands.
type PanelClient interface {
	WebGUI(context.Context) (protocol.WebGUIStatus, error)
	OpenWebGUI(context.Context, string) (protocol.WebGUIOpenResult, error)
	Panels(context.Context) (protocol.PanelList, error)
	InstallPanel(context.Context, string, protocol.PanelInstallRequest) (protocol.MutationResult, error)
	UpdatePanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	ActivatePanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	RollbackPanel(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
}

func newPanelCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "panel", Aliases: []string{"panels", "web-gui"}, Short: "Manage Web GUI panels"}
	root.AddCommand(newPanelListCommand(dependencies, options))
	root.AddCommand(newPanelInstallCommand(dependencies, options))
	root.AddCommand(newPanelUpdateCommand(dependencies, options))
	root.AddCommand(newPanelUseCommand(dependencies, options))
	root.AddCommand(newPanelOpenCommand(dependencies, options))
	root.AddCommand(newPanelRollbackCommand(dependencies, options))
	return root
}

func panelClient(dependencies Dependencies) (PanelClient, error) {
	if dependencies.PanelClient == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "panel client is unavailable"}
	}
	return dependencies.PanelClient, nil
}

func newPanelListCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List panels", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		client, err := panelClient(dependencies)
		if err != nil {
			return err
		}
		result, err := client.Panels(command.Context())
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), result)
		}
		for _, item := range result.Panels {
			marker := " "
			if item.Active {
				marker = "*"
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\t%s\tinstalled=%s\trollback=%s\thealth=%s\n",
				marker, item.ID, item.Name, emptyDash(item.InstalledBuild), emptyDash(item.RollbackBuild), item.Health); err != nil {
				return err
			}
		}
		return nil
	}}
}

func newPanelInstallCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{Use: "install ID", Short: "Install a panel", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := panelClient(dependencies)
		if err != nil {
			return err
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		result, err := client.InstallPanel(command.Context(), args[0], protocol.PanelInstallRequest{
			OperationID: id, IfRevision: revisionFlag(command, revision),
		})
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderMutation(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newPanelUpdateCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{Use: "update ID", Short: "Update a panel", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := panelClient(dependencies)
		if err != nil {
			return err
		}
		request, err := mutationRequest(dependencies)
		if err != nil {
			return err
		}
		request.IfRevision = revisionFlag(command, revision)
		result, err := client.UpdatePanel(command.Context(), args[0], request)
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderMutation(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newPanelUseCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{Use: "use ID", Short: "Set the default Web GUI panel for root /", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := panelClient(dependencies)
		if err != nil {
			return err
		}
		request, err := mutationRequest(dependencies)
		if err != nil {
			return err
		}
		request.IfRevision = revisionFlag(command, revision)
		result, err := client.ActivatePanel(command.Context(), args[0], request)
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderMutation(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newPanelOpenCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "open [ID]",
		Short: "Open an installed Web GUI panel in a browser",
		Long:  "Open a panel at its own /__mihari/panels/{id}/ URL. Omit ID to open the default active panel. Installed panels are not mutually exclusive.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, err := panelClient(dependencies)
			if err != nil {
				return err
			}
			panelID := ""
			if len(args) == 1 {
				panelID = args[0]
			}
			open, err := client.OpenWebGUI(command.Context(), panelID)
			if err != nil {
				return classifyRuntimeError(err)
			}
			openBrowser := dependencies.OpenBrowser
			if openBrowser == nil {
				openBrowser = platform.OpenBrowser
			}
			if err := openBrowser(open.OpenURL); err != nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "failed to open browser"}
			}
			// Never print the open URL or token (human or JSON default CLI output).
			if options.json {
				payload := map[string]any{"schema": "mihari/v1", "opened": true}
				if open.Panel != "" {
					payload["panel"] = open.Panel
				}
				return renderJSON(command.OutOrStdout(), payload)
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "Opened Web GUI in browser")
			return err
		},
	}
}

func newPanelRollbackCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var yes bool
	var revision uint64
	command := &cobra.Command{Use: "rollback ID", Short: "Rollback a panel to the previous build", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if !yes {
			return invalidArgument("panel rollback requires --yes")
		}
		client, err := panelClient(dependencies)
		if err != nil {
			return err
		}
		request, err := mutationRequest(dependencies)
		if err != nil {
			return err
		}
		request.IfRevision = revisionFlag(command, revision)
		result, err := client.RollbackPanel(command.Context(), args[0], request)
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderMutation(command, options, result)
	}}
	command.Flags().BoolVar(&yes, "yes", false, "confirm panel rollback")
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func renderMutation(command *cobra.Command, options *runOptions, result protocol.MutationResult) error {
	if options.json {
		return renderJSON(command.OutOrStdout(), result)
	}
	return printMutation(command.OutOrStdout(), result)
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
