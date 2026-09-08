package subscription

import (
	"encoding/base64"
	"math"
	"strings"
	"time"
)

func anyTLSProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	fields["password"], fields["sni"], fields["client-fingerprint"] = stringSchema(), stringSchema(), stringSchema()
	fields["udp"], fields["disable-reuse"] = boolSchema(), boolSchema()
	fields["alpn"] = alpnSchema()
	fields["idle-session-check-interval"], fields["idle-session-timeout"] = integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second)), integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second))
	fields["min-idle-session"] = integerSchema(math.MinInt64, math.MaxInt64)
	metadata := stringSchema()
	metadata.check = func(v policyValue) bool { return len(v.text) <= 65535-56 && !strings.Contains(v.text, "\n") }
	fields["client-metadata"] = metadata
	addClientTLSFields(fields)
	addTLSModeFields(fields)
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "password")
	schema.validate = validateTLSModes
	return schema
}

func alpnSchema() *policySchema {
	name := stringSchema()
	name.check = func(v policyValue) bool { return len(v.text) > 0 && len(v.text) <= 255 }
	result := listSchema(name)
	result.check = func(v policyValue) bool {
		total := 0
		for _, item := range v.items {
			if len(item.text)+1 > 65535-total {
				return false
			}
			total += len(item.text) + 1
		}
		return true
	}
	return result
}

func addTLSModeFields(fields map[string]*policySchema) {
	fields["ech-opts"] = echSchema()
	fields["shadow-tls-opts"] = objectSchema(map[string]*policySchema{"password": stringSchema(), "version": integerSchema(0, 3)})
	fields["restls-opts"] = restlsSchema()
	jls := objectSchema(map[string]*policySchema{"username": stringSchema(), "password": stringSchema()})
	jls.required = []string{"username", "password"}
	jls.check = func(v policyValue) bool {
		user, _ := v.get("username")
		password, _ := v.get("password")
		return (user.text == "") == (password.text == "")
	}
	fields["jls-opts"] = jls
}

func echSchema() *policySchema {
	schema := objectSchema(map[string]*policySchema{"enable": boolSchema(), "config": stringSchema(), "query-server-name": stringSchema()})
	schema.validate = func(v policyValue, field string) error {
		enabled, _ := v.get("enable")
		config, _ := v.get("config")
		if enabled.boolean && config.text != "" {
			if _, err := base64.StdEncoding.DecodeString(config.text); err != nil {
				return policyFailure(field + ".config")
			}
		}
		return nil
	}
	return schema
}

func validateTLSModes(v policyValue, field string) error {
	if !validClientKeyPair(v) {
		return policyFailure(field + ".certificate")
	}
	active := 0
	for _, name := range []string{"shadow-tls-opts", "restls-opts", "jls-opts"} {
		option, _ := v.get(name)
		for _, member := range option.fields {
			if member.value.text != "" || member.value.integer != 0 {
				active++
				break
			}
		}
	}
	if active > 1 {
		return policyFailure(field + ".security-modes")
	}
	return nil
}

func restlsSchema() *policySchema {
	schema := objectSchema(map[string]*policySchema{"password": stringSchema(), "version-hint": enumSchema("", "tls12", "tls13"), "restls-script": stringSchema()})
	schema.validate = func(v policyValue, field string) error {
		password, _ := v.get("password")
		version, _ := v.get("version-hint")
		script, _ := v.get("restls-script")
		if password.text == "" && version.text == "" && script.text == "" {
			return nil
		}
		if version.text == "" {
			return policyFailure(field + ".version-hint")
		}
		ceiling := 16372
		if version.text == "tls12" {
			ceiling = 16364
		}
		if !validRestlsScript(script.text, ceiling) {
			return policyFailure(field + ".restls-script")
		}
		return nil
	}
	return schema
}

// The actual Restls DSL permits omitted numeric operands (zero), empty comma
// items, and ASCII spaces. Random upper bounds are exclusive. Validate the
// maximum reachable record length without sampling or changing its algorithm.
func validRestlsScript(script string, ceiling int) bool {
	if script == "" {
		script = "250?100<1,350~100<1,600~100,300~200,300~100"
	}
	integer := func(input *string) (int, bool) {
		result := 0
		i := 0
		for i < len(*input) && (*input)[i] >= '0' && (*input)[i] <= '9' {
			digit := int((*input)[i] - '0')
			if result > (32768-digit)/10 {
				return 0, false
			}
			result = result*10 + digit
			i++
		}
		*input = (*input)[i:]
		return result, true
	}
	for item := range strings.SplitSeq(strings.ReplaceAll(script, " ", ""), ",") {
		if item == "" {
			continue
		}
		target, ok := integer(&item)
		if !ok || target > 32767 {
			return false
		}
		maximum := target
		if len(item) > 0 && (item[0] == '~' || item[0] == '?') {
			item = item[1:]
			spread, ok := integer(&item)
			if !ok || spread > 32767 || target+spread > 32768 {
				return false
			}
			if spread > 0 {
				maximum = target + spread - 1
			}
		}
		if maximum > ceiling {
			return false
		}
		if len(item) > 0 && item[0] == '<' {
			item = item[1:]
			responses, ok := integer(&item)
			if !ok || responses >= 255 {
				return false
			}
		}
		if item != "" {
			return false
		}
	}
	return true
}
