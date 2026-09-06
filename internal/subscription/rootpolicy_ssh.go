package subscription

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"math/big"
	"strings"
)

func sshProxySchema() *policySchema {
	fields := proxyBaseFields()
	fields["server"], fields["port"] = policyNameSchema(), integerSchema(1, 65535)
	for _, name := range []string{"username", "password", "private-key", "private-key-passphrase"} {
		fields[name] = stringSchema()
	}
	host := stringSchema()
	host.check = func(v policyValue) bool { return validSSHAuthorizedKey(v.text) }
	fields["host-key"], fields["host-key-algorithms"] = listSchema(host), listSchema(stringSchema())
	schema := proxyObject(fields)
	schema.required = append(schema.required, "server", "port", "username")
	schema.validate = func(v policyValue, field string) error {
		key, _ := v.get("private-key")
		passphrase, _ := v.get("private-key-passphrase")
		if key.text != "" && !validSSHPrivateKey(key.text, passphrase.text) {
			return policyFailure(field + ".private-key")
		}
		return nil
	}
	return schema
}

// The pinned SSH adapter decides inline versus file before parsing. No parse
// failure can fall back to a file. Unlike TLS, encrypted OpenSSH crypto can be
// checked by the trusted candidate core after this finite inline framing check.
func validSSHPrivateKey(value, passphrase string) bool {
	if len(value) > maxDocumentBytes {
		return false
	}
	encoded := bytes.TrimSpace([]byte(value))
	if !bytes.HasPrefix(encoded, []byte("-----BEGIN ")) || bytes.Count(encoded, []byte("-----BEGIN ")) != 1 {
		return false
	}
	block, rest := pem.Decode(encoded)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Bytes) == 0 {
		return false
	}
	if block.Type == "OPENSSH PRIVATE KEY" {
		return validOpenSSHPrivateKey(block.Bytes, passphrase != "")
	}
	der := block.Bytes
	if strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		if passphrase == "" || !x509.IsEncryptedPEMBlock(block) || block.Type == "PRIVATE KEY" {
			return false
		}
		var err error
		der, err = x509.DecryptPEMBlock(block, []byte(passphrase))
		if err != nil {
			return false
		}
	} else if passphrase != "" {
		return false
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(der)
		return err == nil && key.Validate() == nil
	case "EC PRIVATE KEY":
		_, err := x509.ParseECPrivateKey(der)
		return err == nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return false
		}
		switch key.(type) {
		case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
			return true
		}
		return false
	case "DSA PRIVATE KEY":
		var key struct {
			Version            int
			P, Q, G, Pub, Priv *big.Int
		}
		tail, err := asn1.Unmarshal(der, &key)
		if err != nil || len(tail) != 0 {
			return false
		}
		for _, n := range []*big.Int{key.P, key.Q, key.G, key.Pub, key.Priv} {
			if n == nil || n.Sign() <= 0 {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func sshWireString(in *[]byte) ([]byte, bool) {
	if len(*in) < 4 {
		return nil, false
	}
	length := binary.BigEndian.Uint32((*in)[:4])
	*in = (*in)[4:]
	if uint64(length) > uint64(len(*in)) {
		return nil, false
	}
	value := (*in)[:int(length)]
	*in = (*in)[int(length):]
	return value, true
}

func knownSSHPublicType(kind string) bool {
	switch kind {
	case "ssh-rsa", "ssh-dss", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ecdsa-sha2-nistp256@openssh.com", "ssh-ed25519", "sk-ssh-ed25519@openssh.com",
		"ssh-rsa-cert-v01@openssh.com", "ssh-dss-cert-v01@openssh.com", "ecdsa-sha2-nistp256-cert-v01@openssh.com", "ecdsa-sha2-nistp384-cert-v01@openssh.com", "ecdsa-sha2-nistp521-cert-v01@openssh.com", "sk-ecdsa-sha2-nistp256-cert-v01@openssh.com", "ssh-ed25519-cert-v01@openssh.com", "sk-ssh-ed25519-cert-v01@openssh.com":
		return true
	default:
		return false
	}
}

func validSSHPublicFrame(in []byte) bool {
	kind, ok := sshWireString(&in)
	return ok && knownSSHPublicType(string(kind)) && len(in) > 0
}

func validOpenSSHPrivateKey(in []byte, encrypted bool) bool {
	const magic = "openssh-key-v1\x00"
	if !bytes.HasPrefix(in, []byte(magic)) {
		return false
	}
	in = in[len(magic):]
	cipher, ok := sshWireString(&in)
	if !ok {
		return false
	}
	kdf, ok := sshWireString(&in)
	if !ok {
		return false
	}
	options, ok := sshWireString(&in)
	if !ok || len(in) < 4 || binary.BigEndian.Uint32(in[:4]) != 1 {
		return false
	}
	in = in[4:]
	public, ok := sshWireString(&in)
	if !ok || !validSSHPublicFrame(public) {
		return false
	}
	private, ok := sshWireString(&in)
	if !ok || len(in) != 0 || len(private) == 0 {
		return false
	}
	if encrypted {
		if string(kdf) != "bcrypt" || (string(cipher) != "aes256-ctr" && string(cipher) != "aes256-cbc") {
			return false
		}
		salt, ok := sshWireString(&options)
		if !ok || len(salt) == 0 || len(options) != 4 || binary.BigEndian.Uint32(options) == 0 {
			return false
		}
		return string(cipher) != "aes256-cbc" || len(private)%16 == 0
	}
	if string(cipher) != "none" || string(kdf) != "none" || len(options) != 0 || len(private) < 8 || !bytes.Equal(private[:4], private[4:8]) {
		return false
	}
	private = private[8:]
	kind, ok := sshWireString(&private)
	if !ok {
		return false
	}
	count := 0
	switch string(kind) {
	case "ssh-rsa":
		count = 6
	case "ssh-ed25519":
		count = 2
	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		count = 3
	default:
		return false
	}
	for i := 0; i < count; i++ {
		part, ok := sshWireString(&private)
		if !ok || len(part) == 0 {
			return false
		}
		if string(kind) == "ssh-ed25519" && ((i == 0 && len(part) != 32) || (i == 1 && len(part) != 64)) {
			return false
		}
	}
	if _, ok := sshWireString(&private); !ok {
		return false
	} // comment is inline data.
	for i, b := range private {
		if b != byte(i+1) {
			return false
		}
	}
	return true
}

// ParseAuthorizedKey discards options, comments and the outer algorithm token.
// Preserve its quoted-option and first-valid-line grammar. Only the bounded
// key envelope is checked here; key/certificate crypto remains core validation.
func validSSHAuthorizedKey(value string) bool {
	if len(value) == 0 || len(value) > maxDocumentBytes {
		return false
	}
	validTail := func(tail string) bool {
		tail = strings.TrimSpace(tail)
		if end := strings.IndexAny(tail, " \t"); end >= 0 {
			tail = tail[:end]
		}
		key, err := base64.StdEncoding.DecodeString(tail)
		return err == nil && validSSHPublicFrame(key)
	}
	for line := range strings.SplitSeq(value, "\n") {
		if end := strings.IndexByte(line, '\r'); end >= 0 {
			line = line[:end]
		}
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		first := strings.IndexAny(line, " \t")
		if first < 0 {
			continue
		}
		if validTail(line[first:]) {
			return true
		}
		quoted := false
		end := len(line)
		for i := 0; i < len(line); i++ {
			b := line[i]
			if !quoted && (b == ' ' || b == '\t') {
				end = i
				break
			}
			if b == '"' && (i == 0 || line[i-1] != '\\') {
				quoted = !quoted
			}
		}
		if quoted || end == len(line) {
			continue
		}
		tail := strings.TrimLeft(line[end:], " \t")
		next := strings.IndexAny(tail, " \t")
		if next >= 0 && validTail(tail[next:]) {
			return true
		}
	}
	return false
}
