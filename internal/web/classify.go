package web

import (
	"net/http"
	"strings"
)

// Action is the gateway disposition for a browser API request.
type Action int

const (
	// ActionProxyRead forwards a safe read to the mihomo controller with injected secret.
	ActionProxyRead Action = iota
	// ActionProxyWS upgrades and proxies a streaming endpoint.
	ActionProxyWS
	// ActionMutateSelectProxy submits group selection through the coordinator.
	ActionMutateSelectProxy
	// ActionMutateClose closes one or all connections through the coordinator.
	ActionMutateClose
	// ActionMutateDelayTest runs a delay test through the coordinator.
	ActionMutateDelayTest
	// ActionMutateRestart restarts the supervised core through the coordinator.
	ActionMutateRestart
	// ActionMutateConfigs applies allowlisted config field patches through the coordinator.
	ActionMutateConfigs
	// ActionRejectUpgrade rejects core upgrade attempts owned by mihari.
	ActionRejectUpgrade
	// ActionRejectManaged rejects writes to mihari-managed controller/port/UI fields.
	ActionRejectManaged
	// ActionRejectUnknown is the default-deny for unrecognized write methods/paths.
	ActionRejectUnknown
	// ActionNotAPI marks non-API traffic (static/auth) that the classifier does not handle.
	ActionNotAPI
)

// Classify decides how the Web gateway handles a method+path pair.
// Unknown writes never fall through to the controller.
func Classify(method, path string) Action {
	method = strings.ToUpper(method)
	path = normalizeAPIPath(path)
	if path == "" {
		return ActionNotAPI
	}

	// WebSocket stream roots (method is typically GET).
	switch path {
	case "/traffic", "/memory", "/logs", "/connections":
		if method == http.MethodGet {
			// Ambiguous: /connections is both REST list and WS stream; callers that upgrade use WS.
			// REST GET /connections is a safe read; WS upgrade is handled by the server via headers.
			return ActionProxyRead
		}
	}

	if method == http.MethodGet || method == http.MethodHead {
		if isSafeReadPath(path) {
			return ActionProxyRead
		}
		// Unknown GET paths still proxy as reads for mihomo API compatibility (versioned panels).
		if looksLikeAPIPath(path) {
			return ActionProxyRead
		}
		return ActionNotAPI
	}

	// Explicit rejects first.
	if isUpgradePath(path) {
		return ActionRejectUpgrade
	}

	// Allowlisted mutations.
	if method == http.MethodPut && isProxyGroupSelect(path) {
		return ActionMutateSelectProxy
	}
	if method == http.MethodDelete && (path == "/connections" || strings.HasPrefix(path, "/connections/")) {
		return ActionMutateClose
	}
	if method == http.MethodPost && isDelayTestPath(path) {
		return ActionMutateDelayTest
	}
	if method == http.MethodPost && (path == "/restart" || path == "/configs/restart") {
		return ActionMutateRestart
	}
	if (method == http.MethodPatch || method == http.MethodPut) && path == "/configs" {
		return ActionMutateConfigs
	}

	if looksLikeAPIPath(path) {
		return ActionRejectUnknown
	}
	return ActionNotAPI
}

// ClassifyUpgrade distinguishes REST GET /connections from WebSocket upgrade.
func ClassifyUpgrade(method, path string, connectionUpgrade bool) Action {
	action := Classify(method, path)
	if connectionUpgrade && action == ActionProxyRead {
		p := normalizeAPIPath(path)
		switch p {
		case "/traffic", "/memory", "/logs", "/connections":
			return ActionProxyWS
		}
	}
	return action
}

func normalizeAPIPath(path string) string {
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// Strip trailing slash except root.
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

func looksLikeAPIPath(path string) bool {
	prefixes := []string{
		"/version", "/proxies", "/proxy-providers", "/rules", "/rule-providers",
		"/connections", "/configs", "/providers", "/cache", "/dns", "/traffic",
		"/memory", "/logs", "/upgrade", "/restart", "/group", "/script",
		"/meta", "/debug", "/ui", "/fakeip",
	}
	for _, prefix := range prefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func isSafeReadPath(path string) bool {
	switch {
	case path == "/version", path == "/configs":
		return true
	case strings.HasPrefix(path, "/proxies"), strings.HasPrefix(path, "/proxy-providers"):
		return true
	case strings.HasPrefix(path, "/rules"), strings.HasPrefix(path, "/rule-providers"):
		return true
	case path == "/connections", strings.HasPrefix(path, "/connections/"):
		return true
	case path == "/providers/proxies", strings.HasPrefix(path, "/providers/"):
		return true
	case path == "/dns/query", strings.HasPrefix(path, "/cache"):
		return true
	case path == "/traffic", path == "/memory", path == "/logs":
		return true
	default:
		return false
	}
}

func isUpgradePath(path string) bool {
	return path == "/upgrade" || strings.HasPrefix(path, "/upgrade/") ||
		path == "/upgrade/ui" || path == "/configs/upgrade"
}

func isProxyGroupSelect(path string) bool {
	// /proxies/{group} PUT selects; /proxies/{name} for non-group also used by some panels.
	if !strings.HasPrefix(path, "/proxies/") {
		return false
	}
	rest := strings.TrimPrefix(path, "/proxies/")
	if rest == "" || strings.Contains(rest, "/") {
		// /proxies/{name}/delay is delay-test, not select.
		return false
	}
	return true
}

func isDelayTestPath(path string) bool {
	if strings.HasSuffix(path, "/delay") && strings.HasPrefix(path, "/proxies/") {
		return true
	}
	if strings.HasPrefix(path, "/group/") && strings.HasSuffix(path, "/delay") {
		return true
	}
	return false
}
