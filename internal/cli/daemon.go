package cli

import (
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

func newDaemonCommand(dependencies Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the mihari daemon in the foreground (also used by the OS service)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if dependencies.RunDaemon == nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "daemon runner is unavailable"}
			}
			return dependencies.RunDaemon(command.Context())
		},
	}
}
