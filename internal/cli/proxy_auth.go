package cli

import (
	"context"
	"fmt"
	"github.com/charmbracelet/x/term"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/spf13/cobra"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

type proxyAuthClient interface {
	ProxyAuth(context.Context, bool) (protocol.ProxyAuthStatus, error)
	UpdateProxyAuth(context.Context, protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error)
}

func newProxyAuthCommand(deps Dependencies, options *runOptions) *cobra.Command {
	root := &cobra.Command{Use: "proxy-auth", Short: "Manage instance proxy usernames, passwords and authentication bypass"}
	user := &cobra.Command{Use: "user", Short: "Manage instance proxy credentials"}
	prefix := &cobra.Command{Use: "prefix", Short: "Manage custom authentication bypass CIDRs"}
	type action struct {
		parent *cobra.Command
		use    string
		args   int
		kind   string
	}
	actions := []action{{root, "status", 0, "status"}, {root, "mode <follow|enabled|disabled>", 1, "mode"}, {root, "bypass <follow|custom|required>", 1, "bypass"}, {root, "local-bypass <on|off>", 1, "local"}, {root, "reset", 0, "reset"}, {user, "set <username>", 1, "set"}, {user, "remove <username>", 1, "remove"}, {user, "reveal <username>", 1, "reveal"}, {prefix, "add <CIDR>", 1, "prefix-add"}, {prefix, "remove <CIDR>", 1, "prefix-remove"}}
	for _, a := range actions {
		var passwordStdin, yes bool
		command := &cobra.Command{Use: a.use, Short: "Manage proxy authentication", Args: cobra.ExactArgs(a.args)}
		if a.kind == "set" {
			command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Read a password from standard input")
		}
		if a.kind == "reset" {
			command.Flags().BoolVar(&yes, "yes", false, "Confirm clearing instance proxy credentials and restoring subscription authentication")
		}
		command.RunE = func(command *cobra.Command, args []string) error {
			client, err := runtimeClient(deps)
			if err != nil {
				return err
			}
			api, ok := client.(proxyAuthClient)
			if !ok {
				return invalidArgument("proxy authentication capability is unavailable; upgrade Mihari")
			}
			if a.kind == "reset" && !yes {
				return invalidArgument("reset restores subscription authentication and clears saved overrides; pass --yes to confirm")
			}
			status, err := api.ProxyAuth(command.Context(), a.kind == "reveal")
			if err != nil {
				return classifyRuntimeError(err)
			}
			if a.kind == "reveal" {
				users := append(append([]protocol.ProxyAuthUser{}, status.Users...), status.Config.Users...)
				for _, u := range users {
					if u.Username == args[0] {
						if options.json {
							return renderJSON(command.OutOrStdout(), struct {
								Schema string                 `json:"schema"`
								User   protocol.ProxyAuthUser `json:"user"`
							}{"mihari/v1", u})
						}
						_, err := fmt.Fprintf(command.OutOrStdout(), "%s\n", strconv.Quote(u.Password))
						return err
					}
				}
				return invalidArgument("proxy user not found")
			}
			if a.kind != "status" {
				cfg := status.Config
				switch a.kind {
				case "mode":
					cfg.Mode = args[0]
				case "bypass":
					cfg.BypassMode = args[0]
				case "local":
					if args[0] != "on" && args[0] != "off" {
						return invalidArgument("local-bypass must be on or off")
					}
					cfg.LocalBypass = args[0] == "on"
					cfg.BypassMode = "custom"
				case "prefix-add":
					cfg.BypassMode = "custom"
					cfg.BypassPrefixes = append(cfg.BypassPrefixes, args[0])
				case "prefix-remove":
					if !slices.Contains(cfg.BypassPrefixes, args[0]) {
						return invalidArgument("bypass CIDR not found")
					}
					cfg.BypassPrefixes = slices.DeleteFunc(cfg.BypassPrefixes, func(s string) bool { return s == args[0] })
				case "remove":
					found := slices.ContainsFunc(cfg.Users, func(u protocol.ProxyAuthUser) bool { return u.Username == args[0] })
					if !found {
						return invalidArgument("user has no instance override; excluding subscription users is not supported")
					}
					cfg.Users = slices.DeleteFunc(cfg.Users, func(u protocol.ProxyAuthUser) bool { return u.Username == args[0] })
				case "set":
					password, err := proxyPassword(command, passwordStdin, deps.Interactive && !options.json)
					if err != nil {
						return err
					}
					u := protocol.ProxyAuthUser{Username: args[0], Password: password}
					i := slices.IndexFunc(cfg.Users, func(u protocol.ProxyAuthUser) bool { return u.Username == args[0] })
					if i >= 0 {
						cfg.Users[i] = u
					} else {
						cfg.Users = append(cfg.Users, u)
					}
				}
				id, err := operationID(deps)
				if err != nil {
					return err
				}
				status, err = api.UpdateProxyAuth(command.Context(), protocol.ProxyAuthUpdateRequest{OperationID: id, IfRevision: &status.Revision, Config: cfg, Reset: a.kind == "reset"})
				if err != nil {
					return classifyRuntimeError(err)
				}
			}
			if options.json {
				return renderJSON(command.OutOrStdout(), status)
			}
			if status.Problem != "" {
				if _, err := fmt.Fprintln(command.ErrOrStderr(), status.Problem); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Authentication: %s\nBypass: %s\n", status.Config.Mode, status.Config.BypassMode); err != nil {
				return err
			}
			for _, u := range status.Users {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", strconv.Quote(u.Username), u.Source); err != nil {
					return err
				}
			}
			return renderWarnings(command.ErrOrStderr(), status.WarningOutcome)
		}
		a.parent.AddCommand(command)
	}
	root.AddCommand(user, prefix)
	return root
}
func proxyPassword(command *cobra.Command, stdin, interactive bool) (string, error) {
	if stdin {
		content, err := io.ReadAll(io.LimitReader(command.InOrStdin(), 258))
		if err != nil {
			return "", err
		}
		if len(content) > 257 {
			return "", invalidArgument("password input is too large")
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(content), "\n"), "\r"), nil
	}
	if !interactive {
		return "", invalidArgument("use --password-stdin outside an interactive terminal")
	}
	file, ok := command.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return "", invalidArgument("password input requires a terminal or --password-stdin")
	}
	if _, err := fmt.Fprint(command.ErrOrStderr(), "Password: "); err != nil {
		return "", err
	}
	content, err := term.ReadPassword(file.Fd())
	if _, writeErr := fmt.Fprintln(command.ErrOrStderr()); writeErr != nil {
		return "", writeErr
	}
	return string(content), err
}
