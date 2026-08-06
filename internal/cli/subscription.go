package cli

import (
	"fmt"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
			if _, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\t%s\tenabled=%t\tcached=%t\tauto=%t\t%s\n", marker, profile.ID, profile.Name, profile.Enabled, profile.Cached, profile.AutoRefresh, effectiveInterval(profile.Interval, result.GlobalInterval)); err != nil {
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
	command := &cobra.Command{Use: "add NAME URL", Short: "Add a subscription", Args: cobra.ExactArgs(2), RunE: func(command *cobra.Command, args []string) error {
		client, err := subscriptionClient(dependencies)
		if err != nil {
			return err
		}
		id, err := operationID(dependencies)
		if err != nil {
			return err
		}
		result, err := client.AddSubscription(command.Context(), protocol.SubscriptionAddRequest{OperationID: id, IfRevision: revisionFlag(command, revision), Name: args[0], URL: args[1]})
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
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
			result, err = client.RefreshSubscription(command.Context(), args[0], request)
		} else {
			result, err = client.UseSubscription(command.Context(), args[0], request)
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
		result, err := client.SetSubscriptionEnabled(command.Context(), args[0], protocol.SubscriptionEnabledRequest{OperationID: id, IfRevision: revisionFlag(command, revision), Enabled: enabled})
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().Uint64Var(&revision, "if-revision", 0, "require this state revision")
	return command
}

func newSubscriptionSetCommand(dependencies Dependencies, options *runOptions) *cobra.Command {
	var name, rawURL, interval, globalInterval string
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
		if command.Flags().Changed("url") {
			request.URL = &rawURL
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
		if request.Name == nil && request.URL == nil && request.Interval == nil && request.AutoRefresh == nil && request.GlobalInterval == nil {
			return invalidArgument("at least one setting flag is required")
		}
		result, err := client.UpdateSubscription(command.Context(), args[0], request)
		if err != nil {
			return classifyRuntimeError(err)
		}
		return renderSubscriptionResult(command, options, result)
	}}
	command.Flags().StringVar(&name, "name", "", "new display name")
	command.Flags().StringVar(&rawURL, "url", "", "new private subscription URL")
	command.Flags().StringVar(&interval, "interval", "", "per-subscription interval; empty follows global")
	command.Flags().BoolVar(&autoRefresh, "auto-refresh", true, "enable scheduled refresh")
	command.Flags().StringVar(&globalInterval, "global-interval", "", "global refresh interval")
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
		result, err := client.RemoveSubscription(command.Context(), args[0], protocol.MutationRequest{OperationID: id, IfRevision: revisionFlag(command, revision)})
		if err != nil {
			return classifyRuntimeError(err)
		}
		if options.json {
			return renderJSON(command.OutOrStdout(), result)
		}
		return printMutation(command.OutOrStdout(), result)
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
	return printSubscription(command, result.Subscription)
}

func printSubscription(command *cobra.Command, profile protocol.Subscription) error {
	updated := "never"
	if !profile.UpdatedAt.IsZero() {
		updated = profile.UpdatedAt.Local().Format(time.RFC3339)
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "ID: %s\nName: %s\nEnabled: %t\nCached: %t\nAutomatic refresh: %t\nUpdated: %s\n", profile.ID, profile.Name, profile.Enabled, profile.Cached, profile.AutoRefresh, updated)
	return err
}

func effectiveInterval(profile, global string) string {
	if profile != "" {
		return profile
	}
	return global
}
