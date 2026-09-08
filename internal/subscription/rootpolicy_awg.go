package subscription

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"
)

func amneziaSchema() *policySchema {
	fields := make(map[string]*policySchema)
	fields["version"] = integerSchema(math.MinInt64, math.MaxInt64)
	for _, name := range []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4"} {
		fields[name] = integerSchema(0, math.MaxInt64)
	}
	fields["itime"] = integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	for _, name := range []string{"h1", "h2", "h3", "h4"} {
		fields[name] = &policySchema{kind: policyString, stringUint32: true}
	}
	for _, name := range []string{"i1", "i2", "i3", "i4", "i5", "j1", "j2", "j3", "header-protection-key", "content-padding-addition", "rekey-after-time", "rekey-timeout", "reject-after-time", "keepalive-timeout", "max-handshake-attempts"} {
		fields[name] = stringSchema()
	}
	fields["random-trailers"], fields["disable-cookies"] = boolSchema(), boolSchema()
	schema := objectSchema(fields)
	schema.nullable = true
	schema.transform = normalizeAmnezia
	return schema
}

func normalizeAmnezia(ctx context.Context, v policyValue, path string) (policyValue, error) {
	fail := func(name string) (policyValue, error) { return policyValue{}, policyFailure(path + "." + name) }
	version, _ := v.get("version")
	v3 := version.integer == 3
	for _, member := range v.fields {
		if member.value.kind == policyString && member.name != "header-protection-key" && strings.ContainsAny(member.value.text, "\r\n") {
			return fail(member.name)
		}
	}
	jc, _ := v.get("jc")
	jmin, _ := v.get("jmin")
	jmax, _ := v.get("jmax")
	if v3 {
		for _, name := range []string{"jc", "jmin", "jmax"} {
			n, _ := v.get(name)
			if n.integer > math.MaxUint32 {
				return fail(name)
			}
		}
		if jc.integer > 0 && jmin.integer > jmax.integer {
			return fail("jmax")
		}
	} else {
		upper := jmax.integer
		if jc.integer > 0 && upper == jmin.integer {
			if upper >= 65534 {
				return fail("jmax")
			}
			upper++
		}
		if upper >= 65535 || upper < jmin.integer {
			return fail("jmax")
		}
	}
	lengths := [4]int64{148, 92, 64, 32}
	for i, name := range []string{"s1", "s2", "s3", "s4"} {
		n, _ := v.get(name)
		limit := int64(65534) - lengths[i]
		if v3 {
			limit = 65535
			if i == 3 {
				limit = 65519
			}
		}
		if n.integer > limit {
			return fail(name)
		}
		lengths[i] += n.integer
	}
	if !v3 {
		for i := range lengths {
			for j := 0; j < i; j++ {
				if lengths[i] == lengths[j] {
					return fail("s" + strconv.Itoa(i+1))
				}
			}
		}
	}
	var headers [4]xhttpRange
	for i, name := range []string{"h1", "h2", "h3", "h4"} {
		headers[i] = xhttpRange{int64(i + 1), int64(i + 1)}
		text := xhttpText(v, name)
		if text == "" {
			continue
		}
		r, ok := parseAWGRange(text, v3)
		if !ok {
			return fail(name)
		}
		if v3 || r.min > 4 {
			headers[i] = r
		}
	}
	for i := range headers {
		for j := 0; j < i; j++ {
			if headers[i].min <= headers[j].max && headers[j].min <= headers[i].max {
				return fail("h" + strconv.Itoa(i+1))
			}
		}
	}
	maxGenerators := int64(0)
	for _, family := range []struct {
		prefix string
		count  int
	}{{"i", 5}, {"j", 3}} {
		count := int64(0)
		gap := false
		for i := 1; i <= family.count; i++ {
			if err := ctx.Err(); err != nil {
				return policyValue{}, err
			}
			name := family.prefix + strconv.Itoa(i)
			value, exists := v.get(name)
			if !exists || value.text == "" {
				gap = true
				continue
			}
			if v3 && family.prefix == "j" {
				return fail(name)
			}
			if !v3 && gap {
				return fail(name)
			}
			canonical, ok := canonicalAWGRecipe(value.text, v3)
			if !ok {
				return fail(name)
			}
			value.text = canonical
			v.set(name, value)
			count++
		}
		maxGenerators = max(maxGenerators, count)
	}
	if !v3 && jc.integer > math.MaxInt64/24-maxGenerators {
		return fail("jc")
	}
	itime, _ := v.get("itime")
	if v3 && itime.integer != 0 {
		return fail("itime")
	}
	for _, name := range []string{"content-padding-addition", "rekey-after-time", "rekey-timeout", "reject-after-time", "keepalive-timeout", "max-handshake-attempts"} {
		text := xhttpText(v, name)
		if text == "" {
			continue
		}
		if !v3 {
			return fail(name)
		}
		r, ok := parseAWGRange(text, true)
		if !ok || (name == "reject-after-time" && r.max > math.MaxInt64/(3*int64(time.Second))) {
			return fail(name)
		}
	}
	for _, name := range []string{"random-trailers", "disable-cookies"} {
		value, _ := v.get(name)
		if !v3 && value.boolean {
			return fail(name)
		}
	}
	keyText := xhttpText(v, "header-protection-key")
	if keyText != "" {
		if !v3 {
			return fail("header-protection-key")
		}
		key, err := base64.StdEncoding.DecodeString(keyText)
		if err != nil || len(key) != 32 {
			return fail("header-protection-key")
		}
		var nonzero byte
		for _, b := range key {
			nonzero |= b
		}
		if nonzero != 0 {
			for _, name := range []string{"s1", "s2", "s3", "s4"} {
				size, _ := v.get(name)
				if size.integer < 12 {
					return fail(name)
				}
			}
		}
	}
	return v, nil
}

