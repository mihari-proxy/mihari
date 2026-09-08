package subscription

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func sshTestString(data []byte) []byte {
	result := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	return append(result, data...)
}

func sshPublicFixture() string {
	key := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	wire := append(sshTestString([]byte("ssh-ed25519")), sshTestString(key)...)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire)
}

func sshPolicyInput(t *testing.T, key, passphrase string, hosts []string) PolicyInput {
	t.Helper()
	data := struct {
		Proxies []struct {
			Name, Type, Server, Username, Password string
			Port                                   int
			Key                                    string   `yaml:"private-key"`
			Passphrase                             string   `yaml:"private-key-passphrase"`
			Host                                   []string `yaml:"host-key"`
			Algorithms                             []string `yaml:"host-key-algorithms"`
		} `yaml:"proxies"`
		Rules []string `yaml:"rules"`
	}{Rules: []string{"MATCH,DIRECT"}}
	data.Proxies = append(data.Proxies, struct {
		Name, Type, Server, Username, Password string
		Port                                   int
		Key                                    string   `yaml:"private-key"`
		Passphrase                             string   `yaml:"private-key-passphrase"`
		Host                                   []string `yaml:"host-key"`
		Algorithms                             []string `yaml:"host-key-algorithms"`
	}{"fixture", "ssh", "example.test", "fixture-user", "fixture-password", 22, key, passphrase, hosts, []string{"ssh-ed25519"}})
	input := rootPolicyInput()
	var err error
	input.YAML, err = yaml.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestRootPolicy_SSHInlineKeyAndAuthorizedOptions(t *testing.T) {
	_, key := policyTLSFixture(t)
	public := sshPublicFixture()
	for _, host := range []string{public, public + " fixture comment", `command="/bin/ignored --arg",from="*.example.test",no-pty ` + public + " fixture", "# ignored\nmalformed line\n" + public + "\nignored rest", strings.Replace(public, "ssh-ed25519 ", "outer-type-is-ignored ", 1)} {
		out, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, key, "", []string{host}))
		if err != nil {
			t.Fatalf("valid inline SSH key/options rejected: %v", err)
		}
		var got struct {
			Proxies []struct {
				Key  string   `yaml:"private-key"`
				Host []string `yaml:"host-key"`
			} `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(out.YAML, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Proxies) != 1 || got.Proxies[0].Key != key || len(got.Proxies[0].Host) != 1 || got.Proxies[0].Host[0] != host {
			t.Fatal("typed inline SSH data changed")
		}
	}
	for _, invalid := range []string{"/tmp/id_ed25519", "C:\\private\\id_rsa", "contains PRIVATE KEY but no PEM", key + key, "junk\n" + key, key + "trailing"} {
		_, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, invalid, "", nil))
		var failure PolicyError
		if !errors.As(err, &failure) || failure.Field != "proxies[].private-key" {
			t.Fatalf("private key rejection path: %v", err)
		}
		if strings.Contains(err.Error(), invalid) {
			t.Fatal("private key leaked")
		}
	}
	for _, host := range []string{"/tmp/authorized_keys", "ssh-ed25519 !!!!", `command="unterminated ` + public, "ssh-ed25519 AAAABHNzaA=="} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, "", "", []string{host})); err == nil {
			t.Fatal("invalid inline host key accepted")
		}
	}
}

func TestRootPolicy_SSHFieldTypes(t *testing.T) {
	base := "    server: example.test\n    port: 22\n    username: fixture-user\n"
	for _, tc := range []struct{ field, good, bad string }{
		{"password", "fixture-password", "[]"}, {"private-key", "''", "[]"},
		{"private-key-passphrase", "fixture-passphrase", "[]"}, {"host-key", "[]", "{}"},
		{"host-key-algorithms", "[ssh-ed25519]", "[123]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("ssh", base+"    "+tc.field+": "+tc.good+"\n"))
			if err != nil {
				t.Fatalf("valid SSH field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("SSH field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), simpleProxyInput("ssh", base+"    "+tc.field+": "+tc.bad+"\n")); err == nil {
				t.Fatal("invalid SSH field accepted")
			}
		})
	}
}

func TestRootPolicy_SSHOpenSSHFraming(t *testing.T) {
	for _, tc := range []struct{ file, passphrase string }{
		{"ssh-ed25519.pem", ""}, {"ssh-ed25519-encrypted.pem", "password"},
		{"ssh-ed25519-encrypted-cbc.pem", "password"},
		{"ssh-rsa-openssh-format.pem", ""}, {"ssh-p256-openssh-format.pem", ""},
		{"ssh-p384-openssh-format.pem", ""}, {"ssh-p521-openssh-format.pem", ""},
	} {
		t.Run(tc.file, func(t *testing.T) {
			key, err := os.ReadFile("testdata/rootpolicy/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, string(key), tc.passphrase, nil)); err != nil {
				t.Fatalf("valid OpenSSH inline fixture rejected: %v", err)
			}
			block, _ := pem.Decode(key)
			if block == nil {
				t.Fatal("invalid fixture")
			}
			for _, invalid := range [][]byte{block.Bytes[:len(block.Bytes)-1], append(append([]byte(nil), block.Bytes...), 1), []byte("openssh-key-v1\x00\xff\xff\xff\xff")} {
				encoded := pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: invalid})
				if _, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, string(encoded), tc.passphrase, nil)); err == nil {
					t.Fatal("invalid OpenSSH framing accepted")
				}
			}
		})
	}
}

func TestRootPolicy_SSHLegacyPEMFormats(t *testing.T) {
	for _, tc := range []struct{ file, passphrase string }{
		{"ssh-rsa.pem", ""}, {"ssh-dsa.pem", ""}, {"ssh-ecdsa.pem", ""},
		{"ssh-rsa-encrypted.pem", "r54-G0pher_t3st$"}, {"ssh-dsa-encrypted.pem", "qG0pher-dsa_t3st$"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			key, err := os.ReadFile("testdata/rootpolicy/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, string(key), tc.passphrase, nil)); err != nil {
				t.Fatalf("valid legacy inline key rejected: %v", err)
			}
			wrong := "incorrect"
			if tc.passphrase != "" {
				wrong = ""
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), sshPolicyInput(t, string(key), wrong, nil)); err == nil {
				t.Fatal("invalid legacy passphrase mode accepted")
			}
		})
	}
}
