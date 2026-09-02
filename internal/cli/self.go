package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/update"
	"github.com/spf13/cobra"
)

// SelfUpdater checks and updates the running mihari binary.
type SelfUpdater interface {
	Check(ctx context.Context, currentVersion, channel string) (update.CheckResult, error)
	Update(ctx context.Context, binaryPath, currentVersion, channel string) (update.Result, error)
}

func newSelfCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "self", Short: "Manage the mihari binary"}
	root.AddCommand(newSelfVersionCommand(options))
	root.AddCommand(newSelfChannelCommand(options))
	root.AddCommand(newSelfUpdateCommand(dependencies, options))
	return root
}

func newSelfChannelCommand(options *runOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "channel [main|dev]",
		Short: "Show or set the Mihari release channel",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			path, err := platform.ChannelPath()
			if err != nil {
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "resolve mihari channel path"}
			}
			if len(args) == 1 {
				if err := update.SaveChannel(path, args[0]); err != nil {
					return err
				}
			}
			channel, err := update.LoadChannel(path)
			if err != nil {
				return err
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "channel": channel})
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), channel)
			return err
		},
	}
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
	confirmed := false
	command := &cobra.Command{
		Use: "update", Short: "Update the mihari binary from GitHub Releases", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
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
			path, err := platform.ChannelPath()
			if err != nil {
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "resolve mihari channel path"}
			}
			channel, err := update.LoadChannel(path)
			if err != nil {
				return err
			}
			check, err := dependencies.SelfUpdater.Check(command.Context(), buildinfo.Version, channel)
			if err != nil {
				return classifyRuntimeError(err)
			}
			if check.Available && update.IsDowngrade(check.Current, check.Latest) && !confirmed {
				return invalidArgument("replacing Mihari with an older version requires --yes; older builds may not load current settings, subscriptions, or generated files")
			}
			result, err := dependencies.SelfUpdater.Update(command.Context(), binary, buildinfo.Version, channel)
			if err != nil {
				return classifyRuntimeError(err)
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), map[string]any{
					"schema":  "mihari/v1",
					"version": result.Version,
					"updated": result.Updated,
					"channel": result.Channel,
					"ahead":   result.Ahead,
				})
			}
			if result.Updated {
				_, err = fmt.Fprintf(command.OutOrStdout(), "updated to %s\n", result.Version)
			} else if result.Ahead {
				_, err = fmt.Fprintf(command.OutOrStdout(), "current %s is ahead of %s %s\n", buildinfo.Version, result.Channel, result.Version)
			} else {
				_, err = fmt.Fprintf(command.OutOrStdout(), "already up to date (%s)\n", result.Version)
			}
			return err
		},
	}
	command.Flags().BoolVar(&confirmed, "yes", false, "confirm replacing Mihari with an older version")
	return command
}
