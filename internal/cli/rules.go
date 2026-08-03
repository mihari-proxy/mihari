package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRulesCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "rules", Short: "Inspect rules"}
	root.AddCommand(&cobra.Command{
		Use: "list", Short: "List active rules", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			rules, err := client.Rules(command.Context())
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), rules)
			}
			for _, rule := range rules.Rules {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", rule.Type, rule.Payload, rule.Proxy); err != nil {
					return err
				}
			}
			return nil
		},
	})
	return root
}
