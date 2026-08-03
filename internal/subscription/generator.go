package subscription

import (
	"net/netip"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

func Generate(base Document, overrides map[string]any, settings config.Settings) ([]byte, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	if settings.ControllerSecret == "" {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "controller secret is required"}
	}
	document, err := cloneDocument(base)
	if err != nil {
		return nil, err
	}
	for key, value := range overrides {
		document[key] = value
	}
	ensureRoutable(document)
	mixed, err := netip.ParseAddrPort(settings.MixedAddr)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid mixed address"}
	}
	document["mixed-port"] = int(mixed.Port())
	document["bind-address"] = mixed.Addr().String()
	document["allow-lan"] = false
	document["external-controller"] = settings.ControllerAddr
	document["secret"] = settings.ControllerSecret
	delete(document, "external-ui")
	delete(document, "external-ui-name")
	delete(document, "external-ui-url")
	content, err := yaml.Marshal(document)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "encode generated configuration"}
	}
	return content, nil
}

func cloneDocument(base Document) (Document, error) {
	content, err := yaml.Marshal(base)
	if err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "copy subscription document"}
	}
	var clone Document
	if err := yaml.Unmarshal(content, &clone); err != nil {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "copy subscription document"}
	}
	return clone, nil
}

func ensureRoutable(document Document) {
	if groups, ok := document["proxy-groups"].([]any); !ok || len(groups) == 0 {
		names := proxyNames(document["proxies"])
		choices := make([]any, 0, len(names)+1)
		for _, name := range names {
			choices = append(choices, name)
		}
		choices = append(choices, "DIRECT")
		document["proxy-groups"] = []any{map[string]any{"name": "MIHARI", "type": "select", "proxies": choices}}
	}
	if rules, ok := document["rules"].([]any); !ok || len(rules) == 0 {
		document["rules"] = []any{"MATCH,MIHARI"}
	}
	if _, exists := document["mode"]; !exists {
		document["mode"] = "rule"
	}
	if _, exists := document["log-level"]; !exists {
		document["log-level"] = "info"
	}
}

func proxyNames(value any) []string {
	proxies, _ := value.([]any)
	names := make([]string, 0, len(proxies))
	for _, raw := range proxies {
		proxy, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := proxy["name"].(string)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}
