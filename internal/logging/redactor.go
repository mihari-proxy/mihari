package logging

import (
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

const (
	redactedExact = "***"
	redactedURL   = "[REDACTED_URL]"
	minExactLen   = 8
)

var (
	urlPattern          = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s"'<>\\]+`)
	authSchemePattern   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[^\s]+`)
	cookiePattern       = regexp.MustCompile(`(?i)\b(cookie)\s*([:=])\s*[^\r\n]+`)
	quotedSecretPattern = regexp.MustCompile(`(?i)\b(token|secret|password|authorization|credential|api[-_]key)\s*([:=])\s*(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*')`)
	namedSecretPattern  = regexp.MustCompile(`(?i)\b(token|secret|password|authorization|credential|api[-_]key)\s*([:=])\s*[^\s&;,"']+`)
	hex64Pattern        = regexp.MustCompile(`(?i)\b[0-9a-f]{64}\b`)

	sensitiveKeys = map[string]struct{}{
		"secret":        {},
		"token":         {},
		"authorization": {},
		"password":      {},
		"credential":    {},
		"cookie":        {},
		"api-key":       {},
	}
)

type redactionRules struct {
	exact []string
}

// Redactor applies immutable exact-secret and generic redaction rules to log text and attrs.
type Redactor struct {
	rules atomic.Pointer[redactionRules]
}

// NewRedactor builds a redactor and registers optional exact secrets.
func NewRedactor(exact ...string) *Redactor {
	r := &Redactor{}
	r.ReplaceExact(exact)
	return r
}

// ReplaceExact replaces the exact-secret snapshot. Empty and too-short values are dropped;
// remaining values are copied and sorted longest-first. Generic regexes are package-level.
func (r *Redactor) ReplaceExact(values []string) {
	exact := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) < minExactLen {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		exact = append(exact, value)
	}
	sort.Slice(exact, func(i, j int) bool {
		return len(exact[i]) > len(exact[j])
	})
	r.rules.Store(&redactionRules{exact: exact})
}

// String redacts a free-form text value using the current rules snapshot.
func (r *Redactor) String(value string) string {
	rules := r.rules.Load()
	if rules == nil {
		rules = &redactionRules{}
	}
	out := value
	for _, exact := range rules.exact {
		out = strings.ReplaceAll(out, exact, redactedExact)
	}
	out = urlPattern.ReplaceAllString(out, redactedURL)
	out = authSchemePattern.ReplaceAllStringFunc(out, func(match string) string {
		parts := strings.Fields(match)
		if len(parts) == 0 {
			return redactedExact
		}
		return parts[0] + " " + redactedExact
	})
	out = cookiePattern.ReplaceAllString(out, "${1}${2}"+redactedExact)
	out = quotedSecretPattern.ReplaceAllString(out, "${1}${2}"+redactedExact)
	out = namedSecretPattern.ReplaceAllString(out, "${1}${2}"+redactedExact)
	out = hex64Pattern.ReplaceAllString(out, redactedExact)
	return out
}

// ReplaceAttr recursively redacts slog attributes. Sensitive keys and sensitive
// ancestor groups replace values with ***; strings, errors, and LogValuer
// results run through String.
func (r *Redactor) ReplaceAttr(groups []string, attr slog.Attr) slog.Attr {
	if isSensitiveKey(attr.Key) || hasSensitiveGroup(groups) {
		return slog.String(attr.Key, redactedExact)
	}
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindGroup:
		children := attr.Value.Group()
		redacted := make([]slog.Attr, len(children))
		nextGroups := append(append([]string{}, groups...), attr.Key)
		for i, child := range children {
			redacted[i] = r.ReplaceAttr(nextGroups, child)
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}
	}

	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, r.String(attr.Value.String()))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok && err != nil {
			return slog.String(attr.Key, r.String(err.Error()))
		}
	}
	return attr
}

func hasSensitiveGroup(groups []string) bool {
	for _, group := range groups {
		if isSensitiveKey(group) {
			return true
		}
	}
	return false
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.ReplaceAll(normalized, "_", "-")
	_, ok := sensitiveKeys[normalized]
	return ok
}
