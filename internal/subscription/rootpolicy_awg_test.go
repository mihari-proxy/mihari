package subscription

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func awgPolicyInput(t *testing.T, extra string) PolicyInput {
	return baselineProxyInput(t, "wireguard", "amnezia-wg-option:\n  "+strings.ReplaceAll(extra, "\n", "\n  "))
}

func TestRootPolicy_AWGCanonicalTagsAndHeaderUnion(t *testing.T) {
	out, err := NewRootConfigPolicy().Build(context.Background(), awgPolicyInput(t, "h1: 0xff\ni1: '< r +1 ><b 0XF>'"))
	if err != nil {
		t.Fatalf("complete whitespace/number recipe rejected: %v", err)
	}
	var got struct {
		Proxies []struct {
			AWG struct {
				H1 string `yaml:"h1"`
				I1 string `yaml:"i1"`
			} `yaml:"amnezia-wg-option"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out.YAML, &got); err != nil {
		t.Fatal(err)
	}
	if got.Proxies[0].AWG.H1 != "255" || got.Proxies[0].AWG.I1 != "<r 1><b 0x0f>" {
		t.Fatal("fresh canonical AWG data differs")
	}
	for _, raw := range []string{"-1", "4294967296", "1.0", "true", "null", "[]"} {
		if _, err := NewRootConfigPolicy().Build(context.Background(), awgPolicyInput(t, "h1: "+raw)); err == nil {
			t.Fatal("AWG scalar union widened")
		}
	}
}

func TestRootPolicy_AWGFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad, prefix string }{
		{"version", "99", "[]", ""}, {"jc", "129", "-1", ""}, {"jmin", "0", "-1", ""}, {"jmax", "65534", "65535", ""},
		{"s1", "65386", "65387", ""}, {"s2", "65442", "65443", ""}, {"s3", "65470", "65471", ""}, {"s4", "65502", "65503", ""},
		{"h1", "'5-6'", "'5-4'", ""}, {"h2", "'4294967295'", "'4294967296'", ""}, {"h3", "'30'", "'3\\nlisten_port=80'", ""}, {"h4", "'40'", "[]", ""},
		{"i1", "'<b 0Xf><r 1><rc 2><rd 3><t><c><wt 0>'", "'<r -1>'", ""},
		{"i2", "'<r 1>'", "'<wr>'", "i1: '<b 0x01>'\n"}, {"i3", "'<r 1>'", "'<unknown>'", "i1: '<b 0x01>'\ni2: '<r 1>'\n"},
		{"i4", "'<d><ds><dz 2><t ignored>'", "'<c>'", "version: 3\n"}, {"i5", "'<b 01><rc 1025><rd 1>'", "'<b 0X01>'", "version: 3\n"},
		{"j1", "'<r 1000>'", "'<r 1001>'", ""}, {"j2", "'<t>'", "'<t><t>'", "j1: '<r 1>'\n"}, {"j3", "'<c>'", "'<c><c>'", "j1: '<r 1>'\nj2: '<r 1>'\n"},
		{"itime", "-9223372036", "-9223372037", ""},
		{"header-protection-key", "'" + base64.StdEncoding.EncodeToString(make([]byte, 32)) + "'", "AAAA", "version: 3\n"},
		{"content-padding-addition", "'0-4294967294'", "'0-4294967295'", "version: 3\n"},
		{"rekey-after-time", "'4294967295'", "'4294967296'", "version: 3\n"}, {"rekey-timeout", "'0-4294967294'", "'1-0'", "version: 3\n"},
		{"reject-after-time", "'3074457345'", "'3074457346'", "version: 3\n"}, {"keepalive-timeout", "'0'", "'1s'", "version: 3\n"},
		{"max-handshake-attempts", "'4294967295'", "'-1'", "version: 3\n"}, {"random-trailers", "true", "1", "version: 3\n"}, {"disable-cookies", "true", "1", "version: 3\n"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			out, err := NewRootConfigPolicy().Build(context.Background(), awgPolicyInput(t, tc.prefix+tc.field+": "+tc.good))
			if err != nil || !strings.Contains(string(out.YAML), tc.field+":") {
				t.Fatalf("valid AWG field lost: %v", err)
			}
			if _, err := NewRootConfigPolicy().Build(context.Background(), awgPolicyInput(t, tc.prefix+tc.field+": "+tc.bad)); err == nil {
				t.Fatal("invalid AWG field accepted")
			}
		})
	}
}

func TestRootPolicy_AWGBranches(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(append([]byte{1}, make([]byte, 31)...))
	for _, tc := range []struct {
		extra string
		valid bool
	}{
		{"jc: 1", true}, {"version: 3\njc: 1", true},
		{"jc: 1\njmin: 65533\njmax: 65533", true}, {"jc: 1\njmin: 65534\njmax: 65534", false},
		{"jc: 0\njmin: 65534\njmax: 65534", true},
		{"version: 3\njc: 0\njmin: 128\njmax: 64", true}, {"version: 3\njc: 1\njmin: 128\njmax: 64", false},
		{"version: 3\njc: 4294967295", true}, {"version: 3\njc: 4294967296", false},
		{"version: 3\njmin: 4294967295\njmax: 4294967295", true},
		{"version: 3\ns1: 65535", true}, {"version: 3\ns2: 65535", true}, {"version: 3\ns3: 65535", true}, {"version: 3\ns1: 65536", false},
		{"version: 3\ns4: 65519", true}, {"version: 3\ns4: 65520", false},
		{"s1: 0\ns2: 56", false}, {"s1: 0\ns3: 84", false}, {"s1: 0\ns4: 116", false},
		{"s2: 0\ns3: 28", false}, {"s2: 0\ns4: 60", false}, {"s3: 0\ns4: 32", false},
		{"version: 3\ns1: 0\ns2: 56", true},
		{"version: 3\nheader-protection-key: '" + key + "'\ns1: 12\ns2: 12\ns3: 12\ns4: 12", true},
		{"version: 3\nheader-protection-key: '" + key + "'\ns1: 12\ns2: 12\ns3: 12\ns4: 11", false},
		{"h1: '0-100'", true}, {"version: 3\nh1: '0-100'", false}, {"h1: '5-9'\nh2: '9-12'", false},
		{"h1: 5", true}, {"h1: 5.0", false}, {"h1: true", false},
		{"i2: '<r 1>'", false}, {"j2: '<r 1>'", false}, {"version: 3\ni5: '<r 0>'", true},
		{"i1: '<b 0x><wt -1>'", true}, {"i1: '<wt 5001>'", false}, {"i1: '<t argument>'", false},
		{"i1: 'garbage<r 1>'", false}, {"version: 3\ni1: 'garbage<r 1>'", false},
		{"version: 3\ni1: '<b 0f><t ignored><d ignored><ds ignored><dz 0>'", true},
		{"version: 3\ni1: '<dz -1>'", false}, {"version: 3\ni1: '<r 9223372036854775807><t>'", false},
		{"version: 3\nj1: '<r 1>'", false}, {"version: 3\nitime: 1", false}, {"random-trailers: true", false},
		{"jc: 384307168202282325", true}, {"jc: 384307168202282326", false},
		{"jc: 384307168202282325\ni1: '<r 1>'", false},
	} {
		_, err := NewRootConfigPolicy().Build(context.Background(), awgPolicyInput(t, tc.extra))
		if (err == nil) != tc.valid {
			t.Fatalf("AWG branch mismatch: %v", err)
		}
	}
	for _, field := range []string{"h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5", "j1", "j2", "j3", "content-padding-addition", "rekey-after-time", "rekey-timeout", "reject-after-time", "keepalive-timeout", "max-handshake-attempts"} {
		for _, line := range []string{"\n", "\r"} {
			_, err := NewRootConfigPolicy().Build(context.Background(), awgPolicyInput(t, field+": "+strconv.Quote("5"+line+"private_key=secret-canary")))
			if err == nil || strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("AWG UAPI boundary/redaction failed")
			}
		}
	}
}
