package subscription

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestRootPolicy_AnyTLSSolitaryCarriageReturnIsData(t *testing.T) {
	const metadata = "before\rafter"
	out, err := NewRootConfigPolicy().Build(context.Background(), anyTLSPolicyInput("    client-metadata: "+strconv.Quote(metadata)+"\n"))
	if err != nil {
		t.Fatalf("non-separator CR rejected: %v", err)
	}
	var generated struct {
		Proxies []struct {
			Metadata string `yaml:"client-metadata"`
		} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out.YAML, &generated); err != nil {
		t.Fatal(err)
	}
	if generated.Proxies[0].Metadata != metadata {
		t.Fatal("native inline metadata bytes changed")
	}
}

func TestRootPolicy_VMessEmptyIdentityIsExplicitData(t *testing.T) {
	input := vmessPolicyInput("")
	input.YAML = []byte(strings.Replace(string(input.YAML), "uuid: arbitrary-identity", "uuid: ''", 1))
	out, err := NewRootConfigPolicy().Build(context.Background(), input)
	if err != nil {
		t.Fatalf("native UUID-v5 empty input rejected: %v", err)
	}
	var generated struct {
		Proxies []map[string]yaml.Node `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out.YAML, &generated); err != nil {
		t.Fatal(err)
	}
	identity, declared := generated.Proxies[0]["uuid"]
	if !declared || identity.Tag != "!!str" || identity.Value != "" {
		t.Fatal("explicit empty identity lost or changed to null")
	}
}

func TestRootPolicy_MekyaPoolRepresentationDoesNotAllocateTransports(t *testing.T) {
	for _, size := range []int64{math.MinInt64, 0, 1, 2, 3, math.MaxInt64 / 16, math.MaxInt64/16 + 1} {
		t.Run(strconv.FormatInt(size, 10), func(t *testing.T) {
			input := vmessPolicyInput("    network: mekya\n    mekya-opts: {url: 'https://example.test/carrier', h2-pool-size: " + strconv.FormatInt(size, 10) + "}\n")
			out, err := NewRootConfigPolicy().Build(context.Background(), input)
			valid := size <= math.MaxInt64/16
			if (err == nil) != valid {
				t.Fatalf("backing-size representation valid=%v error=%v", valid, err)
			}
			if !valid {
				return
			}
			var generated struct {
				Proxies []struct {
					Mekya struct {
						Pool int64 `yaml:"h2-pool-size"`
					} `yaml:"mekya-opts"`
				} `yaml:"proxies"`
			}
			if err := yaml.Unmarshal(out.YAML, &generated); err != nil {
				t.Fatal(err)
			}
			if generated.Proxies[0].Mekya.Pool != size {
				t.Fatal("native pool branch rewritten")
			}
		})
	}
}

func TestRootPolicy_SnifferPortListsHaveNoCombinedRangeCountLimit(t *testing.T) {
	var ports []string
	for i := 0; i < 40; i++ {
		ports = append(ports, strconv.Quote(strconv.Itoa(i)))
	}
	out, err := NewRootConfigPolicy().Build(context.Background(), dnsPolicyInput("sniffer: {sniff: {TLS: {ports: ["+strings.Join(ports, ",")+"]}}}\n"))
	if err != nil {
		t.Fatalf("native independent port list rejected: %v", err)
	}
	var generated struct {
		Sniffer struct {
			Sniff map[string]struct {
				Ports []string `yaml:"ports"`
			} `yaml:"sniff"`
		} `yaml:"sniffer"`
	}
	if err := yaml.Unmarshal(out.YAML, &generated); err != nil {
		t.Fatal(err)
	}
	if got := generated.Sniffer.Sniff["TLS"].Ports; len(got) != 40 || got[0] != "0" || got[39] != "39" {
		t.Fatal("sniffer port sequence shortened or reordered")
	}
}
