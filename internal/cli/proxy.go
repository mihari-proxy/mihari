package cli

import (
	"fmt"
	"sort"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

func newProxyCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "proxy", Short: "Inspect and select proxies"}
	root.AddCommand(&cobra.Command{
		Use: "groups", Short: "List proxy groups", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			groups, err := client.ProxyGroups(command.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), groups)
			}
			for _, group := range groups.Groups {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", group.Name, group.Type, group.Now); err != nil {
					return err
				}
			}
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "select GROUP PROXY", Short: "Select a proxy in a group", Args: cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			id, err := operationID(dependencies)
			if err != nil {
				return err
			}
			result, err := client.SelectProxy(command.Context(), args[0], protocol.ProxySelectionRequest{OperationID: id, Name: args[1]})
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), result)
			}
			return printMutation(command.OutOrStdout(), result)
		},
	})
	testURL := "https://www.gstatic.com/generate_204"
	timeout := 5000
	testCommand := &cobra.Command{
		Use: "test GROUP", Short: "Test proxy-group delays", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			result, err := client.DelayTest(command.Context(), args[0], protocol.DelayTestRequest{URL: testURL, TimeoutMilliseconds: timeout})
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), result)
			}
			names := make([]string, 0, len(result.Delays))
			for name := range result.Delays {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%d ms\n", name, result.Delays[name]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	testCommand.Flags().StringVar(&testURL, "url", testURL, "URL used for delay testing")
	testCommand.Flags().IntVar(&timeout, "timeout", timeout, "timeout in milliseconds")
	root.AddCommand(testCommand)
	return root
}
