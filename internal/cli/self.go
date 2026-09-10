package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/update"
	"github.com/spf13/cobra"
)

// SelfUpdater updates the running mihari binary.
type SelfUpdater interface {
	Prepare(ctx context.Context, binaryPath, currentVersion, channel string) (update.PreparedUpdate, error)
	ApplyPrepared(context.Context, update.PreparedUpdate) (update.Result, error)
}

func newSelfCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "self", Short: "Manage the mihari binary"}
	root.AddCommand(newSelfVersionCommand(options))
	root.AddCommand(newSelfChannelCommand(dependencies, options))
	root.AddCommand(newSelfUpdateCommand(dependencies, options))
	return root
}

func newSelfChannelCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "channel [main|dev]",
		Short: "Show or set the Mihari release channel",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if dependencies.ChannelQuery != nil {
				if len(args) == 1 {
					if dependencies.ChannelSet == nil {
						return protocol.APIError{Code: protocol.CodeInvalidState, Message: "channel maintenance unavailable"}
					}
					if err := dependencies.ChannelSet(command.Context(), args[0]); err != nil {
						return err
					}
				}
				channel, err := dependencies.ChannelQuery(command.Context())
				if err != nil {
					return err
				}
				if options.json {
					return renderJSON(command.OutOrStdout(), map[string]any{"schema": "mihari/v1", "channel": channel})
				}
				_, err = fmt.Fprintln(command.OutOrStdout(), channel)
				return err
			}
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
	var yes bool
	command := &cobra.Command{Use: "update", Short: "Update the mihari binary from GitHub Releases", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) (resultErr error) {
		if err := elevate.RequireElevated(); err != nil {
			return err
		}
		if dependencies.SelfUpdater == nil {
			return protocol.APIError{Code: protocol.CodeInternal, Message: "self updater is unavailable"}
		}
		ctx := localTaskContext(command.Context(), dependencies, "self.update")
		var taskErr error
		defer func() { reportLocalTaskFailure(ctx, dependencies, "self.update.failed", taskErr) }()
		binary, err := os.Executable()
		if err != nil {
			taskErr = err
			return protocol.APIError{Code: protocol.CodeInternal, Message: "resolve mihari executable path"}
		}
		channel := ""
		if dependencies.SelfUpdateChannel != nil {
			channel, err = dependencies.SelfUpdateChannel(ctx)
		} else {
			path, pathErr := platform.ChannelPath()
			if pathErr != nil {
				taskErr = pathErr
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "resolve mihari channel path"}
			}
			channel, err = update.LoadChannel(path)
		}
		if err != nil {
			taskErr = err
			return err
		}
		prepared, err := dependencies.SelfUpdater.Prepare(ctx, binary, buildinfo.Version, channel)
		if err != nil {
			taskErr = err
			return classifyRuntimeError(err)
		}
		defer func() {
			closeErr := prepared.Close()
			resultErr = errors.Join(resultErr, closeErr)
			if closeErr != nil {
				if taskErr == nil {
					taskErr = closeErr
				} else {
					taskErr = errors.Join(taskErr, closeErr)
				}
			}
		}()
		prepared.Consent = update.ReplacementConsent{Yes: yes}
		var warning string
		if prepared.Available {
			warning = update.ReplacementWarning(prepared.Preview)
			if err := update.ValidateReplacementConsent(prepared.Preview, prepared.Consent); err != nil {
				return err
			}
		}
		if warning != "" && !options.json {
			if err := renderReplacementWarning(command, options, warning, nil); err != nil {
				return err
			}
			warning = ""
		}
		result, err := dependencies.SelfUpdater.ApplyPrepared(ctx, prepared)
		taskErr = err
		closeErr := prepared.Close()
		err = errors.Join(err, closeErr)
		if closeErr != nil {
			if taskErr == nil {
				taskErr = closeErr
			} else {
				taskErr = errors.Join(taskErr, closeErr)
			}
		}
		if err = renderReplacementWarning(command, options, warning, err); err != nil {
			return err
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
	}}
	command.Flags().BoolVar(&yes, "yes", false, "Accept replacement compatibility risks")
	return command
}

// renderReplacementWarning preserves the single JSON error envelope on failure.
func renderReplacementWarning(command *cobra.Command, options *runOptions, warning string, operationErr error) error {
	if operationErr != nil {
		classified := classifyRuntimeError(operationErr)
		if warning != "" && options.json {
			api := normalizeCommandError(classified)
			api.Message = warning + " " + api.Message
			return api
		}
		if warning != "" && !options.json {
			_, writeErr := fmt.Fprintln(command.ErrOrStderr(), warning)
			return errors.Join(classified, writeErr)
		}
		return classified
	}
	if warning != "" {
		_, err := fmt.Fprintln(command.ErrOrStderr(), warning)
		return err
	}
	return nil
}
