package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

// SystemProxyClient is the typed control surface used by sysproxy CLI commands.
type SystemProxyClient interface {
	SystemProxy(context.Context) (protocol.SystemProxyStatus, error)
	EnableSystemProxy(context.Context, protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error)
	DisableSystemProxy(context.Context, protocol.SystemProxyMutationRequest) (protocol.SystemProxyStatus, error)
}

func newSysproxyCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "sysproxy", Short: "Manage OS system proxy"}
	root.AddCommand(newSysproxyStatusCommand(dependencies, options))
	root.AddCommand(newSysproxyEnableCommand(dependencies, options))
	root.AddCommand(newSysproxyDisableCommand(dependencies, options))
	return root
}

func systemProxyClient(dependencies Dependencies) (SystemProxyClient, error) {
	if dependencies.SystemProxyClient == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "system proxy client is unavailable"}
	}
	return dependencies.SystemProxyClient, nil
}

func newSysproxyStatusCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show system proxy status", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := systemProxyClient(dependencies)
			if err != nil {
				return err
			}
			status, err := client.SystemProxy(command.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			return renderSystemProxyStatus(command, options, status)
		},
	}
}

func newSysproxyEnableCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var force bool
	var revision uint64
	command := &cobra.Command{
		Use: "enable", Short: "Enable OS system proxy via the daemon", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := systemProxyClient(dependencies)
			if err != nil {
				return err
			}
			id, err := operationID(dependencies)
			if err != nil {
				return err
			}
			status, err := client.EnableSystemProxy(command.Context(), protocol.SystemProxyMutationRequest{
				OperationID: id,
				IfRevision:  revisionFlag(command, revision),
				Force:       force,
			})
			if err != nil {
				return classifyRuntimeError(err)
			}
			return renderSystemProxyStatus(command, options, status)
		},
	}
	command.Flags().BoolVar(&force, "force", false, "overwrite a foreign system proxy")
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newSysproxyDisableCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{
		Use: "disable", Short: "Disable Mihari-owned system proxy", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := systemProxyClient(dependencies)
			if err != nil {
				return err
			}
			id, err := operationID(dependencies)
			if err != nil {
				return err
			}
			status, err := client.DisableSystemProxy(command.Context(), protocol.SystemProxyMutationRequest{
				OperationID: id,
				IfRevision:  revisionFlag(command, revision),
			})
			if err != nil {
				return classifyRuntimeError(err)
			}
			return renderSystemProxyStatus(command, options, status)
		},
	}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func renderSystemProxyStatus(command *cobra.Command, options *runOptions, status protocol.SystemProxyStatus) error {
	if options.json {
		return renderJSON(command.OutOrStdout(), status)
	}
	return printSystemProxyStatus(command.OutOrStdout(), status)
}

func printSystemProxyStatus(writer io.Writer, status protocol.SystemProxyStatus) error {
	_, err := fmt.Fprintf(writer, "Desired: %t\nTarget: %s\nEnabled: %t\nServer: %s\nOwned: %t\nForeign: %t\n",
		status.Desired, emptyDash(status.Target), status.Observed.Enabled, emptyDash(status.Observed.Server),
		status.Observed.Owned, status.Observed.Foreign)
	return err
}
