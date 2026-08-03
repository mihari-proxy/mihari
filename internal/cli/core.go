package cli

import (
	"fmt"

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
			return err
		},
	})
	for _, name := range []string{"install", "update"} {
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
				result, err := client.InstallCore(command.Context(), request)
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
				return err
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
			result, err := client.RestartCore(command.Context(), request)
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), result)
			}
			return printMutation(command.OutOrStdout(), result)
		},
	})
	return root
}
