package cli

import (
	"fmt"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/update"
	"github.com/spf13/cobra"
)

func newServiceApplyCommand(deps Dependencies, options *runOptions, euid func() int) *cobra.Command {
	var request, expected string
	var yes bool
	command := &cobra.Command{Use: "apply", Short: "Apply a verified Unix installation transaction", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if euid() == -1 || euid() != 0 {
			return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "service apply requires root"}
		}
		consent := update.ReplacementConsent{Yes: yes, ExpectedPreview: expected}
		// Validate the public flag grammar before request IO or installer dispatch.
		if cmd.Flags().Changed("expected-preview") && expected == "" {
			return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "--expected-preview requires --yes and a valid preview ID"}
		}
		if err := update.ValidateReplacementConsent(update.ReplacementPreview{Risk: update.ReplacementNone, ID: expected}, consent); err != nil {
			return err
		}
		req, err := app.ReadInstallRequestFile(cmd.Context(), request)
		if err != nil {
			return err
		}
		if deps.ServiceApply == nil {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "service apply is unavailable"}
		}
		var warning string
		consent.Warn = func(message string) error {
			if !options.json {
				_, err := fmt.Fprintln(cmd.ErrOrStderr(), message)
				return err
			}
			warning = message
			return nil
		}
		ctx := localTaskContext(cmd.Context(), deps, "service.apply")
		result, err := deps.ServiceApply(ctx, req, consent)
		reportLocalTaskFailure(ctx, deps, "service.apply.failed", err)
		if err = renderReplacementWarning(cmd, options, warning, err); err != nil {
			return err
		}
		if options.json {
			return renderJSON(cmd.OutOrStdout(), result)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "service %s (%s)\n", result.ServiceStatus, result.TransactionID)
		return err
	}}
	command.Flags().StringVar(&request, "request", "", "Absolute install request JSON file")
	command.Flags().BoolVar(&yes, "yes", false, "Accept replacement compatibility risks")
	command.Flags().StringVar(&expected, "expected-preview", "", "Require the previously reviewed replacement preview")
	return command
}
