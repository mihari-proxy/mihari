package subscription

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"go.yaml.in/yaml/v3"
)

// RootPolicyID identifies the immutable configuration semantics for the supported core.
const RootPolicyID = "mihari.root-config/v1/mihomo-v1.19.30"

// PolicyInput contains a subscription candidate and already verified resource bytes.
// Resources uses target/source provider IDs or compiled GeoResourceID keys,
// never local filesystem paths. Source authentication belongs to the caller;
// Build and Inspect independently validate every supplied resource's structure.
type PolicyInput struct {
	YAML              []byte
	SubscriptionID    string
	Generation        uint64
	CoreTag, OS, Arch string
	Settings          config.Settings
	Resources         map[string][]byte
}

// PolicyOutput contains freshly generated core YAML and validated provider resources.
type PolicyOutput struct {
	YAML      []byte
	Providers []ProviderSpec
	Geo       []GeoResourceSpec
}

// ProviderSpec carries a validated provider source to the daemon-owned lifecycle.
// Inline holds generated, validated bytes for all source kinds.
type ProviderSpec struct {
	SubscriptionID                                string
	Generation                                    uint64
	Kind, Name, Format, Behavior, URL, ResourceID string
	SourceResourceID                              string
	Interval                                      time.Duration
	Inline                                        []byte
	Header                                        map[string][]string
	MaxBytes                                      int64
}

// PolicyError reports a registered field path without disclosing candidate values.
type PolicyError struct {
	Field string
	Code  protocol.ErrorCode
}

func (e PolicyError) Error() string { return "root configuration policy: " + e.Field }

// RootConfigPolicy constructs candidates using the compiled, versioned field registry.
type RootConfigPolicy struct{}

// NewRootConfigPolicy constructs an immutable configuration policy.
func NewRootConfigPolicy() *RootConfigPolicy { return &RootConfigPolicy{} }

// Inspect validates a candidate and discovers resources without executable YAML.
func (p *RootConfigPolicy) Inspect(ctx context.Context, input PolicyInput) (PolicyRequirements, error) {
	prepared, err := prepareRootPolicy(ctx, input, false)
	return prepared.requirements, err
}

// Build validates the complete candidate before emitting a fresh configuration.
func (p *RootConfigPolicy) Build(ctx context.Context, input PolicyInput) (PolicyOutput, error) {
	prepared, err := prepareRootPolicy(ctx, input, true)
	return prepared.output, err
}

type policyRootResult struct {
	output       PolicyOutput
	requirements PolicyRequirements
}

