package cli

import (
	"context"
	"fmt"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

type routingAPI interface {
	Routing(context.Context) (protocol.RoutingStatus, error)
	UpdateRouting(context.Context, protocol.RoutingUpdateRequest) (protocol.RoutingStatus, error)
}

func newRoutingCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "mode [rule|global|direct]", Short: "Inspect or change the persisted routing mode", Args: cobra.MaximumNArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if len(args) == 1 && !protocol.ValidRoutingMode(args[0]) {
			return invalidArgument("mode must be rule, global, or direct")
		}
		client, err := runtimeClient(dependencies)
		if err != nil {
			return err
		}
		routing, ok := client.(routingAPI)
		if !ok {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "routing mode capability is unavailable"}
		}
		var status protocol.RoutingStatus
		if len(args) == 0 {
			status, err = routing.Routing(command.Context())
		} else {
			id, idErr := operationID(dependencies)
			if idErr != nil {
				return idErr
			}
			status, err = routing.UpdateRouting(command.Context(), protocol.RoutingUpdateRequest{OperationID: id, Mode: args[0]})
		}
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), status)
		}
		live := status.LiveMode
		if live == "" {
			live = "unavailable"
		}
		global := status.GlobalSelection
		if global == "" {
			global = "not selected"
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Mode: %s (saved)\nLive: %s (%s)\nGLOBAL: %s\n", status.DesiredMode, live, status.State, global); err != nil {
			return err
		}
		if status.Message != "" {
			_, err = fmt.Fprintln(command.OutOrStdout(), status.Message)
		}
		return err
	}}
}
