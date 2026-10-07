package cli

import (
	"context"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"

	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/spf13/cobra"
)

func newCoreCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "core", Short: "Manage the mihomo core"}
	root.AddCommand(&cobra.Command{
		Use: "status", Short: "Show mihomo core status", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			status, err := client.Core(command.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), status)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Core: %s\nVersion: %s\nPID: %d\nRestarts: %d\n", status.Status, status.Version, status.PID, status.Restarts)
			if err != nil {
				return err
			}
			if status.StartedAt.IsZero() {
				return nil
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Started: %s\n", status.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
			return err
		},
	})
	for _, name := range []string{"install", "update", "reinstall"} {
		name := name
		root.AddCommand(&cobra.Command{
			Use: name, Short: name + " the mihomo core", Args: cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				client, err := runtimeClient(dependencies)
				if err != nil {
					return err
				}
				request, err := mutationRequest(dependencies)
				if err != nil {
					return err
				}
				action, nameOfOperation := client.InstallCore, "core.install"
				if name == "reinstall" {
					repair, ok := client.(interface {
						ReinstallCore(context.Context, protocol.MutationRequest) (protocol.CoreInstallResult, error)
					})
					if !ok {
						return classifyRuntimeError(protocol.APIError{Code: protocol.CodeInvalidState, Message: "core reinstall unavailable"})
					}
					action, nameOfOperation = repair.ReinstallCore, "core.reinstall"
				}
				ctx := logging.WithOperation(command.Context(), logging.OperationMetadata{ID: request.OperationID, Name: nameOfOperation})
				result, err := action(ctx, request)
				if err != nil {
					return classifyRuntimeError(err)
				}
				if options.json {
					return renderJSON(command.OutOrStdout(), result)
				}
				message := "already current"
				if result.Updated {
					message = "installed"
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Core %s: %s\n", message, result.Version)
				if err != nil {
					return err
				}
				return renderWarnings(command.ErrOrStderr(), result.WarningOutcome)
			},
		})
	}
	root.AddCommand(&cobra.Command{
		Use: "restart", Short: "Restart the mihomo core", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			request, err := mutationRequest(dependencies)
			if err != nil {
				return err
			}
			ctx := logging.WithOperation(command.Context(), logging.OperationMetadata{ID: request.OperationID, Name: "core.restart"})
			result, err := client.RestartCore(ctx, request)
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), result)
			}
			return renderMutation(command, options, result)
		},
	})
	return root
}
