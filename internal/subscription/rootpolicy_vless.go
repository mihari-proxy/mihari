package subscription

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"
	"time"
)

func vlessProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"uuid", "network", "servername", "packet-encoding", "client-fingerprint", "flow"} {
		fields[name] = stringSchema()
	}
	for _, name := range []string{"udp", "tls", "packet-addr", "xudp"} {
		fields[name] = boolSchema()
	}
	fields["alpn"] = alpnSchema()
	fields["encryption"] = stringSchema()
	fields["encryption"].check = func(v policyValue) bool { return validVLESSEncryption(v.text) }
	addClientTLSFields(fields)
	addTLSModeFields(fields)
	fields["reality-opts"] = realitySchema()
	addVmessTransportFields(fields)
	// These transports belong only to VMess; the VLESS declaration has no
	// equivalent fields. Their absence must remain an unknown-field error.
	delete(fields, "mkcp-opts")
	delete(fields, "mekya-opts")
	delete(fields, "tlsmirror-opts")
	fields["ws-headers"] = policyHeadersSchema(false)
	fields["xhttp-opts"] = xhttpSchema()
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "uuid")
	schema.validate = func(v policyValue, field string) error {
		if err := validateVLESSSecurity(v, field); err != nil {
			return err
		}
		flow, _ := v.get("flow")
		if len(flow.text) >= 16 && flow.text[:16] != "xtls-rprx-vision" {
			return policyFailure(field + ".flow")
		}
		return validateXHTTP(v, field)
	}
	return schema
}

func validateVLESSSecurity(v policyValue, field string) error {
	if err := validateRealityTLSModes(v, field); err != nil {
		return err
	}
	tls, _ := v.get("tls")
	if !tls.boolean {
		for _, name := range []string{"shadow-tls-opts", "restls-opts", "jls-opts", "reality-opts"} {
			mode, _ := v.get(name)
			if name == "reality-opts" {
				if xhttpText(mode, "public-key") != "" {
					return policyFailure(field + ".tls")
				}
				continue
			}
			for _, member := range mode.fields {
				if member.value.text != "" || member.value.integer != 0 {
					return policyFailure(field + ".tls")
				}
			}
		}
	}
	return nil
}

// This is the outbound factory's network encryption DSL. It never calls the
// core's cryptographic factory or treats a token as a filename or executable.
func validVLESSEncryption(value string) bool {
	if value == "" || value == "none" {
		return true
	}
	parts := strings.Split(value, ".")
	if len(parts) < 4 || parts[0] != "mlkem768x25519plus" {
		return false
	}
	if parts[1] != "native" && parts[1] != "xorpub" && parts[1] != "random" {
		return false
	}
	if parts[2] != "1rtt" && parts[2] != "0rtt" {
		return false
	}
	var padding []string
	keys, relayBytes := 0, int64(0)
	for _, token := range parts[3:] {
		if len(token) < 20 {
			padding = append(padding, token)
			continue
		}
		key, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return false
		}
		contribution := int64(64)
		switch len(key) {
		case 32: // X25519 constructor checks length; low-order failure is later.
		case 1184:
			contribution = 1120
			for i := 0; i < 1152; i += 3 {
				word := uint32(key[i]) | uint32(key[i+1])<<8 | uint32(key[i+2])<<16
				if word&4095 >= 3329 || word>>12 >= 3329 {
					return false
				}
			}
		default:
			return false
		}
		if relayBytes > math.MaxInt64-contribution {
			return false
		}
		relayBytes += contribution
		keys++
	}
	if keys == 0 {
		return false
	}
	joined := strings.Join(padding, ".")
	maximum := int64(1111 + 3333)
	if joined != "" {
		maximum = 0
		for index, token := range strings.Split(joined, ".") {
			components := strings.Split(token, "-")
			if len(components) < 3 {
				return false
			}
			var triple [3]int64
			for i := range triple {
				parsed, err := strconv.ParseInt(components[i], 10, 64)
				if err != nil || parsed < 0 {
					return false
				}
				triple[i] = parsed
			}
			if index == 0 && (triple[0] < 100 || triple[1] < 35 || triple[2] < 35) {
				return false
			}
			endpoint := max(triple[1], triple[2])
			if index%2 == 0 {
				if endpoint > 65553-maximum {
					return false
				}
				maximum += endpoint
			} else if endpoint > math.MaxInt64/int64(time.Millisecond) {
				return false
			}
		}
	}
	// The hello is dynamically allocated: only padding has a uint16 limit.
	// 16 + (relayBytes-32) + 1250 + maximum, all supported targets int64.
	return relayBytes <= math.MaxInt64-1234-maximum
}
