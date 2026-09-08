package subscription

import (
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"strings"
)

func tlsFingerprintSchema() *policySchema {
	s := stringSchema()
	s.check = func(v policyValue) bool {
		if v.text == "" {
			return true
		}
		decoded, err := hex.DecodeString(strings.TrimSpace(strings.ReplaceAll(v.text, ":", "")))
		return err == nil && len(decoded) == 32
	}
	return s
}

func addClientTLSFields(fields map[string]*policySchema) {
	fields["skip-cert-verify"] = boolSchema()
	fields["name-cert-verify"] = stringSchema()
	fields["fingerprint"] = tlsFingerprintSchema()
	fields["certificate"], fields["private-key"] = stringSchema(), stringSchema()
}

// validClientKeyPair proves that the pinned core selects its inline branch.
// A failed parse must never be forwarded because the core then tries file IO.
func validClientKeyPair(v policyValue) bool {
	certificate, _ := v.get("certificate")
	key, _ := v.get("private-key")
	if certificate.text == "" && key.text == "" {
		return true
	}
	_, err := tls.X509KeyPair([]byte(certificate.text), []byte(key.text))
	return err == nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r) {
			continue
		}
		return false
	}
	return true
}

func validHTTPValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 127 || value[i] < 32 && value[i] != '\t' {
			return false
		}
	}
	return true
}

func policyHeadersSchema(multiple bool) *policySchema {
	value := stringSchema()
	value.check = func(v policyValue) bool { return validHTTPValue(v.text) }
	if multiple {
		value = listSchema(value)
	}
	s := &policySchema{kind: policyObject, dynamic: value, normalize: http.CanonicalHeaderKey}
	s.check = func(v policyValue) bool {
		for _, field := range v.fields {
			if !validHTTPToken(field.name) {
				return false
			}
		}
		return true
	}
	return s
}
