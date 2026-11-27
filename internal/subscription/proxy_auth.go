package subscription

import (
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"strconv"
	"strings"
)

// ErrProxyAuthNoUsers identifies an enabled authentication configuration with no users.
var ErrProxyAuthNoUsers = errors.New("proxy authentication is enabled but there are no users; add a user or reset proxy authentication")

// ApplyProxyAuth updates authentication and bypass keys in document.
// Callers must pass a detached document; existing source list elements are never mutated.
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
			return nil, diagnostics.Wrap(authError(ErrProxyAuthNoUsers.Error()), ErrProxyAuthNoUsers)
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

func authError(message string) protocol.APIError {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}

// ApplyProxyAuthWithSources resolves users and bypasses against the fragment
// result, retaining the origin of each inherited username and final YAML line.
func ApplyProxyAuthWithSources(document Document, auth protocol.ProxyAuthConfig, sources map[string]string) ([]protocol.ProxyAuthUser, error) {
	raw, _ := document["authentication"].([]any)
	users, err := ApplyProxyAuth(document, auth)
	if err != nil || sources == nil {
		return users, err
	}
	const source = "Authentication form"
	inputSources := make(map[string]string, len(users))
	for i, item := range raw {
		entry, ok := item.(string)
		if username, _, found := strings.Cut(entry, ":"); ok && found {
			inputSources[username] = sources["authentication["+strconv.Itoa(i)+"]"]
		}
	}
	if auth.Mode != "follow" {
		sources["authentication"] = source
		for path := range sources {
			if strings.HasPrefix(path, "authentication[") {
				delete(sources, path)
			}
		}
	}
	for i := range users {
		user := &users[i]
		user.BaseSource = inputSources[user.Username]
		if auth.Mode != "follow" {
			label := source
			if user.Source == "subscription" {
				label = user.BaseSource
			}
			if label != "" {
				sources["authentication["+strconv.Itoa(i)+"]"] = label
			}
		}
	}
	if auth.Mode != "disabled" && (auth.BypassMode == "custom" || auth.BypassMode == "required") {
		sources["skip-auth-prefixes"] = source
		for path := range sources {
			if strings.HasPrefix(path, "skip-auth-prefixes[") {
				delete(sources, path)
			}
		}
		if list, ok := document["skip-auth-prefixes"].([]string); ok {
			for i := range list {
				sources["skip-auth-prefixes["+strconv.Itoa(i)+"]"] = source
			}
		}
	}
	return users, nil
}
