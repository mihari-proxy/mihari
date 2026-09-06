package subscription

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestRootPolicy_WireGuardFields(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct{ field, good, bad string }{
		{"ip", "10.0.0.2/24", "malformed"}, {"ipv6", "'2001:db8::2'", "malformed"},
		{"workers", "1", "[]"}, {"mtu", "4294967295", "4294967296"}, {"udp", "true", "1"},
		{"persistent-keepalive", "65535", "65536"}, {"ip-stack", "{mode: MIPS, congestion-controller: BBR}", "{mode: os}"},
		{"remote-dns-resolve", "true", "1"}, {"dns", "[system, tls://1.1.1.1]", "[file:///tmp/resolv]"},
		{"refresh-server-ip-interval", "-9223372036", "-9223372037"},
		{"server", "example.test", "[]"}, {"port", "0", "65536"}, {"public-key", "'" + key + "'", "AAAA"},
		{"pre-shared-key", "'" + key + "'", "AAAA"}, {"reserved", "[0, 127, 255]", "[0, 127, 256]"},
		{"allowed-ips", "['unused-root-data']", "[1]"},
		{"peers", "[{server: example.test, port: 443, public-key: '" + key + "', allowed-ips: ['0.0.0.0/0']}]", "[{server: example.test, public-key: '" + key + "', allowed-ips: ['0.0.0.0/33']}]"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "wireguard", tc.field+": "+tc.good))
			if err != nil {
				t.Fatalf("valid WireGuard field rejected: %v", err)
			}
			if !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatal("WireGuard field lost")
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "wireguard", tc.field+": "+tc.bad)); err == nil {
				t.Fatal("invalid WireGuard field accepted")
			}
		})
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"private-key: AAAA", false}, {"private-key: /tmp/key", false},
		{"pre-shared-key: ''", true}, {"reserved: []", true}, {"reserved: [1,2]", false},
		{"ip: ''\nipv6: ''", false}, {"ip: '2001:db8::2/64'", true},
		{"mtu: -1", false}, {"ip-stack: {mode: mips}\nmtu: 67", false},
		{"peers: [{public-key: '" + key + "', allowed-ips: []}]", false},
		{"peers: [{server: example.test, public-key: '" + key + "', reserved: [1,2], allowed-ips: ['::/0']}]", false},
		{"peers: [{server: example.test, public-key: '" + key + "', pre-shared-key: AAAA, allowed-ips: ['::/0']}]", false},
		{"peers: [{server: example.test, public-key: '" + key + "', allowed-ips: [\"::/0\\nlisten_port=80\"]}]", false},
		{"peers: [{server: example.test, public-key: '" + key + "', allowed-ips: ['::/0']}]\npublic-key: unused-root-key\npre-shared-key: unused-root-key", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "wireguard", tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("WireGuard branch mismatch: %v", err)
		}
	}
}

func TestRootPolicy_WireGuardWorkerReferences(t *testing.T) {
	// RFC7748 section6.1 fixed Alice vector proves nonzero scalar clamping.
	private, err := hex.DecodeString("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	if err != nil {
		t.Fatal(err)
	}
	public, err := hex.DecodeString("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	if err != nil {
		t.Fatal(err)
	}
	privateText, self := base64.StdEncoding.EncodeToString(private), base64.StdEncoding.EncodeToString(public)
	zero := base64.StdEncoding.EncodeToString(make([]byte, 32))
	peer := func(key string) string {
		return fmt.Sprintf("{server: example.test, public-key: %q, allowed-ips: ['0.0.0.0/0']}", key)
	}
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"workers: -1", false}, {"workers: -2", false}, {"workers: 0", true},
		{"workers: 2147483644", true}, {"workers: 2147483645", false},
		{"private-key: '" + privateText + "'\npublic-key: '" + self + "'\nworkers: 2147483645", true},
		{"private-key: '" + privateText + "'\npublic-key: '" + self + "'\nworkers: 2147483646", false},
		{"private-key: '" + zero + "'\npublic-key: '" + zero + "'\nworkers: 2147483645", true},
		{"private-key: '" + zero + "'\npublic-key: '" + self + "'\nworkers: 2147483645", false},
		{"private-key: '" + privateText + "'\npeers: [" + peer(zero) + ", " + peer(zero[:10]+"\n"+zero[10:]) + ", " + peer(self) + "]\nworkers: 2147483644", true},
		{"private-key: '" + privateText + "'\npeers: [" + peer(zero) + ", " + peer(zero) + ", " + peer(self) + "]\nworkers: 2147483645", false},
		{"private-key: '" + privateText + "'\npublic-key: unused\npeers: [" + peer(self) + "]\nworkers: 2147483645", true},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), baselineProxyInput(t, "wireguard", tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("worker reference bound mismatch: %v", err)
		}
	}
}
