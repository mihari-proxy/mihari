package subscription

import (
	"math"
	"strconv"
	"strings"
	"time"
)

func xhttpSchema() *policySchema {
	fields := make(map[string]*policySchema)
	for _, name := range []string{"path", "host", "mode", "x-padding-bytes", "x-padding-key", "x-padding-header", "x-padding-placement", "x-padding-method", "uplink-http-method", "session-placement", "session-key", "session-table", "session-length", "seq-placement", "seq-key", "uplink-data-placement", "uplink-data-key", "uplink-chunk-size", "sc-max-each-post-bytes", "sc-min-posts-interval-ms"} {
		fields[name] = stringSchema()
	}
	fields["headers"] = policyHeadersSchema(false)
	fields["no-grpc-header"], fields["x-padding-obfs-mode"] = boolSchema(), boolSchema()
	fields["reuse-settings"] = xhttpReuseSchema()
	download := map[string]*policySchema{
		"path": stringSchema(), "host": stringSchema(), "headers": policyHeadersSchema(false),
		"server": policyNameSchema(), "port": integerSchema(1, 65535), "tls": boolSchema(), "alpn": alpnSchema(),
		"servername": stringSchema(), "client-fingerprint": stringSchema(), "reuse-settings": xhttpReuseSchema(),
	}
	addClientTLSFields(download)
	addTLSModeFields(download)
	download["reality-opts"] = realitySchema()
	for _, schema := range download {
		schema.nullable = true
	}
	fields["download-settings"] = objectSchema(download)
	fields["download-settings"].nullable = true
	return objectSchema(fields)
}

func xhttpReuseSchema() *policySchema {
	fields := make(map[string]*policySchema)
	for _, name := range []string{"max-concurrency", "max-connections", "c-max-reuse-times", "h-max-request-times", "h-max-reusable-secs"} {
		fields[name] = stringSchema()
	}
	fields["h-keep-alive-period"] = integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	schema := objectSchema(fields)
	schema.nullable = true
	return schema
}

func xhttpText(v policyValue, name string) string { value, _ := v.get(name); return value.text }

type xhttpRange struct{ min, max int64 }

func parseXHTTPRange(value, fallback string) (xhttpRange, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return xhttpRange{}, false
	}
	low, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || low < 0 {
		return xhttpRange{}, false
	}
	high := low
	if len(parts) == 2 {
		high, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || high < low {
			return xhttpRange{}, false
		}
	}
	if high != low && high-low == math.MaxInt64 {
		return xhttpRange{}, false
	}
	return xhttpRange{low, high}, true
}

