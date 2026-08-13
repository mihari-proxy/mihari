package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

// TunClient is the typed control surface used by tun CLI commands.
type TunClient interface {
	Tun(context.Context) (protocol.TunStatus, error)
	EnableTun(context.Context, protocol.TunMutationRequest) (protocol.TunStatus, error)
	DisableTun(context.Context, protocol.TunMutationRequest) (protocol.TunStatus, error)
}

func newTunCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "tun", Short: "Manage mihomo TUN mode"}
	root.AddCommand(newTunStatusCommand(dependencies, options))
	root.AddCommand(newTunEnableCommand(dependencies, options))
	root.AddCommand(newTunDisableCommand(dependencies, options))
	return root
}

func tunClient(dependencies Dependencies) (TunClient, error) {
	if dependencies.TunClient == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "tun client is unavailable"}
	}
	return dependencies.TunClient, nil
}

func newTunStatusCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show TUN status", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := tunClient(dependencies)
			if err != nil {
				return err
			}
			status, err := client.Tun(command.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			return renderTunStatus(command, options, status)
		},
	}
}

func newTunEnableCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	var force bool
	command := &cobra.Command{
		Use: "enable", Short: "Enable managed TUN via the daemon", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := tunClient(dependencies)
			if err != nil {
				return err
			}
			id, err := operationID(dependencies)
			if err != nil {
				return err
			}
			status, err := client.EnableTun(command.Context(), protocol.TunMutationRequest{
				OperationID: id,
				IfRevision:  revisionFlag(command, revision),
				Force:       force,
			})
			if err != nil {
				return classifyRuntimeError(err)
			}
			return renderTunStatus(command, options, status)
		},
	}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	command.Flags().BoolVar(&force, "force", false, "overwrite when other TUN adapters are detected")
	return command
}

func newTunDisableCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{
		Use: "disable", Short: "Disable managed TUN via the daemon", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := tunClient(dependencies)
			if err != nil {
				return err
			}
			id, err := operationID(dependencies)
			if err != nil {
				return err
			}
			status, err := client.DisableTun(command.Context(), protocol.TunMutationRequest{
				OperationID: id,
				IfRevision:  revisionFlag(command, revision),
			})
			if err != nil {
				return classifyRuntimeError(err)
			}
			return renderTunStatus(command, options, status)
		},
	}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func renderTunStatus(command *cobra.Command, options *runOptions, status protocol.TunStatus) error {
	if options.json {
		return renderJSON(command.OutOrStdout(), status)
	}
	return printTunStatus(command.OutOrStdout(), status)
}

func printTunStatus(writer io.Writer, status protocol.TunStatus) error {
	live := "-"
	if status.LiveEnable != nil {
		if *status.LiveEnable {
			live = "true"
		} else {
			live = "false"
		}
	}
	_, err := fmt.Fprintf(writer, "Desired: %t\nLive: %s\nStack: %s\nManaged: %t\nRevision: %d\n",
		status.DesiredEnable, live, emptyDash(status.Stack), status.Managed, status.Revision)
	if err != nil {
		return err
	}
	if status.LastError != "" {
		_, err = fmt.Fprintf(writer, "LastError: %s\n", status.LastError)
		if err != nil {
			return err
		}
	}
	if status.Conflict != nil {
		if len(status.Conflict.OtherTunInterfaces) > 0 {
			if _, err = fmt.Fprintf(writer, "Other TUN: %s\n", strings.Join(status.Conflict.OtherTunInterfaces, ", ")); err != nil {
				return err
			}
		}
		if len(status.Conflict.OtherMihomoProcesses) > 0 {
			if _, err = fmt.Fprintf(writer, "Other mihomo: %d\n", len(status.Conflict.OtherMihomoProcesses)); err != nil {
				return err
			}
		}
	}
	return err
}
