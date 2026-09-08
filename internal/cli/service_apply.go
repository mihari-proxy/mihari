package cli

import (
	"fmt"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

func newServiceApplyCommand(deps Dependencies, options *runOptions, euid func() int) *cobra.Command {
	var request string
	command := &cobra.Command{Use: "apply", Short: "Apply a verified Unix installation transaction", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if euid() == -1 || euid() != 0 {
			return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "service apply requires root"}
		}
		req, err := app.ReadInstallRequestFile(cmd.Context(), request)
		if err != nil {
			return err
		}
		if deps.ServiceApply == nil {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "service apply is unavailable"}
		}
		result, err := deps.ServiceApply(cmd.Context(), req)
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(cmd.OutOrStdout(), result)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "service %s (%s)\n", result.ServiceStatus, result.TransactionID)
		return err
	}}
	command.Flags().StringVar(&request, "request", "", "Absolute install request JSON file")
	return command
}
