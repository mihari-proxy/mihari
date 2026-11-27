package subscription

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// applyEgress edits only the cloned effective configuration. Native proxy-chain
// and DNS proxy selection retain their meaning; automatic leaves them untouched.
func applyEgress(document Document, name string, sources map[string]string) error {
	if name == "" {
		return nil
	}
	document["interface-name"] = name
	sources["interface-name"] = "Mihari"
	proxyNames := map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "PASS-RULE": true, "GLOBAL": true, "COMPATIBLE": true, "RULES": true}
	for _, key := range []string{"proxies", "proxy-groups"} {
		items, _ := document[key].([]any)
		for i, raw := range items {
			item := egressMapping(raw)
			if item == nil {
				continue
			}
			if n, ok := item["name"].(string); ok {
				proxyNames[n] = true
			}
			if key == "proxies" {
				item["interface-name"] = name
				sources["proxies["+strconv.Itoa(i)+"].interface-name"] = "Mihari"
			}
		}
	}
	providers := egressMapping(document["proxy-providers"])
	for providerName, raw := range providers {
		provider := egressMapping(raw)
		if provider == nil {
			continue
		}
		override := egressMapping(provider["override"])
		if override == nil {
			override = make(map[string]any)
		}
		override["interface-name"] = name
		provider["override"] = override
		sources["proxy-providers."+providerName+".override.interface-name"] = "Mihari"
	}
	dns := egressMapping(document["dns"])
	for _, key := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver", "direct-nameserver", "nameserver-policy"} {
		if value, ok := dns[key]; ok {
			rewritten, err := egressDNS(value, name, proxyNames, "dns."+key, sources)
			if err != nil {
				return err
			}
			dns[key] = rewritten
		}
	}
	return nil
}

func egressDNS(value any, name string, proxies map[string]bool, path string, sources map[string]string) (any, error) {
	switch value := value.(type) {
	case Document:
		return egressDNS(map[string]any(value), name, proxies, path, sources)
	case string:
		base, fragment, ok := strings.Cut(value, "#")
		if !ok {
			return value, nil
		}
		decoded, err := url.PathUnescape(fragment)
		if err != nil {
			return value, nil
		} // The core's validator reports malformed URLs.
		fragment = decoded
		parts := strings.Split(fragment, "&")
		last := -1
		for i, part := range parts {
			if !strings.Contains(part, "=") {
				last = i
			}
		}
		if last < 0 || parts[last] == "" || proxies[parts[last]] {
			return value, nil
		}
		if proxies[name] || strings.ContainsAny(name, "&=") {
			return nil, fmt.Errorf("DNS interface binding cannot represent adapter %q without changing proxy selection", name)
		}
		// Only the last bare token is effective in mihomo's parser.
		parts[last] = name
		u := url.URL{Fragment: strings.Join(parts, "&")}
		sources[path] = "Mihari"
		return base + "#" + u.EscapedFragment(), nil
	case []any:
		for i, item := range value {
			rewritten, err := egressDNS(item, name, proxies, path+"["+strconv.Itoa(i)+"]", sources)
			if err != nil {
				return nil, err
			}
			value[i] = rewritten
		}
		return value, nil
	case map[string]any:
		for key, item := range value {
			rewritten, err := egressDNS(item, name, proxies, overridePath(path, key), sources)
			if err != nil {
				return nil, err
			}
			value[key] = rewritten
		}
		return value, nil
	default:
		return value, nil
	}
}

func egressMapping(value any) map[string]any {
	switch value := value.(type) {
	case Document:
		return map[string]any(value)
	case map[string]any:
		return value
	default:
		return nil
	}
}
