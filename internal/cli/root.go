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

type RuntimeClient interface {
	Core(context.Context) (protocol.CoreStatus, error)
	InstallCore(context.Context, protocol.MutationRequest) (protocol.CoreInstallResult, error)
	RestartCore(context.Context, protocol.MutationRequest) (protocol.MutationResult, error)
	ProxyGroups(context.Context) (protocol.ProxyGroups, error)
	SelectProxy(context.Context, string, protocol.ProxySelectionRequest) (protocol.MutationResult, error)
	DelayTest(context.Context, string, protocol.DelayTestRequest) (protocol.DelayResult, error)
	Connections(context.Context) (protocol.ConnectionList, error)
	CloseConnection(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
	CloseAllConnections(context.Context, protocol.MutationRequest) (protocol.MutationResult, error)
	Rules(context.Context) (protocol.RuleList, error)
	Stream(context.Context, string, func(protocol.StreamEvent) error) error
}

type SubscriptionClient interface {
	Subscriptions(context.Context) (protocol.SubscriptionList, error)
	Subscription(context.Context, string) (protocol.SubscriptionResult, error)
	AddSubscription(context.Context, protocol.SubscriptionAddRequest) (protocol.SubscriptionResult, error)
	RefreshSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error)
	UseSubscription(context.Context, string, protocol.MutationRequest) (protocol.SubscriptionResult, error)
	SetSubscriptionEnabled(context.Context, string, protocol.SubscriptionEnabledRequest) (protocol.SubscriptionResult, error)
	UpdateSubscription(context.Context, string, protocol.SubscriptionUpdateRequest) (protocol.SubscriptionResult, error)
	RemoveSubscription(context.Context, string, protocol.MutationRequest) (protocol.MutationResult, error)
}

type Dependencies struct {
	StatusClient       StatusClient
	RuntimeClient      RuntimeClient
	SubscriptionClient SubscriptionClient
	PanelClient        PanelClient
	SystemProxyClient  SystemProxyClient
	ServiceController  ServiceController
	SelfUpdater        SelfUpdater
	OpenBrowser        func(url string) error
	RunDaemon          func(context.Context) error
	RunTUI             func(context.Context) error
	Interactive        bool
	NewOperationID     func() string
	SetupError         error
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
			if !dependencies.Interactive || options.json {
				return invalidArgument("interactive terminal required when no command is specified")
			}
			if dependencies.RunTUI == nil {
				return protocol.APIError{Code: protocol.CodeInvalidState, Message: "TUI is unavailable"}
			}
			return dependencies.RunTUI(command.Context())
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
	root.AddCommand(newCoreCommand(dependencies, options))
	root.AddCommand(newProxyCommand(dependencies, options))
	root.AddCommand(newConnectionsCommand(dependencies, options))
	root.AddCommand(newRulesCommand(dependencies, options))
	root.AddCommand(newStreamCommand("traffic", dependencies, options))
	root.AddCommand(newStreamCommand("logs", dependencies, options))
	root.AddCommand(newSubscriptionCommand(dependencies, options))
	root.AddCommand(newPanelCommand(dependencies, options))
	root.AddCommand(newSysproxyCommand(dependencies, options))
	root.AddCommand(newServiceCommand(dependencies, options))
	root.AddCommand(newSelfCommand(dependencies, options))
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

func invalidArgument(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message}
}
