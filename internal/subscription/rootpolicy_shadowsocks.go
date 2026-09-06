package subscription

import "strings"

func ssrProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	fields["password"], fields["obfs-param"], fields["protocol-param"], fields["udp"] = stringSchema(), stringSchema(), stringSchema(), boolSchema()
	cipher := stringSchema()
	cipher.check = func(v policyValue) bool {
		if v.text == "none" || v.text == "dummy" {
			return true
		}
		switch strings.ToLower(v.text) {
		case "rc4-md5", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "chacha20", "chacha20-ietf", "xchacha20":
			return true
		default:
			return false
		}
	}
	fields["cipher"] = cipher
	fields["obfs"] = &policySchema{kind: policyString, choices: []string{"plain", "http_simple", "http_post", "random_head", "tls1.2_ticket_auth", "tls1.2_ticket_fastauth"}}
	fields["protocol"] = &policySchema{kind: policyString, choices: []string{"origin", "auth_aes128_md5", "auth_aes128_sha1", "auth_sha1_v4", "auth_chain_a", "auth_chain_b"}}
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "password", "cipher", "obfs", "protocol")
	schema.validate = func(v policyValue, field string) error {
		obfs, _ := v.get("obfs")
		if obfs.text == "tls1.2_ticket_auth" || obfs.text == "tls1.2_ticket_fastauth" {
			hosts, _ := v.get("obfs-param")
			if hosts.text == "" {
				hosts, _ = v.get("server")
			}
			// The native whole-string trailing-digit check happens before splitting.
			if len(hosts.text) > 0 {
				last := hosts.text[len(hosts.text)-1]
				if last >= '0' && last <= '9' {
					return nil
				}
			}
			for host := range strings.SplitSeq(hosts.text, ",") {
				// Largest random ticket384 + all other record fields186 =570.
				if len(host) > 65535-570 {
					return policyFailure(field + ".obfs-param")
				}
			}
		}
		return nil
	}
	return schema
}
