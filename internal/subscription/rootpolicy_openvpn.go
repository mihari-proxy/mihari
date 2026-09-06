package subscription

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"math"
	"strings"
	"time"
)

func openVPNProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, field := range []string{"proto", "dev", "cipher", "auth", "comp-lzo", "ca", "cert", "key", "tls-auth", "key-direction", "tls-crypt", "tls-crypt-v2", "username", "password", "data-ciphers-fallback"} {
		fields[field] = stringSchema()
	}
	fields["data-ciphers"] = listSchema(stringSchema())
	// This one named dynamic schema carries network peer metadata, including
	// native reserved-key and aggregate wire-string truncation semantics.
	fields["peer-info"] = &policySchema{kind: policyObject, dynamic: stringSchema()}
	for _, field := range []string{"ping", "ping-restart", "handshake-timeout", "tran-window"} {
		fields[field] = integerSchema(0, math.MaxInt64/int64(time.Second))
	}
	fields["tran-window"].nullable = true
	fields["mtu"] = integerSchema(0, math.MaxUint32)
	fields["udp"], fields["remote-dns-resolve"] = boolSchema(), boolSchema()
	fields["ip-stack"], fields["dns"] = ipStackSchema(), listSchema(nameserverSchema())
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "ca")
	schema.validate = func(v policyValue, field string) error {
		fail := func(name string) error { return policyFailure(field + "." + name) }
		proto, _ := v.get("proto")
		switch strings.ToLower(strings.TrimSpace(proto.text)) {
		case "", "udp", "udp4", "tcp", "tcp-client", "tcp4", "tcp4-client":
		default:
			return fail("proto")
		}
		dev, _ := v.get("dev")
		if dev.text != "" && strings.ToLower(strings.TrimSpace(dev.text)) != "tun" {
			return fail("dev")
		}
		cipher, _ := v.get("cipher")
		switch strings.ToUpper(strings.TrimSpace(cipher.text)) {
		case "", "AES-CBC", "AES-128-CBC", "AES-192-CBC", "AES-256-CBC", "AES-128-GCM", "AES-192-GCM", "AES-256-GCM", "CHACHA20-POLY1305":
		default:
			return fail("cipher")
		}
		auth, _ := v.get("auth")
		switch strings.ToUpper(strings.TrimSpace(auth.text)) {
		case "", "MD5", "SHA-1", "SHA1", "SHA256", "SHA384", "SHA512":
		default:
			return fail("auth")
		}
		direction, _ := v.get("key-direction")
		if direction.text != "" && direction.text != "0" && direction.text != "1" {
			return fail("key-direction")
		}
		ca, _ := v.get("ca")
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(ca.text)) {
			return fail("ca")
		}
		cert, _ := v.get("cert")
		key, _ := v.get("key")
		if strings.TrimSpace(cert.text) != "" || strings.TrimSpace(key.text) != "" {
			if _, err := tls.X509KeyPair([]byte(cert.text), []byte(key.text)); err != nil {
				return fail("cert")
			}
		} else {
			username, _ := v.get("username")
			if strings.TrimSpace(username.text) == "" {
				return fail("username")
			}
		}
		keyCount := 0
		for _, name := range []string{"tls-auth", "tls-crypt", "tls-crypt-v2"} {
			value, _ := v.get(name)
			if strings.TrimSpace(value.text) == "" {
				continue
			}
			keyCount++
			if keyCount > 1 {
				return fail(name)
			}
			if name == "tls-crypt-v2" {
				block, _ := pem.Decode([]byte(value.text))
				if block == nil || block.Type != "OpenVPN tls-crypt-v2 client key" || len(block.Bytes) <= 256 {
					return fail(name)
				}
			} else if !validOpenVPNStaticKey(value.text) {
				return fail(name)
			}
		}
		mtu, _ := v.get("mtu")
		effective := mtu.integer
		if effective == 0 {
			effective = 1500
		}
		stack, _ := v.get("ip-stack")
		mode, _ := stack.get("mode")
		// Local addresses arrive in a future authenticated server push. Only
		// the selected stack's address-independent MTU bounds are known here.
		if mode.text == "mips" && (effective < 68 || effective > 65535) {
			return fail("mtu")
		}
		return nil
	}
	return schema
}

func validOpenVPNStaticKey(value string) bool {
	var encoded strings.Builder
	for _, raw := range strings.Split(value, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-----BEGIN OpenVPN Static key") || strings.HasPrefix(line, "-----END OpenVPN Static key") {
			continue
		}
		if len(line) > 512-encoded.Len() {
			return false
		}
		encoded.WriteString(line)
	}
	key, err := hex.DecodeString(encoded.String())
	return err == nil && len(key) == 256
}