func validateXHTTP(parent policyValue, field string) error {
	cfg, exists := parent.get("xhttp-opts")
	network := xhttpText(parent, "network")
	if !exists && network != "xhttp" {
		return nil
	}
	download, _ := cfg.get("download-settings")
	hasDownload := download.kind == policyObject
	if hasDownload {
		// Pointer null/absence inherits; any non-null value replaces that entire
		// member. A new owned field slice prevents mutating the upload policy.
		effective := policyValue{kind: policyObject, fields: append([]policyMember(nil), parent.fields...)}
		for _, member := range download.fields {
			if member.value.kind != policyNull {
				effective.set(member.name, member.value)
			}
		}
		if err := validateVLESSSecurity(effective, field+".xhttp-opts.download-settings"); err != nil {
			return err
		}
		if network == "xhttp" {
			if err := validateXHTTPH3(effective, field+".xhttp-opts.download-settings"); err != nil {
				return err
			}
		}
	}
	if network != "xhttp" {
		return nil
	}
	path := field + ".xhttp-opts"
	fail := func(name string) error { return policyFailure(path + "." + name) }
	if err := validateXHTTPH3(parent, field); err != nil {
		return err
	}
	mode := xhttpText(cfg, "mode")
	switch mode {
	case "", "auto", "stream-one", "stream-up", "packet-up":
	default:
		return fail("mode")
	}
	if mode == "stream-one" && hasDownload {
		return fail("download-settings")
	}
	if mode == "" || mode == "auto" {
		mode = "packet-up"
		reality, _ := parent.get("reality-opts")
		if xhttpText(reality, "public-key") != "" {
			mode = "stream-one"
			if hasDownload {
				mode = "stream-up"
			}
		}
	}
	if !validHTTPValue(xhttpText(cfg, "host")) {
		return fail("host")
	}
	if hasDownload && !validHTTPValue(xhttpText(download, "host")) {
		return fail("download-settings.host")
	}
	method := xhttpText(cfg, "uplink-http-method")
	if method != "" && !validHTTPToken(method) {
		return fail("uplink-http-method")
	}
	post, ok := parseXHTTPRange(xhttpText(cfg, "sc-max-each-post-bytes"), "1000000")
	if !ok || post.max == 0 || (mode == "packet-up" && post.min == 0) {
		return fail("sc-max-each-post-bytes")
	}
	interval, ok := parseXHTTPRange(xhttpText(cfg, "sc-min-posts-interval-ms"), "30")
	if !ok || interval.max == 0 || interval.max > math.MaxInt64/int64(time.Millisecond) {
		return fail("sc-min-posts-interval-ms")
	}
	if err := validateXHTTPSession(cfg, mode, path); err != nil {
		return err
	}
	if err := validateXHTTPPadding(cfg, path); err != nil {
		return err
	}
	if mode == "packet-up" {
		placement := xhttpText(cfg, "uplink-data-placement")
		if placement == "header" || placement == "cookie" {
			key := xhttpText(cfg, "uplink-data-key")
			if !validHTTPToken(key + "-0") {
				return fail("uplink-data-key")
			}
			chunk, ok := parseXHTTPRange(xhttpText(cfg, "uplink-chunk-size"), "0")
			if !ok {
				return fail("uplink-chunk-size")
			}
			// Native zero uses placement defaults, otherwise minimum64 is
			// applied before slicing actual bytes. No make(chunk.max) occurs.
			_ = chunk
		}
	}
	reuse, _ := cfg.get("reuse-settings")
	if reuse.kind == policyObject {
		if err := validateXHTTPReuse(reuse, path+".reuse-settings"); err != nil {
			return err
		}
		if hasDownload {
			child, _ := download.get("reuse-settings")
			if child.kind == policyObject {
				if err := validateXHTTPReuse(child, path+".download-settings.reuse-settings"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateXHTTPH3(v policyValue, path string) error {
	alpn, _ := v.get("alpn")
	if len(alpn.items) != 1 || alpn.items[0].text != "h3" {
		return nil
	}
	tls, _ := v.get("tls")
	if !tls.boolean {
		return policyFailure(path + ".tls")
	}
	for _, name := range []string{"shadow-tls-opts", "restls-opts", "jls-opts", "reality-opts"} {
		value, _ := v.get(name)
		if name == "reality-opts" {
			if xhttpText(value, "public-key") != "" {
				return policyFailure(path + ".security-modes")
			}
			continue
		}
		for _, member := range value.fields {
			if member.value.text != "" || member.value.integer != 0 {
				return policyFailure(path + ".security-modes")
			}
		}
	}
	return nil
}

func validateXHTTPReuse(v policyValue, path string) error {
	for _, name := range []string{"max-concurrency", "max-connections", "c-max-reuse-times", "h-max-request-times", "h-max-reusable-secs"} {
		value, ok := parseXHTTPRange(xhttpText(v, name), "0")
		if !ok {
			return policyFailure(path + "." + name)
		}
		if (name == "c-max-reuse-times" || name == "h-max-request-times") && value.max > math.MaxInt32 {
			return policyFailure(path + "." + name)
		}
		if name == "h-max-reusable-secs" && value.max > math.MaxInt64/int64(time.Second) {
			return policyFailure(path + "." + name)
		}
	}
	return nil
}

func validateXHTTPSession(v policyValue, mode, path string) error {
	table := xhttpText(v, "session-table")
	if table != "" && table != "uuid" {
		presets := map[string]string{"ALPHABET": "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "Alphabet": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", "BASE36": "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", "Base62": "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", "HEX": "0123456789ABCDEF", "alphabet": "abcdefghijklmnopqrstuvwxyz", "base36": "0123456789abcdefghijklmnopqrstuvwxyz", "hex": "0123456789abcdef", "number": "0123456789"}
		if preset, exists := presets[table]; exists {
			table = preset
		}
		length, ok := parseXHTTPRange(xhttpText(v, "session-length"), "16-32")
		if !ok || length.min == 0 || length.max == math.MaxInt64 || !xhttpSessionRoom(len(table), length) {
			return policyFailure(path + ".session-length")
		}
		for i := range len(table) {
			if table[i] >= 128 {
				return policyFailure(path + ".session-table")
			}
		}
	}
	if mode == "stream-one" {
		return nil
	}
	placement := xhttpText(v, "session-placement")
	key := xhttpText(v, "session-key")
	if (placement == "header" || placement == "cookie") && key != "" && !validHTTPToken(key) {
		return policyFailure(path + ".session-key")
	}
	if table != "" && table != "uuid" {
		if placement == "header" && !validHTTPValue(table) {
			return policyFailure(path + ".session-table")
		}
		if placement == "cookie" && !validXHTTPCookieValue(table) {
			return policyFailure(path + ".session-table")
		}
	}
	if mode == "packet-up" {
		placement, key = xhttpText(v, "seq-placement"), xhttpText(v, "seq-key")
		if (placement == "header" || placement == "cookie") && key != "" && !validHTTPToken(key) {
			return policyFailure(path + ".seq-key")
		}
	}
	return nil
}

// Saturate at the native threshold. For base>=2 at most31 exponent/term
// steps are needed, independently of the supplied range. Duplicate table
// bytes still contribute to base, matching the pinned nominal entropy rule.
func xhttpSessionRoom(base int, bounds xhttpRange) bool {
	const threshold int64 = 1 << 31
	if base == 0 {
		return false
	}
	if base == 1 {
		return bounds.max-bounds.min+1 >= threshold
	}
	term := int64(1)
	for exponent := int64(0); exponent < bounds.min; exponent++ {
		if int64(base) >= (threshold+term-1)/term {
			return true
		}
		term *= int64(base)
	}
	total := int64(0)
	for exponent := bounds.min; exponent <= bounds.max; exponent++ {
		if term >= threshold-total {
			return true
		}
		total += term
		if int64(base) >= (threshold+term-1)/term {
			return exponent < bounds.max
		}
		term *= int64(base)
	}
	return false
}

func validXHTTPCookieValue(value string) bool {
	for i := range len(value) {
		b := value[i]
		if b < 0x20 || b >= 0x7f || b == '"' || b == ';' || b == '\\' {
			return false
		}
	}
	return true
}

func validateXHTTPPadding(v policyValue, path string) error {
	padding, ok := parseXHTTPRange(xhttpText(v, "x-padding-bytes"), "100-1000")
	if !ok {
		return policyFailure(path + ".x-padding-bytes")
	}
	obfs, _ := v.get("x-padding-obfs-mode")
	if !obfs.boolean {
		return nil
	}
	if xhttpText(v, "x-padding-method") == "tokenish" && padding.max != 0 {
		n := math.Ceil(float64(padding.max) / 0.8)
		if n >= math.Ldexp(1, 63) || int64(n) > math.MaxInt64-150 {
			return policyFailure(path + ".x-padding-bytes")
		}
	}
	placement, header, key := xhttpText(v, "x-padding-placement"), xhttpText(v, "x-padding-header"), xhttpText(v, "x-padding-key")
	if (placement == "header" || placement == "queryInHeader") && !validHTTPToken(header) {
		return policyFailure(path + ".x-padding-header")
	}
	if placement == "cookie" && key != "" && !validHTTPToken(key) {
		return policyFailure(path + ".x-padding-key")
	}
	if placement == "queryInHeader" && !validHTTPValue(key) {
		return policyFailure(path + ".x-padding-key")
	}
	return nil
}