func parseAWGRange(text string, inclusive bool) (xhttpRange, bool) {
	parts := strings.Split(text, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return xhttpRange{}, false
	}
	low, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return xhttpRange{}, false
	}
	high := low
	if len(parts) == 2 {
		high, err = strconv.ParseUint(parts[1], 10, 32)
		if err != nil || high < low {
			return xhttpRange{}, false
		}
	}
	if inclusive && high-low == math.MaxUint32 {
		return xhttpRange{}, false
	}
	return xhttpRange{int64(low), int64(high)}, true
}

// Canonicalize a complete sequence of known byte-generator tags. Native
// parsers ignore some outside text; policy explicitly rejects that ambiguous
// spelling instead of copying their unchecked regex indexing or junk suffixes.
func canonicalAWGRecipe(text string, v3 bool) (string, bool) {
	if strings.ContainsAny(text, "\r\n") {
		return "", false
	}
	var output strings.Builder
	seen := map[string]bool{}
	total := int64(0)
	for remaining := strings.TrimSpace(text); remaining != ""; {
		if remaining[0] != '<' {
			return "", false
		}
		end := strings.IndexByte(remaining, '>')
		if end < 0 {
			return "", false
		}
		body := strings.TrimSpace(remaining[1:end])
		if strings.ContainsAny(body, "<>") {
			return "", false
		}
		parts := strings.Fields(body)
		if len(parts) == 0 {
			return "", false
		}
		tag, param := parts[0], ""
		if len(parts) > 1 {
			if v3 {
				param = parts[1]
			} else {
				param = strings.TrimSpace(strings.TrimPrefix(body, tag))
			}
		}
		length := int64(0)
		switch tag {
		case "b":
			hexText := param
			if v3 {
				hexText = strings.TrimPrefix(hexText, "0x")
				if hexText == "" || len(hexText)%2 != 0 {
					return "", false
				}
			} else {
				if !strings.HasPrefix(hexText, "0x") && !strings.HasPrefix(hexText, "0X") {
					return "", false
				}
				hexText = hexText[2:]
				if len(hexText)%2 != 0 {
					hexText = "0" + hexText
				}
			}
			bytes, err := hex.DecodeString(hexText)
			if err != nil {
				return "", false
			}
			length = int64(len(bytes))
			param = "0x" + hex.EncodeToString(bytes)
		case "r", "rc", "rd", "dz":
			if tag == "dz" && !v3 {
				return "", false
			}
			n, err := strconv.ParseInt(param, 10, 64)
			if err != nil || n < 0 || (!v3 && n > 1000) {
				return "", false
			}
			length = n
			param = strconv.FormatInt(n, 10)
		case "t", "c":
			if v3 {
				if tag == "c" {
					return "", false
				}
				length = 4
				param = ""
			} else {
				if param != "" || seen[tag] {
					return "", false
				}
				seen[tag] = true
				length = 8
			}
		case "wt":
			if v3 {
				return "", false
			}
			n, err := strconv.ParseInt(param, 10, 64)
			if err != nil || n < math.MinInt64/int64(time.Millisecond) || n > 5000 {
				return "", false
			}
			param = strconv.FormatInt(n, 10)
		case "d", "ds":
			if !v3 {
				return "", false
			}
			param = ""
		default:
			return "", false
		}
		if length > math.MaxInt64-total {
			return "", false
		}
		total += length
		output.WriteByte('<')
		output.WriteString(tag)
		if param != "" {
			output.WriteByte(' ')
			output.WriteString(param)
		}
		output.WriteByte('>')
		remaining = strings.TrimSpace(remaining[end+1:])
	}
	return output.String(), output.Len() > 0
}
