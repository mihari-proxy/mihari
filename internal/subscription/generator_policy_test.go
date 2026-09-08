package subscription

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type recordingPolicyBuilder struct {
	ctx    context.Context
	input  PolicyInput
	output PolicyOutput
	err    error
	calls  int
}

func (b *recordingPolicyBuilder) Build(ctx context.Context, input PolicyInput) (PolicyOutput, error) {
	b.ctx, b.input = ctx, input
	b.calls++
	return b.output, b.err
}

func TestGenerateWithPolicy_PreservesRawInputContextIdentityAndResult(t *testing.T) {
	input := rootPolicyInput()
	input.YAML = []byte("# raw syntax retained\nproxies: []\nproxies: []\n")
	input.Resources = map[string][]byte{"object-fixture": []byte("raw provider bytes")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := PolicyOutput{YAML: []byte("policy-owned result"), Providers: []ProviderSpec{{ResourceID: "fixture", Inline: []byte("validated")}}, Geo: []GeoResourceSpec{{Kind: GeoSiteDAT, Bytes: []byte("Geo fixture")}}}
	builder := &recordingPolicyBuilder{output: want}
	out, err := GenerateWithPolicy(ctx, input, builder)
	if err != nil || builder.calls != 1 || builder.ctx != ctx {
		t.Fatalf("policy invocation: %v", err)
	}
	if !bytes.Equal(builder.input.YAML, input.YAML) || builder.input.SubscriptionID != input.SubscriptionID || builder.input.Generation != input.Generation || builder.input.CoreTag != input.CoreTag || builder.input.OS != input.OS || builder.input.Arch != input.Arch || builder.input.Settings.ControllerSecret != input.Settings.ControllerSecret || !bytes.Equal(builder.input.Resources["object-fixture"], input.Resources["object-fixture"]) {
		t.Fatal("policy input was reparsed or lost identity/resources")
	}
	if !bytes.Equal(out.YAML, want.YAML) || len(out.Providers) != 1 || len(out.Geo) != 1 || !bytes.Equal(out.Providers[0].Inline, want.Providers[0].Inline) || !bytes.Equal(out.Geo[0].Bytes, want.Geo[0].Bytes) {
		t.Fatal("policy output was discarded or regenerated")
	}
	sentinel := errors.New("policy failed")
	builder.output = PolicyOutput{}
	builder.err = sentinel
	if _, err := GenerateWithPolicy(ctx, input, builder); !errors.Is(err, sentinel) {
		t.Fatal("policy failure replaced")
	}
}

func TestGenerateWithPolicy_RejectsMissingPolicyAndKeepsClosedParsing(t *testing.T) {
	input := rootPolicyInput()
	_, err := GenerateWithPolicy(context.Background(), input, nil)
	var failure PolicyError
	if !errors.As(err, &failure) || failure.Code != protocol.CodeInvalidState {
		t.Fatalf("missing builder silently fell back: %v", err)
	}
	if _, err := GenerateWithPolicy(context.Background(), input, NewRootConfigPolicy()); err != nil {
		t.Fatalf("real policy positive: %v", err)
	}
	for _, suffix := range []string{"proxies: []\n", "unregistered-secret-field: true\n"} {
		attack := input
		attack.YAML = append(append([]byte(nil), input.YAML...), []byte(suffix)...)
		if _, err := GenerateWithPolicy(context.Background(), attack, NewRootConfigPolicy()); err == nil {
			t.Fatal("raw duplicate or unknown field lost through legacy generation")
		}
	}
}
