package subscription

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

func rootPolicyInput() PolicyInput {
	settings := config.Defaults()
	settings.ControllerSecret = strings.Repeat("a", 64)
	return PolicyInput{
		YAML:           []byte("proxies: []\nproxy-groups: []\nrules: ['MATCH,DIRECT']\n"),
		SubscriptionID: "00000000000000000000000000000001", Generation: 1,
		CoreTag: "v1.19.30", OS: "linux", Arch: "amd64", Settings: settings,
	}
}

func TestRootPolicy_RejectsControllerUnix(t *testing.T) {
	input := rootPolicyInput()
	policy := NewRootConfigPolicy()
	if _, err := policy.Build(context.Background(), input); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	input.YAML = append(input.YAML, []byte("external-controller-unix: /etc/cron.d/secret-value\n")...)
	_, err := policy.Build(context.Background(), input)
	var failure PolicyError
	if !errors.As(err, &failure) || failure.Field != "external-controller-unix" || failure.Code != protocol.CodeDataFailure {
		t.Fatalf("wrong failure: %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("sensitive value leaked")
	}
}

func TestRootPolicy_GeneratesManagedPositiveControl(t *testing.T) {
	input := rootPolicyInput()
	if _, err := ParseDocument(input.YAML); err != nil {
		t.Fatal(err)
	}
	output, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("positive control rejected: %v", err)
	}
	var got struct {
		MixedPort   int      `yaml:"mixed-port"`
		BindAddress string   `yaml:"bind-address"`
		Controller  string   `yaml:"external-controller"`
		Secret      string   `yaml:"secret"`
		Rules       []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(output.YAML, &got); err != nil {
		t.Fatal(err)
	}
	if got.MixedPort != 9190 || got.BindAddress != "127.0.0.1" || got.Controller != "127.0.0.1:9090" || got.Secret != input.Settings.ControllerSecret {
		t.Fatal("managed loopback settings were not generated")
	}
	if len(got.Rules) != 1 || got.Rules[0] != "MATCH,DIRECT" {
		t.Fatal("rule order/data lost")
	}
}

func TestRootPolicy_RejectsAdditionalDocument(t *testing.T) {
	input := rootPolicyInput()
	input.YAML = append(input.YAML, []byte("---\nexternal-controller-unix: /private/secret-value\n")...)
	_, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err == nil {
		t.Fatal("second YAML document accepted")
	}
}

func TestRootPolicy_RejectsUnsupportedTuple(t *testing.T) {
	for _, tc := range []struct{ name, tag, os, arch string }{
		{"tag", "v1.19.31", "linux", "amd64"},
		{"os", "v1.19.30", "windows", "amd64"},
		{"arch", "v1.19.30", "linux", "386"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			input.CoreTag, input.OS, input.Arch = tc.tag, tc.os, tc.arch
			_, err := NewRootConfigPolicy().Build(context.Background(), input)
			var failure PolicyError
			if !errors.As(err, &failure) || failure.Code != protocol.CodeInvalidState {
				t.Fatalf("unsupported tuple accepted or misclassified: %v", err)
			}
		})
	}
}

func TestRootPolicy_ValidatesManagedInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PolicyInput)
	}{
		{"identity", func(v *PolicyInput) { v.SubscriptionID = "../../private" }},
		{"generation", func(v *PolicyInput) { v.Generation = 0 }},
		{"mixed-wildcard", func(v *PolicyInput) { v.Settings.MixedAddr = "0.0.0.0:9190" }},
		{"controller-wildcard", func(v *PolicyInput) { v.Settings.ControllerAddr = "[::]:9090" }},
		{"empty-secret", func(v *PolicyInput) { v.Settings.ControllerSecret = "" }},
		{"zero-port", func(v *PolicyInput) { v.Settings.MixedAddr = "127.0.0.1:0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rootPolicyInput()
			tc.change(&input)
			_, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err == nil {
				t.Fatal("invalid managed input accepted")
			}
		})
	}
}

func TestRootPolicy_RespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewRootConfigPolicy().Build(ctx, rootPolicyInput())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

func TestRootPolicy_RootNetworkFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"ipv6", "true", "'true'"}, {"unified-delay", "true", "1"},
		{"mode", "global", "secret-value"}, {"log-level", "debug", "trace"},
		{"inbound-tfo", "true", "1"}, {"inbound-mptcp", "true", "1"},
		{"tcp-concurrent", "true", "1"}, {"etag-support", "false", "1"},
		{"disable-keep-alive", "true", "1"},
		{"keep-alive-idle", "-1", "9223372037"}, {"keep-alive-interval", "30", "9223372037"},
		{"routing-mark", "4294967295", "4294967296"},
		{"interface-name", "en0", "[en0]"}, {"global-ua", "'Example/1.0'", "[bad]"},
		{"find-process-mode", "strict", "execute"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			input := rootPolicyInput()
			input.YAML = append(input.YAML, []byte(tc.field+": "+tc.good+"\n")...)
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("valid field rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{tc.field}, tc.good)
			input = rootPolicyInput()
			input.YAML = append(input.YAML, []byte(tc.field+": "+tc.bad+"\n")...)
			_, err = NewRootConfigPolicy().Build(context.Background(), input)
			assertPolicyDataFailure(t, err, tc.field)
		})
	}
}

func TestRootPolicy_DisablesNativeAssetAndProfileWriters(t *testing.T) {
	input := rootPolicyInput()
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profile *struct {
			Selected bool `yaml:"store-selected"`
			Fake     bool `yaml:"store-fake-ip"`
		} `yaml:"profile"`
		GeoURLs map[string]string `yaml:"geox-url"`
		Auto    *bool             `yaml:"geo-auto-update"`
	}
	if err := yaml.Unmarshal(out.YAML, &got); err != nil {
		t.Fatal(err)
	}
	if got.Profile == nil || got.Profile.Selected || got.Profile.Fake || got.Auto == nil || *got.Auto || len(got.GeoURLs) != 4 {
		t.Fatal("native writers/default download URLs were not explicitly disabled")
	}
	for _, v := range got.GeoURLs {
		if v != "" {
			t.Fatal("native Geo URL retained")
		}
	}
}

func TestRootPolicy_NTPFields(t *testing.T) {
	for _, tc := range []struct{ field, good, bad string }{
		{"enable", "true", "1"}, {"server", "time.example.test", "[bad]"},
		{"port", "123", "65536"}, {"interval", "30", "153722868"},
		{"dialer-proxy", "DIRECT", "[bad]"}, {"write-to-system", "false", "true"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			input := rootPolicyInput()
			input.YAML = append(input.YAML, []byte("ntp:\n  "+tc.field+": "+tc.good+"\n")...)
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			if err != nil {
				t.Fatalf("valid NTP field rejected: %v", err)
			}
			assertPolicyRootLeaf(t, out.YAML, []string{"ntp", tc.field}, tc.good)
			input = rootPolicyInput()
			input.YAML = append(input.YAML, []byte("ntp:\n  "+tc.field+": "+tc.bad+"\n")...)
			_, err = NewRootConfigPolicy().Build(context.Background(), input)
			assertPolicyDataFailure(t, err, "ntp."+tc.field)
		})
	}
}
