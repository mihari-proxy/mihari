package cli

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

func newDaemonCommand(dependencies Dependencies) *cobra.Command {
	var validationID string
	var systemService bool
	var launchdProcessGroup bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the mihari daemon in the foreground (also used by the OS service)",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if command.Flags().Changed("system-service") && !systemService {
				return invalidArgument("system service marker must be enabled")
			}
			if systemService && command.Flags().Changed("install-validation") {
				return invalidArgument("service and validation startup modes are mutually exclusive")
			}
			if command.Flags().Changed("launchd-process-group") {
				if !launchdProcessGroup || !systemService || command.Flags().Changed("install-validation") || dependencies.RunLaunchdServiceDaemon == nil {
					return invalidArgument("launchd process group requires the installed launchd service")
				}
				return dependencies.RunLaunchdServiceDaemon(command.Context())
			}
			if systemService {
				if dependencies.RunSystemServiceDaemon == nil {
					return invalidArgument("system service runner is unavailable")
				}
				return dependencies.RunSystemServiceDaemon(command.Context())
			}
			if command.Flags().Changed("install-validation") && validationID == "" {
				return invalidArgument("install validation transaction is required")
			}
			if validationID != "" {
				if dependencies.RunInstallValidation == nil {
					return protocol.APIError{Code: protocol.CodeInternal, Message: "install validation runner is unavailable"}
				}
				return dependencies.RunInstallValidation(command.Context(), validationID)
			}
			if dependencies.RunDaemon == nil {
				return protocol.APIError{Code: protocol.CodeInternal, Message: "daemon runner is unavailable"}
			}
			return dependencies.RunDaemon(command.Context())
		},
	}
	cmd.Flags().StringVar(&validationID, "install-validation", "", "run the no-business install validation child")
	cmd.Flags().BoolVar(&launchdProcessGroup, "launchd-process-group", false, "share the installed launchd process group")
	// The registered flag exists; hiding it cannot fail.
	_ = cmd.Flags().MarkHidden("launchd-process-group")
	if dependencies.RunSystemServiceDaemon != nil || dependencies.RunLaunchdServiceDaemon != nil {
		cmd.Flags().BoolVar(&systemService, "system-service", false, "run the installed Unix system service")
		// The registered flag exists; hiding it cannot fail.
		_ = cmd.Flags().MarkHidden("system-service")
	}
	return cmd
}
