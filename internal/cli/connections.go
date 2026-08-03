package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newConnectionsCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "connections", Short: "Inspect and close connections"}
	root.AddCommand(&cobra.Command{
		Use: "list", Short: "List active connections", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			connections, err := client.Connections(command.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), connections)
			}
			for _, connection := range connections.Connections {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%d/%d\n", connection.ID, connection.Metadata.Host, connection.Upload, connection.Download); err != nil {
					return err
				}
			}
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "close ID", Short: "Close one connection", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			request, err := mutationRequest(dependencies)
			if err != nil {
				return err
			}
			result, err := client.CloseConnection(command.Context(), args[0], request)
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), result)
			}
			return printMutation(command.OutOrStdout(), result)
		},
	})
	confirmed := false
	closeAll := &cobra.Command{
		Use: "close-all", Short: "Close all connections", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !confirmed {
				return invalidArgument("--yes is required to close all connections")
			}
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			request, err := mutationRequest(dependencies)
			if err != nil {
				return err
			}
			result, err := client.CloseAllConnections(command.Context(), request)
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), result)
			}
			return printMutation(command.OutOrStdout(), result)
		},
	}
	closeAll.Flags().BoolVar(&confirmed, "yes", false, "confirm closing every connection")
	root.AddCommand(closeAll)
	return root
}
