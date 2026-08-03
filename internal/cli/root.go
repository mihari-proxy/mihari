package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
)

type StatusClient interface {
	Status(context.Context) (protocol.Status, error)
}

type Dependencies struct {
	StatusClient StatusClient
	RunDaemon    func(context.Context) error
	SetupError   error
}

type runOptions struct {
	json bool
}

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies Dependencies) int {
	options := &runOptions{}
	root := newRoot(dependencies, options)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	options.json = wantsJSON(args)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	apiError := normalizeCommandError(err)
	if options.json {
		_ = json.NewEncoder(stderr).Encode(protocol.NewError(apiError.Code, apiError.Message, apiError.Details))
	} else {
		_, _ = fmt.Fprintf(stderr, "Error: %s\n", apiError.Message)
	}
	return exitCode(apiError)
}

func newRoot(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{
		Use:           "mihari",
		Short:         "Manage the local mihari daemon",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			if dependencies.SetupError == nil {
				return nil
			}
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "local control setup failed"}
		},
	}
	root.PersistentFlags().BoolVar(&options.json, "json", false, "write machine-readable JSON")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: err.Error()}
	})
	root.AddCommand(newStatusCommand(dependencies, options))
	root.AddCommand(newDaemonCommand(dependencies))
	return root
}

func normalizeCommandError(err error) protocol.APIError {
	var apiError protocol.APIError
	if errors.As(err, &apiError) {
		return apiError
	}
	message := err.Error()
	if strings.Contains(message, "unknown command") || strings.Contains(message, "unknown flag") || strings.Contains(message, "accepts ") {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message}
	}
	return protocol.APIError{Code: protocol.CodeInternal, Message: "internal error"}
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" || arg == "--json=true" {
			return true
		}
	}
	return false
}
