package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

func newStatusCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if dependencies.StatusClient == nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "status client is unavailable"}
			}
			status, err := dependencies.StatusClient.Status(command.Context())
			if err != nil {
				var apiError protocol.APIError
				if errors.As(err, &apiError) {
					return apiError
				}
				return protocol.APIError{Code: protocol.CodeDaemonUnavailable, Message: "daemon is unavailable"}
			}
			if options.json {
				return json.NewEncoder(command.OutOrStdout()).Encode(status)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Daemon: %s\nHealth: %s\nRevision: %d\nStarted: %s\n",
				status.DaemonVersion, status.Health, status.Revision, status.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
			return err
		},
	}
}
