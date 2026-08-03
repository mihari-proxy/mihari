package cli

import (
	"errors"
	"fmt"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

var errOneStreamEvent = errors.New("received one stream event")

func newStreamCommand(kind string, dependencies Dependencies, options *runOptions) *cobra.Command {
	follow := false
	command := &cobra.Command{
		Use: kind, Short: "Stream mihomo " + kind, Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			err = client.Stream(command.Context(), kind, func(event protocol.StreamEvent) error {
				if options.json {
					if err := renderJSON(command.OutOrStdout(), event); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintln(command.OutOrStdout(), string(event.Data)); err != nil {
						return err
					}
				}
				if !follow {
					return errOneStreamEvent
				}
				return nil
			})
			if errors.Is(err, errOneStreamEvent) {
				return nil
			}
			return classifyRuntimeError(err)
		},
	}
	command.Flags().BoolVarP(&follow, "follow", "f", false, "continue following events")
	return command
}
