package subscription

import (
	"encoding/base64"
	"encoding/hex"
	"math"
	"net/url"
	"strings"
	"time"
)

func websocketSchema() *policySchema {
	path := stringSchema()
	path.check = func(v policyValue) bool { _, err := url.Parse(v.text); return err == nil }
	header := stringSchema()
	header.check = func(v policyValue) bool { return v.text == "" || validHTTPToken(v.text) }
	return objectSchema(map[string]*policySchema{
		"path": path, "headers": policyHeadersSchema(false), "max-early-data": integerSchema(math.MinInt64, math.MaxInt64),
		"early-data-header-name": header, "v2ray-http-upgrade": boolSchema(), "v2ray-http-upgrade-fast-open": boolSchema(),
	})
}

func grpcSchema() *policySchema {
	agent := stringSchema()
	agent.check = func(v policyValue) bool { return validHTTPValue(v.text) }
	return objectSchema(map[string]*policySchema{
		"grpc-service-name": stringSchema(), "grpc-user-agent": agent,
		"ping-interval":   integerSchema(math.MinInt64/int64(time.Second), math.MaxInt64/int64(time.Second)),
		"max-connections": integerSchema(math.MinInt64, math.MaxInt64), "min-streams": integerSchema(math.MinInt64, math.MaxInt64), "max-streams": integerSchema(math.MinInt64, math.MaxInt64),
	})
}

func realitySchema() *policySchema {
	schema := objectSchema(map[string]*policySchema{"public-key": stringSchema(), "short-id": stringSchema(), "support-x25519mlkem768": boolSchema()})
	schema.required = []string{"public-key"}
	schema.validate = func(v policyValue, field string) error {
		key, _ := v.get("public-key")
		if key.text == "" {
			return nil
		}
		decoded, err := base64.RawURLEncoding.DecodeString(key.text)
		if err != nil || len(decoded) != 32 {
			return policyFailure(field + ".public-key")
		}
		id, _ := v.get("short-id")
		if len(id.text) > 16 {
			return policyFailure(field + ".short-id")
		}
		if _, err := hex.DecodeString(id.text); err != nil {
			return policyFailure(field + ".short-id")
		}
		return nil
	}
	return schema
}

func validateRealityTLSModes(v policyValue, field string) error {
	if err := validateTLSModes(v, field); err != nil {
		return err
	}
	reality, _ := v.get("reality-opts")
	key, _ := reality.get("public-key")
	if key.text != "" {
		for _, name := range []string{"shadow-tls-opts", "restls-opts", "jls-opts"} {
			mode, _ := v.get(name)
			for _, member := range mode.fields {
				if member.value.text != "" || member.value.integer != 0 {
					return policyFailure(field + ".security-modes")
				}
			}
		}
	}
	return nil
}

// This is the legacy Mihomo PickCipher registry used by Trojan's SS wrapper,
// distinct from the sing-shadowsocks2 registry of an SS outbound.
func validLegacySSCipher(value string) bool {
	switch strings.ToUpper(value) {
	case "DUMMY", "RC4-MD5", "AES-128-CTR", "AES-192-CTR", "AES-256-CTR", "AES-128-CFB", "AES-192-CFB", "AES-256-CFB", "CHACHA20", "CHACHA20-IETF", "XCHACHA20",
		"AES-128-GCM", "AES-192-GCM", "AES-256-GCM", "CHACHA20-IETF-POLY1305", "XCHACHA20-IETF-POLY1305", "CHACHA8-IETF-POLY1305", "XCHACHA8-IETF-POLY1305", "AES-128-CCM", "AES-192-CCM", "AES-256-CCM",
		"AEAD_AES_128_GCM", "AEAD_AES_192_GCM", "AEAD_AES_256_GCM", "AEAD_CHACHA20_POLY1305", "AEAD_XCHACHA20_POLY1305", "AEAD_CHACHA8_POLY1305", "AEAD_XCHACHA8_POLY1305", "AEAD_AES_128_CCM", "AEAD_AES_192_CCM", "AEAD_AES_256_CCM":
		return true
	default:
		return false
	}
}

func trojanProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"password", "sni", "network", "client-fingerprint"} {
		fields[name] = stringSchema()
	}
	fields["udp"], fields["alpn"] = boolSchema(), alpnSchema()
	addClientTLSFields(fields)
	addTLSModeFields(fields)
	fields["reality-opts"], fields["ws-opts"], fields["grpc-opts"] = realitySchema(), websocketSchema(), grpcSchema()
	ss := objectSchema(map[string]*policySchema{"enabled": boolSchema(), "method": stringSchema(), "password": stringSchema()})
	ss.validate = func(v policyValue, field string) error {
		enabled, _ := v.get("enabled")
		if !enabled.boolean {
			return nil
		}
		password, _ := v.get("password")
		if password.text == "" {
			return policyFailure(field + ".password")
		}
		method, _ := v.get("method")
		if method.text != "" && !validLegacySSCipher(method.text) {
			return policyFailure(field + ".method")
		}
		return nil
	}
	fields["ss-opts"] = ss
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "password")
	schema.validate = validateRealityTLSModes
	return schema
}
