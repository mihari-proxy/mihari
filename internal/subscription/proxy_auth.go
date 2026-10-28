package subscription

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"strings"
)

// ApplyProxyAuth merges instance credentials without mutating the source list.
func ApplyProxyAuth(document Document, auth protocol.ProxyAuthConfig) ([]protocol.ProxyAuthUser, error) {
	users := []protocol.ProxyAuthUser{}
	indexes := map[string]int{}
	if auth.Mode != "disabled" {
		if raw, exists := document["authentication"]; exists && raw != nil {
			items, ok := raw.([]any)
			if !ok {
				return nil, authError("subscription authentication must be a list")
			}
			for _, item := range items {
				text, ok := item.(string)
				name, password, found := strings.Cut(text, ":")
				if !ok || !found {
					// Match mihomo: records without a colon are ignored, and native empty fields remain source-owned.
					continue
				}
				user := protocol.ProxyAuthUser{Username: name, Password: password, Source: "subscription"}
				if i, found := indexes[name]; found {
					users[i] = user
				} else {
					indexes[name] = len(users)
					users = append(users, user)
				}
			}
		}
	}
	if auth.Mode == "enabled" {
		for _, user := range auth.Users {
			if i, found := indexes[user.Username]; found {
				user.Source = "overridden"
				users[i] = user
			} else {
				user.Source = "added"
				indexes[user.Username] = len(users)
				users = append(users, user)
			}
		}
		if len(users) == 0 {
			return nil, authError("proxy authentication is enabled but there are no users; add a user or reset proxy authentication")
		}
	}
	if auth.Mode != "follow" {
		values := make([]string, 0, len(users))
		for _, u := range users {
			values = append(values, u.Username+":"+u.Password)
		}
		document["authentication"] = values
	}
	if auth.Mode != "disabled" {
		switch auth.BypassMode {
		case "custom":
			values := []string{}
			if auth.LocalBypass {
				values = append(values, "127.0.0.0/8", "::1/128")
			}
			document["skip-auth-prefixes"] = append(values, auth.BypassPrefixes...)
		case "required":
			document["skip-auth-prefixes"] = []string{}
		}
	}
	return users, nil
}

func authError(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
