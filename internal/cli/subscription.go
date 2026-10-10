package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"github.com/spf13/cobra"
)

func newSubscriptionCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "sub", Aliases: []string{"subscription", "subscriptions"}, Short: "Manage subscriptions"}
	root.AddCommand(newSubscriptionListCommand(dependencies, options))
	root.AddCommand(newSubscriptionShowCommand(dependencies, options))
	root.AddCommand(newSubscriptionAddCommand(dependencies, options))
	root.AddCommand(newSubscriptionSimpleCommand("refresh", dependencies, options))
	root.AddCommand(newSubscriptionSimpleCommand("use", dependencies, options))
	root.AddCommand(newSubscriptionEnabledCommand("enable", true, dependencies, options))
	root.AddCommand(newSubscriptionEnabledCommand("disable", false, dependencies, options))
	root.AddCommand(newSubscriptionSetCommand(dependencies, options))
	root.AddCommand(newSubscriptionRemoveCommand(dependencies, options))
	return root
}

func newSubscriptionListCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List subscriptions", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		result, err := client.Subscriptions(command.Context())
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), result)
		}
		for _, profile := range result.Subscriptions {
			marker := " "
			if profile.ID == result.ActiveID {
				marker = "*"
			}
			if profile.SourceType == "file" {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\t%s\tenabled=%t\tcached=%t\tauto=%t\tsource=file\t%s\n", marker, profile.ID, profile.Name, profile.Enabled, profile.Cached, profile.AutoRefresh, effectiveInterval(profile.Interval, result.GlobalInterval)); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\t%s\tenabled=%t\tcached=%t\tauto=%t\tproxy=%s\t%s\n", marker, profile.ID, profile.Name, profile.Enabled, profile.Cached, profile.AutoRefresh, proxyModeLabel(profile.ProxyMode), effectiveInterval(profile.Interval, result.GlobalInterval)); err != nil {
				return err
			}
		}
		return nil
	}}
}

func newSubscriptionShowCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	return &cobra.Command{Use: "show ID", Short: "Show a redacted subscription", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		result, err := client.Subscription(command.Context(), args[0])
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), result)
		}
		return printSubscription(command, result.Subscription)
	}}
}

func newSubscriptionAddCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	var proxyMode, file string
	var allowReferences bool
	command := &cobra.Command{Use: "add NAME [URL]", Short: "Import and cache a YAML subscription from URL or --file", Args: cobra.RangeArgs(1, 2), RunE: func(command *cobra.Command, args []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		source := ""
		if command.Flags().Changed("file") {
			if len(args) != 1 || command.Flags().Changed("proxy") {
				return invalidArgument("--file cannot be combined with URL or --proxy")
			}
			source, err = platform.FileURI(file)
			if err != nil {
				return invalidArgument(err.Error())
			}
		} else {
			if len(args) != 2 {
				return invalidArgument("provide URL or --file with an absolute path")
			}
			source = args[1]
		}
		mode := resolveProxyFlag(proxyMode)
		if !subscription.ValidProxyMode(mode) {
			return invalidArgument(fmt.Sprintf("invalid --proxy mode %q (use direct, proxy, or auto)", proxyMode))
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		request := protocol.SubscriptionAddRequest{OperationID: id, IfRevision: revisionFlag(command, revision), Name: args[0], URL: source, ProxyMode: mode, AllowFileReferences: allowReferences}
		result, err := client.AddSubscription(subscriptionOperationContext(command.Context(), id, "subscription.add"), request)
		var api protocol.APIError
		if err != nil && errors.As(err, &api) && api.Code == protocol.CodeInvalidArgument && api.Details["confirmation_required"] == "file_references" && dependencies.Interactive && !options.json {
			if _, writeErr := fmt.Fprintln(command.ErrOrStderr(), diagnostics.EscapeTerminal(api.Message)+" Continue? [y/N]"); writeErr != nil {
				return writeErr
			}
			scanner := bufio.NewScanner(command.InOrStdin())
			accepted := scanner.Scan() && (strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") || strings.EqualFold(strings.TrimSpace(scanner.Text()), "yes"))
			if scanner.Err() != nil {
				return invalidArgument("could not read file reference confirmation")
			}
			if !accepted {
				return invalidArgument("subscription creation cancelled")
			}
			request.OperationID, err = operationID(dependencies)
			if err != nil {
				return err
			}
			request.AllowFileReferences = true
			result, err = client.AddSubscription(subscriptionOperationContext(command.Context(), request.OperationID, "subscription.add"), request)
		}
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	command.Flags().StringVar(&proxyMode, "proxy", "", "refresh fetch mode: direct (default), proxy, or auto")
	command.Flags().StringVar(&file, "file", "", "absolute YAML file path or file URI, read by daemon")
	command.Flags().BoolVar(&allowReferences, "allow-file-references", false, "accept native mihomo local file references and their runtime effects")
	return command
}

func newSubscriptionSimpleCommand(action string, dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{Use: action + " ID", Short: action + " a subscription", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		request := protocol.MutationRequest{OperationID: id, IfRevision: revisionFlag(command, revision)}
		var result protocol.SubscriptionResult
		if action == "refresh" {
			result, err = client.RefreshSubscription(subscriptionOperationContext(command.Context(), id, "subscription.refresh"), args[0], request)
		} else {
			result, err = client.UseSubscription(subscriptionOperationContext(command.Context(), id, "subscription.use"), args[0], request)
		}
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newSubscriptionEnabledCommand(action string, enabled bool, dependencies Dependencies, options *runOptions) *cobra.Command {
	var revision uint64
	command := &cobra.Command{Use: action + " ID", Short: action + " a subscription", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		result, err := client.SetSubscriptionEnabled(subscriptionOperationContext(command.Context(), id, "subscription.enabled"), args[0], protocol.SubscriptionEnabledRequest{OperationID: id, IfRevision: revisionFlag(command, revision), Enabled: enabled})
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newSubscriptionSetCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var name, rawURL, interval, globalInterval, proxyMode, file string
	var autoRefresh bool
	var revision uint64
	command := &cobra.Command{Use: "set ID", Short: "Change subscription settings", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		request := protocol.SubscriptionUpdateRequest{OperationID: id, IfRevision: revisionFlag(command, revision)}
		if command.Flags().Changed("name") {
			request.Name = &name
		}
		if command.Flags().Changed("url") && command.Flags().Changed("file") {
			return invalidArgument("--url and --file are mutually exclusive")
		}
		if command.Flags().Changed("url") {
			request.URL = &rawURL
		}
		if command.Flags().Changed("file") {
			source, err := platform.FileURI(file)
			if err != nil {
				return invalidArgument(err.Error())
			}
			if command.Flags().Changed("proxy") {
				return invalidArgument("--file cannot be combined with --proxy")
			}
			request.URL = &source
		}
		if command.Flags().Changed("interval") {
			request.Interval = &interval
		}
		if command.Flags().Changed("auto-refresh") {
			request.AutoRefresh = &autoRefresh
		}
		if command.Flags().Changed("global-interval") {
			request.GlobalInterval = &globalInterval
		}
		if command.Flags().Changed("proxy") {
			mode := resolveProxyFlag(proxyMode)
			if !subscription.ValidProxyMode(mode) {
				return invalidArgument(fmt.Sprintf("invalid --proxy mode %q (use direct, proxy, or auto)", proxyMode))
			}
			request.ProxyMode = &mode
		}
		if request.Name == nil && request.URL == nil && request.Interval == nil && request.AutoRefresh == nil && request.GlobalInterval == nil && request.ProxyMode == nil {
			return invalidArgument("at least one setting flag is required")
		}
		result, err := client.UpdateSubscription(subscriptionOperationContext(command.Context(), id, "subscription.set"), args[0], request)
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().StringVar(&name, "name", "", "new display name")
	command.Flags().StringVar(&rawURL, "url", "", "new private subscription URL")
	command.Flags().StringVar(&file, "file", "", "new absolute YAML file path or file URI; source type stays fixed")
	command.Flags().StringVar(&interval, "interval", "", "per-subscription interval; empty follows global")
	command.Flags().BoolVar(&autoRefresh, "auto-refresh", true, "enable scheduled refresh")
	command.Flags().StringVar(&globalInterval, "global-interval", "", "global refresh interval")
	command.Flags().StringVar(&proxyMode, "proxy", "", "refresh fetch mode: direct (default), proxy, or auto")
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newSubscriptionRemoveCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var yes bool
	var revision uint64
	command := &cobra.Command{Use: "remove ID", Short: "Remove a subscription and its cache", Args: cobra.ExactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if !yes {
			return invalidArgument("subscription removal requires --yes")
		}
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		result, err := client.RemoveSubscription(subscriptionOperationContext(command.Context(), id, "subscription.remove"), args[0], protocol.MutationRequest{OperationID: id, IfRevision: revisionFlag(command, revision)})
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), result)
		}
		return renderMutation(command, options, result)
	}}
	command.Flags().BoolVar(&yes, "yes", false, "confirm permanent removal")
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func subscriptionClient(dependencies Dependencies) (SubscriptionClient, error) {
	if dependencies.SubscriptionClient == nil {
		return nil, protocol.APIError{Code: protocol.CodeInternal, Message: "subscription client is unavailable"}
	}
	return dependencies.SubscriptionClient, nil
}

func subscriptionOperationContext(ctx context.Context, id, name string) context.Context {
	return logging.WithOperation(ctx, logging.OperationMetadata{ID: id, Name: name})
}

func revisionFlag(command *cobra.Command, value uint64) *uint64 {
	if !command.Flags().Changed("if-revision") {
		return nil
	}
	return &value
}

func renderSubscriptionResult(command *cobra.Command, options *runOptions, result protocol.SubscriptionResult) error {
	if options.json {
		return renderJSON(command.OutOrStdout(), result)
	}
	if err := printSubscription(command, result.Subscription); err != nil {
		return err
	}
	return renderWarnings(command.ErrOrStderr(), result.WarningOutcome)
}

func printSubscription(command *cobra.Command, profile protocol.Subscription) error {
	updated := "never"
	if !profile.UpdatedAt.IsZero() {
		updated = profile.UpdatedAt.Local().Format(time.RFC3339)
	}
	if profile.SourceType == "file" {
		_, err := fmt.Fprintf(command.OutOrStdout(), "ID: %s\nName: %s\nSource: Local file\nEnabled: %t\nCached: %t\nAutomatic refresh: %t\nUpdated: %s\n", profile.ID, profile.Name, profile.Enabled, profile.Cached, profile.AutoRefresh, updated)
		return err
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "ID: %s\nName: %s\nEnabled: %t\nCached: %t\nAutomatic refresh: %t\nProxy mode: %s\nUpdated: %s\n", profile.ID, profile.Name, profile.Enabled, profile.Cached, profile.AutoRefresh, proxyModeLabel(profile.ProxyMode), updated)
	return err
}

// proxyModeLabel renders a stored proxy mode for humans; the zero value is direct.
func proxyModeLabel(mode string) string {
	if mode == "" {
		return "direct"
	}
	return mode
}

// resolveProxyFlag maps the human-facing --proxy token to the stored proxy mode.
// "direct" is accepted as the spelling of the zero value (direct).
func resolveProxyFlag(mode string) string {
	if mode == "direct" {
		return ""
	}
	return mode
}

func effectiveInterval(profile, global string) string {
	if profile != "" {
		return profile
	}
	return global
}
