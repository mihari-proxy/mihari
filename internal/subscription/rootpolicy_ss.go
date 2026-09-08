package subscription

import (
	"encoding/base64"
	"math"
	"net/url"
	"strings"
	"time"
)

var ss2CipherNames = []string{"none", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "rc4-md5", "chacha20-ietf", "xchacha20", "chacha20",
	"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305", "chacha8-ietf-poly1305", "xchacha8-ietf-poly1305", "rabbit128-poly1305", "aes-128-ccm", "aes-192-ccm", "aes-256-ccm", "aes-128-gcm-siv", "aes-256-gcm-siv", "aegis-128l", "aegis-256", "aez-384", "deoxys-ii-256-128", "lea-128-gcm", "lea-192-gcm", "lea-256-gcm", "ascon128", "ascon128a",
	"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "2022-blake3-chacha8-poly1305", "2022-blake3-aes-128-ccm", "2022-blake3-aes-256-ccm"}

func ssProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	fields["cipher"] = &policySchema{kind: policyString, choices: ss2CipherNames}
	fields["password"], fields["client-fingerprint"] = stringSchema(), stringSchema()
	fields["udp"], fields["udp-over-tcp"], fields["udp-over-tcp-version"] = boolSchema(), boolSchema(), integerSchema(0, 2)
	fields["plugin"] = &policySchema{kind: policyString, choices: []string{"", "obfs", "v2ray-plugin", "gost-plugin", "shadow-tls", "restls", "jls", "kcptun"}}
	fields["plugin-opts"] = ssPluginSchema()
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "cipher", "password")
	schema.validate = func(v policyValue, field string) error {
		cipher, _ := v.get("cipher")
		password, _ := v.get("password")
		if !validSS2Password(cipher.text, password.text) {
			return policyFailure(field + ".password")
		}
		plugin, _ := v.get("plugin")
		options, _ := v.get("plugin-opts")
		if !validClientKeyPair(options) {
			return policyFailure(field + ".plugin-opts.certificate")
		}
		mode, _ := options.get("mode")
		switch plugin.text {
		case "obfs":
			if mode.text != "tls" && mode.text != "http" {
				return policyFailure(field + ".plugin-opts.mode")
			}
			return snellObfuscationSchema().validate(options, field+".plugin-opts")
		case "shadow-tls", "restls", "jls":
			// Select a trusted built-in mode for validation without mutating the
			// owned output tree or copying any raw input YAML.
			selected := options
			selected.fields = append([]policyMember(nil), options.fields...)
			selected.set("mode", policyValue{kind: policyString, text: plugin.text})
			return snellObfuscationSchema().validate(selected, field+".plugin-opts")
		case "v2ray-plugin", "gost-plugin":
			if mode.text != "websocket" {
				return policyFailure(field + ".plugin-opts.mode")
			}
			host, _ := options.get("host")
			if !validHTTPValue(host.text) {
				return policyFailure(field + ".plugin-opts.host")
			}
			path, _ := options.get("path")
			if _, err := url.Parse(path.text); err != nil {
				return policyFailure(field + ".plugin-opts.path")
			}
		case "kcptun":
			return validateKCPPlugin(options, field+".plugin-opts")
		}
		return nil
	}
	return schema
}

func validSS2Password(cipher, password string) bool {
	if cipher == "none" {
		return true
	}
	if password == "" {
		return false
	}
	if !strings.HasPrefix(cipher, "2022-") {
		return true
	}
	size, maxKeys := 32, 1971
	if strings.Contains(cipher, "aes-128") {
		size, maxKeys = 16, 1972
	}
	if strings.Contains(cipher, "chacha") {
		maxKeys = 1
	}
	count := 0
	for segment := range strings.SplitSeq(password, ":") {
		count++
		if count > maxKeys {
			return false
		}
		key, err := base64.StdEncoding.DecodeString(segment)
		if err != nil || len(key) != size {
			return false
		}
	}
	return true
}

func ssPluginSchema() *policySchema {
	fields := snellObfuscationSchema().fields
	fields["mode"] = stringSchema()
	for _, name := range []string{"path", "key", "crypt"} {
		fields[name] = stringSchema()
	}
	for _, name := range []string{"tls", "mux", "v2ray-http-upgrade", "v2ray-http-upgrade-fast-open", "nocomp", "acknodelay"} {
		fields[name] = boolSchema()
	}
	for _, name := range []string{"conn", "autoexpire", "scavengettl", "mtu", "ratelimit", "sndwnd", "rcvwnd", "datashard", "parityshard", "dscp", "nodelay", "interval", "resend", "nc", "sockbuf", "smuxver", "smuxbuf", "framesize", "streambuf", "keepalive"} {
		fields[name] = integerSchema(math.MinInt64, math.MaxInt64)
	}
	fields["headers"], fields["ech-opts"] = policyHeadersSchema(false), echSchema()
	return objectSchema(fields)
}

func validateKCPPlugin(v policyValue, field string) error {
	integer := func(name string, zeroDefault int64) int64 {
		value, _ := v.get(name)
		if value.integer == 0 {
			return zeroDefault
		}
		return value.integer
	}
	fail := func(name string) error { return policyFailure(field + "." + name) }
	conn := integer("conn", 1)
	if conn < 1 || conn > 65535 {
		return fail("conn")
	}
	frame := integer("framesize", 8192)
	if frame < 1 || frame > 65535 {
		return fail("framesize")
	}
	receive := integer("smuxbuf", 4194304)
	if receive < 1 || receive > math.MaxInt32 {
		return fail("smuxbuf")
	}
	stream := integer("streambuf", 2097152)
	if stream < 1 || stream > receive {
		return fail("streambuf")
	}
	if integer("smuxver", 1) < 1 {
		return fail("smuxver")
	} // >2 is natively clamped.
	keep := integer("keepalive", 10)
	if keep < 1 || keep > math.MaxInt64/(3*int64(time.Second)) {
		return fail("keepalive")
	}
	for _, name := range []string{"autoexpire", "scavengettl"} {
		n := integer(name, 0)
		if n < math.MinInt64/int64(time.Second) || n > math.MaxInt64/int64(time.Second) {
			return fail(name)
		}
	}
	rate := integer("ratelimit", 0)
	if rate < 0 || rate > math.MaxUint32 {
		return fail("ratelimit")
	}
	for _, name := range []string{"sndwnd", "rcvwnd"} {
		if integer(name, 0) > math.MaxUint32 {
			return fail(name)
		}
	}
	d, p := integer("datashard", 10), integer("parityshard", 3)
	if d > 0 && p > 0 && (d > 255 || p > 256-d) {
		return fail("datashard")
	}
	mode, _ := v.get("mode")
	switch mode.text {
	case "", "normal", "fast", "fast2", "fast3": // Fixed modes replace all four knobs.
	case "manual":
		if integer("nodelay", 0) > math.MaxUint32 {
			return fail("nodelay")
		}
		for _, name := range []string{"resend", "nc"} {
			if integer(name, 0) > math.MaxInt32 {
				return fail(name)
			}
		}
	default:
		return fail("mode")
	}
	return nil
}
