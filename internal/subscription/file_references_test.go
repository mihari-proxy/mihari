package subscription

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"testing"
)

func TestFileReferences_SemanticPathsOnly(t *testing.T) {
	content := []byte("proxies: []\nproxy-providers:\n  local: {type: file, path: nodes.yaml}\n  remote: {type: http, url: 'https://example.test/nodes', path: cache/nodes.yaml}\nrule-providers:\n  local: {type: file, path: rules.yaml}\ntls:\n  certificate: cert.pem\n  private-key: '-----BEGIN PRIVATE KEY-----inline'\nother: {path: ignore.yaml}\n")
	doc, err := ParseDocument(content)
	if err != nil {
		t.Fatal(err)
	}
	if refs := FileReferences(doc); len(refs) != 4 {
		t.Fatalf("refs=%v", refs)
	}
	base := t.TempDir()
	ResolveFileReferences(doc, base)
	providers, _ := referenceMap(doc["proxy-providers"])
	local, _ := referenceMap(providers["local"])
	remote, _ := referenceMap(providers["remote"])
	if local["path"] != filepath.Join(base, "nodes.yaml") || remote["path"] != "cache/nodes.yaml" {
		t.Fatalf("local=%v remote=%v", local, remote)
	}
	tls, _ := referenceMap(doc["tls"])
	if tls["certificate"] != filepath.Join(base, "cert.pem") || tls["private-key"] != filepath.Join(base, "-----BEGIN PRIVATE KEY-----inline") {
		t.Fatalf("tls=%v", tls)
	}
}

func TestFileReferences_InlineTLSAndSSHRemainUnchanged(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	private := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	tls := Document{"certificate": cert, "private-key": private, "client-auth-cert": cert}
	ssh := Document{"type": "ssh", "private-key": private}
	doc := Document{"proxies": []any{ssh}, "tls": tls, "listeners": []any{Document{"type": "vless", "reality-config": Document{"private-key": "inline-base64"}}}}
	if refs := FileReferences(doc); len(refs) != 0 {
		t.Fatalf("inline values became paths: %v", refs)
	}
	ResolveFileReferences(doc, t.TempDir())
	if tls["certificate"] != cert || tls["private-key"] != private || ssh["private-key"] != private {
		t.Fatal("inline values changed")
	}
}
