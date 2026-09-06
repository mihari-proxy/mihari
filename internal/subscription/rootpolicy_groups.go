package subscription

import (
	"context"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func policyNameSchema() *policySchema {
	s := stringSchema()
	s.check = func(v policyValue) bool { return v.text != "" && !strings.ContainsRune(v.text, 0) }
	return s
}

func policyHTTPURLSchema() *policySchema {
	s := stringSchema()
	s.check = func(v policyValue) bool {
		if v.text == "" {
			return true
		}
		u, err := url.Parse(v.text)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.Opaque == ""
	}
	return s
}

func groupSchema() *policySchema {
	text, boolean := stringSchema(), boolSchema()
	name := stringSchema()
	name.check = func(v policyValue) bool { return v.text != "" }
	status := stringSchema()
	status.check = func(v policyValue) bool { return validUnsignedRanges(v.text, math.MaxUint16, 28) }
	s := objectSchema(map[string]*policySchema{
		"name":    name,
		"type":    {kind: policyString, choices: []string{"select", "url-test", "fallback", "load-balance"}},
		"proxies": listSchema(text), "use": listSchema(policyNameSchema()),
		"url":              policyHTTPURLSchema(),
		"interval":         integerSchema(0, math.MaxInt64/int64(time.Second)),
		"timeout":          integerSchema(0, math.MaxInt64/int64(time.Millisecond)),
		"max-failed-times": integerSchema(0, math.MaxInt64),
		"empty-fallback":   text, "lazy": boolean, "disable-udp": boolean,
		// regexp2 reads only proxy names. Keep its grammar for the pinned core;
		// these fields cannot construct paths, commands, or configuration fields.
		"filter": text, "exclude-filter": text, "exclude-type": text,
		"expected-status": status,
		"include-all":     boolean, "include-all-proxies": boolean, "include-all-providers": boolean,
		"hidden": boolean, "icon": text,
		"default-selected": text, "tolerance": unsignedSchema(math.MaxUint16),
		"strategy": text,
		// These three native keys only log a removed-setting warning.
		"routing-mark":   integerSchema(math.MinInt64, math.MaxInt64),
		"interface-name": text, "dialer-proxy": text,
	})
	s.normalize = func(key string) string {
		if key != "name" && strings.EqualFold(key, "name") {
			return key
		}
		return strings.ToLower(key)
	}
	s.check = func(v policyValue) bool {
		if _, ok := v.get("name"); !ok {
			return false
		}
		typ, ok := v.get("type")
		if !ok {
			return false
		}
		if typ.text == "load-balance" {
			switch xhttpText(v, "strategy") {
			case "", "consistent-hashing", "round-robin", "sticky-sessions":
			default:
				return false
			}
		}
		return true
	}
	s.transform = func(_ context.Context, v policyValue, _ string) (policyValue, error) {
		for _, key := range []string{"routing-mark", "interface-name", "dialer-proxy"} {
			v.remove(key)
		}
		typ := xhttpText(v, "type")
		for key, active := range map[string]string{"default-selected": "select", "tolerance": "url-test", "strategy": "load-balance"} {
			if typ != active {
				v.remove(key)
			}
		}
		return v, nil
	}
	return s
}

// validUnsignedRanges mirrors common/utils/ranges.go's list grammar while
// preventing its uint64-to-narrow-integer truncation. Reversed bounds remain valid.
func validUnsignedRanges(value string, max uint64, maxSegments int) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "*" {
		return true
	}
	parts := strings.Split(strings.ReplaceAll(value, ",", "/"), "/")
	if len(parts) > maxSegments {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		bounds := strings.Split(part, "-")
		if len(bounds) > 2 {
			return false
		}
		for _, bound := range bounds {
			n, err := strconv.ParseUint(strings.Trim(bound, "[ ]"), 10, 64)
			if err != nil || n > max {
				return false
			}
		}
	}
	return true
}
