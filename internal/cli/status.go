package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
			_, err = fmt.Fprintf(command.OutOrStdout(), "Daemon: %s\nHealth: %s\n",
				status.DaemonVersion, status.Health)
			if err != nil {
				return err
			}
			if status.LastError != "" {
				if _, err = fmt.Fprintf(command.OutOrStdout(), "Error: %s\n", status.LastError); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Revision: %d\nStarted: %s\n",
				status.Revision, status.StartedAt.Format("2006-01-02T15:04:05Z07:00"))
			if err == nil && status.Config != nil {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Config: %s (desired=%d observed=%d)\n", status.Config.Status, status.Config.DesiredRevision, status.Config.ObservedRevision)
			}
			return err
		},
	}
}
