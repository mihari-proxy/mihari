package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/update"
	"github.com/spf13/cobra"
)

// SelfUpdater updates the running mihari binary.
type SelfUpdater interface {
	Update(ctx context.Context, binaryPath, currentVersion string) (update.Result, error)
}

func newSelfCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "self", Short: "Manage the mihari binary"}
	root.AddCommand(newSelfVersionCommand(options))
	root.AddCommand(newSelfUpdateCommand(dependencies, options))
	return root
}

func newSelfVersionCommand(options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print mihari version", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		version := buildinfo.Version
		if options.json {
			return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "version": version})
		}
		_, err := fmt.Fprintln(command.OutOrStdout(), version)
		return err
	}}
}

func newSelfUpdateCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "update", Short: "Update the mihari binary from GitHub Releases", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if err := elevate.RequireElevated(); err != nil {
			return err
		}
		if dependencies.SelfUpdater == nil {
			return protocol.APIError{Code: protocol.CodeInternal, Message: "self updater is unavailable"}
		}
		binary, err := os.Executable()
		if err != nil {
			return protocol.APIError{Code: protocol.CodeInternal, Message: "resolve mihari executable path"}
		}
		result, err := dependencies.SelfUpdater.Update(command.Context(), binary, buildinfo.Version)
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), map[string]any{
				"schema": "mihari/v1", "version": result.Version, "updated": result.Updated,
			})
		}
		if result.Updated {
			_, err = fmt.Fprintf(command.OutOrStdout(), "updated to %s\n", result.Version)
		} else {
			_, err = fmt.Fprintf(command.OutOrStdout(), "already up to date (%s)\n", result.Version)
		}
		return err
	}}
}
