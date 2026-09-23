package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

type egressAPI interface {
	Egress(context.Context) (protocol.EgressStatus, error)
	UpdateEgress(context.Context, protocol.EgressUpdateRequest) (protocol.EgressStatus, error)
}

func newEgressCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "egress", Short: "Inspect or change the outbound network interface"}
	for _, action := range []string{"list", "status", "set", "auto"} {
		var revision uint64
		command := &cobra.Command{Use: action, Short: "Manage the instance outbound interface", Args: cobra.NoArgs}
		if action == "set" {
			command.Use = "set <interface-name>"
			command.Args = cobra.ExactArgs(1)
		}
		if action == "set" || action == "auto" {
			command.Flags().Uint64Var(&revision, "if-revision", 0, "Apply only if the state revision matches")
		}
		command.RunE = func(command *cobra.Command, args []string) error {
			client, err := runtimeClient(dependencies)
			if err != nil {
				return err
			}
			if dependencies.StatusClient != nil {
				observed, statusErr := dependencies.StatusClient.Status(command.Context())
				if statusErr != nil {
					return classifyRuntimeError(statusErr)
				}
				if !slices.Contains(observed.Capabilities, protocol.CapabilityEgress) {
					return protocol.APIError{Code: protocol.CodeInvalidState, Message: "daemon does not support outbound interface selection; upgrade Mihari"}
				}
			}
			egress, ok := client.(egressAPI)
			if !ok {
				return protocol.APIError{Code: protocol.CodeInvalidState, Message: "outbound interface capability is unavailable"}
			}
			var status protocol.EgressStatus
			if action == "set" || action == "auto" {
				id, idErr := operationID(dependencies)
				if idErr != nil {
					return idErr
				}
				request := protocol.EgressUpdateRequest{OperationID: id, Mode: "automatic"}
				if action == "set" {
					request.Mode = "manual"
					request.InterfaceName = args[0]
				}
				if command.Flags().Changed("if-revision") {
					request.IfRevision = &revision
				}
				status, err = egress.UpdateEgress(command.Context(), request)
			} else {
				status, err = egress.Egress(command.Context())
			}
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), status)
			}
			if action == "list" {
				for _, item := range status.Interfaces {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", strconv.Quote(item.Name), item.Availability, item.Reason); err != nil {
						return err
					}
				}
				return renderWarnings(command.ErrOrStderr(), status.WarningOutcome)
			}
			name := "No-Override"
			if status.Selection.Mode == "manual" {
				name = strconv.Quote(status.Selection.InterfaceName)
			}
			availability := ""
			for _, item := range status.Interfaces {
				if item.Name == status.Selection.InterfaceName {
					availability = " (" + item.Availability + ")"
				}
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Saved: %s%s\nState: %s\n", name, availability, status.State)
			if err != nil {
				return err
			}
			return renderWarnings(command.ErrOrStderr(), status.WarningOutcome)
		}
		root.AddCommand(command)
	}
	return root
}
