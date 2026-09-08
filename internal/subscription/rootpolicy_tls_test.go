package subscription

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"go.yaml.in/yaml/v3"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func policyTLSFixture(t *testing.T, seedByte ...byte) (string, string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if len(seedByte) > 0 {
		seed[0] = seedByte[0]
	}
	private := ed25519.NewKeyFromSeed(seed)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC), DNSNames: []string{"example.test"}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, private.Public(), private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
}

func tlsProxyInput(t *testing.T, certificate, key string) PolicyInput {
	t.Helper()
	data := struct {
		Proxies []struct {
			Name, Type, Server string
			Port               int
			TLS                bool
			Certificate        string `yaml:"certificate"`
			Key                string `yaml:"private-key"`
		} `yaml:"proxies"`
		Rules []string `yaml:"rules"`
	}{Rules: []string{"MATCH,DIRECT"}}
	data.Proxies = append(data.Proxies, struct {
		Name, Type, Server string
		Port               int
		TLS                bool
		Certificate        string `yaml:"certificate"`
		Key                string `yaml:"private-key"`
	}{"fixture", "http", "example.test", 443, true, certificate, key})
	input := rootPolicyInput()
	var err error
	input.YAML, err = yaml.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestRootPolicy_TLSInlinePairAndFileFallback(t *testing.T) {
	certificate, key := policyTLSFixture(t)
	if _, err := NewRootConfigPolicy().Build(context.Background(), tlsProxyInput(t, certificate, key)); err != nil {
		t.Fatalf("inline TLS pair rejected: %v", err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "client.crt"), filepath.Join(dir, "client.key")
	if err := os.WriteFile(certPath, []byte(certificate), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{certPath, keyPath}, {certificate, keyPath}, {certPath, key}, {"", key}} {
		_, err := NewRootConfigPolicy().Build(context.Background(), tlsProxyInput(t, pair[0], pair[1]))
		var failure PolicyError
		if !errors.As(err, &failure) || failure.Field != "proxies[].certificate" {
			t.Fatalf("TLS boundary failure path is not registered pair field: %v", err)
		}
		if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Fatal("TLS material leaked")
		}
	}
}

func TestRootPolicy_AllRegisteredClientTLSPairs(t *testing.T) {
	certificate, key := policyTLSFixture(t)
	for _, kind := range []string{"anytls", "gost-relay", "http", "hysteria", "hysteria2", "socks5", "trojan", "trusttunnel", "tuic", "vless", "vmess"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range []struct {
				field, certificate, key string
				valid                   bool
			}{
				{"inline", certificate, key, true},
				{"certificate", "/private/secret-value.crt", key, false},
				{"private-key", certificate, "/private/secret-value.key", false},
			} {
				t.Run(tc.field, func(t *testing.T) {
					changes := struct {
						Certificate string `yaml:"certificate"`
						Key         string `yaml:"private-key"`
					}{tc.certificate, tc.key}
					extra, err := yaml.Marshal(changes)
					if err != nil {
						t.Fatal(err)
					}
					out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, kind, string(extra)))
					if tc.valid {
						if err != nil {
							t.Fatalf("valid inline pair rejected: %v", err)
						}
						var generated struct {
							Proxies []struct {
								Certificate string `yaml:"certificate"`
								Key         string `yaml:"private-key"`
							} `yaml:"proxies"`
						}
						if err := yaml.Unmarshal(out.YAML, &generated); err != nil {
							t.Fatal(err)
						}
						if generated.Proxies[0].Certificate != certificate || generated.Proxies[0].Key != key {
							t.Fatal("inline certificate/key bytes changed")
						}
					} else {
						var failure PolicyError
						if !errors.As(err, &failure) || failure.Field != "proxies[].certificate" {
							t.Fatalf("file fallback pair not rejected at safe boundary: %v", err)
						}
						if strings.Contains(err.Error(), "secret-value") {
							t.Fatal("certificate path leaked")
						}
					}
				})
			}
		})
	}
}