func prepareRootPolicy(ctx context.Context, input PolicyInput, complete bool) (policyRootResult, error) {
	if err := ctx.Err(); err != nil {
		return policyRootResult{}, err
	}
	if input.CoreTag != "v1.19.30" || (input.OS != "linux" && input.OS != "darwin") || (input.Arch != "amd64" && input.Arch != "arm64") {
		return policyRootResult{}, PolicyError{Field: "core", Code: protocol.CodeInvalidState}
	}
	if !profileIDPattern.MatchString(input.SubscriptionID) || input.Generation == 0 {
		return policyRootResult{}, policyFailure("identity")
	}
	if len(input.YAML) == 0 || len(input.YAML) > maxDocumentBytes {
		return policyRootResult{}, policyFailure("$")
	}
	if input.Settings.ControllerSecret == "" {
		return policyRootResult{}, policyFailure("settings.controller-secret")
	}
	for _, addr := range []struct{ field, value string }{{"settings.mixed-addr", input.Settings.MixedAddr}, {"settings.controller-addr", input.Settings.ControllerAddr}} {
		parsed, err := netip.ParseAddrPort(addr.value)
		if err != nil || !parsed.Addr().IsLoopback() || parsed.Port() == 0 || parsed.Addr().Zone() != "" {
			return policyRootResult{}, policyFailure(addr.field)
		}
	}
	var syntax yaml.Node
	syntaxDecoder := yaml.NewDecoder(bytes.NewReader(input.YAML))
	if err := syntaxDecoder.Decode(&syntax); err != nil {
		return policyRootResult{}, PolicyError{Field: "$", Code: protocol.CodeDataFailure}
	}
	var extra yaml.Node
	if err := syntaxDecoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return policyRootResult{}, PolicyError{Field: "$", Code: protocol.CodeDataFailure}
	}
	if syntax.Kind != yaml.DocumentNode || len(syntax.Content) != 1 {
		return policyRootResult{}, policyFailure("$")
	}
	candidate, err := decodePolicyValueContext(ctx, syntax.Content[0], rootSchema(), "")
	if err != nil {
		return policyRootResult{}, err
	}
	managedTun, err := policyManagedTun(input.Settings.Tun)
	if err != nil {
		return policyRootResult{}, err
	}
	if err := validateSubruleGraph(ctx, candidate, input.OS); err != nil {
		return policyRootResult{}, err
	}
	if _, declared := candidate.get("geodata-mode"); !declared {
		candidate.set("geodata-mode", policyValue{kind: policyBool})
	}
	providers, err := preparePolicyProviderSet(ctx, input, candidate, complete)
	if err != nil {
		return policyRootResult{}, err
	}
	if err := validatePolicyProxyGraph(ctx, candidate, providers); err != nil {
		return policyRootResult{}, err
	}
	graph, err := collectPolicyResourceGraph(ctx, candidate, providers)
	if err != nil {
		return policyRootResult{}, err
	}
	if graph.linuxOnlyField != "" && input.OS != "linux" {
		return policyRootResult{}, policyFailure(graph.linuxOnlyField)
	}
	globalIPv6, declaredIPv6 := candidate.get("ipv6")
	dns, _ := candidate.get("dns")
	if declaredIPv6 && !globalIPv6.boolean && xhttpText(dns, "enhanced-mode") == "fake-ip" {
		if v4, declared := dns.get("fake-ip-range"); declared && v4.text == "" {
			return policyRootResult{}, policyFailure("dns.fake-ip-range")
		}
	}
	if err := validateRuleProviderReferences(ctx, graph, providers); err != nil {
		return policyRootResult{}, err
	}
	geoKinds, geo, err := preparePolicyGeo(ctx, input, candidate, graph, complete)
	if err != nil {
		return policyRootResult{}, err
	}
	if err := validatePolicyResourceIdentities(input, candidate); err != nil {
		return policyRootResult{}, err
	}
	requirements := PolicyRequirements{Geo: geoKinds}
	for _, provider := range providers {
		requirements.Providers = append(requirements.Providers, provider.spec)
	}
	if !complete {
		// Discovery shares all parsing and present-resource validation, but never
		// renders candidate YAML or claims missing resource bytes are ready.
		return policyRootResult{requirements: requirements}, nil
	}
	for _, kind := range []string{"proxy", "rule"} {
		if _, declared := candidate.get(kind + "-providers"); !declared {
			continue
		}
		managed := policyValue{kind: policyObject}
		for _, provider := range providers {
			if provider.kind == kind {
				managed.set(provider.name, provider.definition)
			}
		}
		candidate.set(kind+"-providers", managed)
	}
	candidate.set("tun", managedTun)
	for _, name := range []string{"global-client-fingerprint", "clash-for-android", "geo-update-interval"} {
		candidate.remove(name)
	}
	if experimental, ok := candidate.get("experimental"); ok {
		experimental.remove("fingerprints")
		candidate.set("experimental", experimental)
	}
	mixed, err := netip.ParseAddrPort(input.Settings.MixedAddr)
	if err != nil {
		return policyRootResult{}, PolicyError{Field: "settings.mixed-addr", Code: protocol.CodeDataFailure}
	}
	candidate.set("mixed-port", policyValue{kind: policyUint, unsigned: uint64(mixed.Port())})
	candidate.set("bind-address", policyValue{kind: policyString, text: mixed.Addr().String()})
	candidate.set("allow-lan", policyValue{kind: policyBool, boolean: false})
	candidate.set("external-controller", policyValue{kind: policyString, text: input.Settings.ControllerAddr})
	candidate.set("secret", policyValue{kind: policyString, text: input.Settings.ControllerSecret})
	candidate.set("profile", policyValue{kind: policyObject, fields: []policyMember{
		{name: "store-selected", value: policyValue{kind: policyBool}},
		{name: "store-fake-ip", value: policyValue{kind: policyBool}},
	}})
	candidate.set("geo-auto-update", policyValue{kind: policyBool})
	geoURLs := policyValue{kind: policyObject}
	for _, name := range []string{"geoip", "mmdb", "asn", "geosite"} {
		geoURLs.set(name, policyValue{kind: policyString})
	}
	candidate.set("geox-url", geoURLs)
	content, err := yaml.Marshal(candidate.yamlNode())
	if err != nil {
		return policyRootResult{}, PolicyError{Field: "$", Code: protocol.CodeDataFailure}
	}
	return policyRootResult{output: PolicyOutput{YAML: content, Providers: requirements.Providers, Geo: geo}, requirements: requirements}, nil
}
