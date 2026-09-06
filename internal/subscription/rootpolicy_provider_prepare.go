package subscription

import (
	"context"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type preparedPolicyProvider struct {
	name, kind          string
	definition, payload policyValue
	spec                ProviderSpec
	ready               bool
}

const maxPolicyProviders = 256
const maxPolicyProviderTotalBytes = 256 << 20

type policyProviderBudget struct {
	sources, generated int64
	count              int
}

func (b *policyProviderBudget) source(size int64) error {
	if b.count >= maxPolicyProviders || size < 0 || size > maxDocumentBytes || size > maxPolicyProviderTotalBytes-b.sources {
		return policyFailure("providers.budget")
	}
	b.count++
	b.sources += size
	return nil
}
func (b *policyProviderBudget) output(size int64) error {
	if size < 0 || size > maxDocumentBytes || size > maxPolicyProviderTotalBytes-b.generated {
		return policyFailure("providers.budget")
	}
	b.generated += size
	return nil
}

func providerMapSchema(kind string) *policySchema {
	entry := proxyProviderDefinitionSchema()
	if kind == "rule" {
		entry = ruleProviderDefinitionSchema()
	}
	return &policySchema{kind: policyObject, dynamic: entry}
}

func preparePolicyProviderSet(ctx context.Context, input PolicyInput, root policyValue, complete bool) ([]preparedPolicyProvider, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var budget policyProviderBudget
	var definitions []preparedPolicyProvider
	// Check the source count/aggregate before parsing any external resource.
	// Reused source IDs are charged for each provider's separately validated copy.
	for _, kind := range []string{"proxy", "rule"} {
		providers, _ := root.get(kind + "-providers")
		for _, member := range providers.fields {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			id, err := ProviderResourceID(input.SubscriptionID, input.Generation, kind, member.name)
			if err != nil {
				return nil, err
			}
			typ, _ := member.value.get("type")
			if typ.text == "file" {
				source, _ := member.value.get("path")
				id = source.text
			}
			if err := budget.source(int64(len(input.Resources[id]))); err != nil {
				return nil, err
			}
			definitions = append(definitions, preparedPolicyProvider{kind: kind, name: member.name, definition: member.value})
		}
	}
	result := make([]preparedPolicyProvider, 0, len(definitions))
	for _, entry := range definitions {
		prepared, err := preparePolicyProvider(ctx, input, entry.kind, entry.name, entry.definition, complete)
		if err != nil {
			return nil, err
		}
		if err := budget.output(int64(len(prepared.spec.Inline))); err != nil {
			return nil, err
		}
		result = append(result, prepared)
	}
	return result, nil
}

func validatePolicyResourceIdentities(input PolicyInput, root policyValue) error {
	allowed := make(map[string]bool)
	for _, kind := range []string{"proxy", "rule"} {
		definitions, _ := root.get(kind + "-providers")
		for _, member := range definitions.fields {
			id, err := ProviderResourceID(input.SubscriptionID, input.Generation, kind, member.name)
			if err != nil {
				return err
			}
			if typ, _ := member.value.get("type"); typ.text == "file" {
				source, _ := member.value.get("path")
				id = source.text
			}
			allowed[id] = true
		}
	}
	for _, kind := range []GeoResourceKind{GeoCountryMMDB, GeoASNMMDB, GeoIPDAT, GeoSiteDAT} {
		id, err := GeoResourceID(kind)
		if err != nil {
			return err
		}
		allowed[id] = true
	}
	for id := range input.Resources {
		if !allowed[id] {
			return policyFailure("resources.[entry]")
		}
	}
	return nil
}

func preparePolicyProvider(ctx context.Context, input PolicyInput, kind, name string, definition policyValue, requireComplete bool) (preparedPolicyProvider, error) {
	const path = "providers.[entry]"
	if err := ctx.Err(); err != nil {
		return preparedPolicyProvider{}, err
	}
	id, err := ProviderResourceID(input.SubscriptionID, input.Generation, kind, name)
	if err != nil {
		return preparedPolicyProvider{}, err
	}
	result := preparedPolicyProvider{name: name, kind: kind, spec: ProviderSpec{
		SubscriptionID: input.SubscriptionID, Generation: input.Generation,
		Kind: kind, Name: name, ResourceID: id, Format: "yaml", MaxBytes: maxDocumentBytes,
	}}
	typ, _ := definition.get("type")
	if behavior, present := definition.get("behavior"); present {
		result.spec.Behavior = behavior.text
	}
	if kind == "rule" && typ.text != "inline" {
		if format, _ := definition.get("format"); format.text == "text" {
			result.spec.Format = "text"
		}
	}
	resourceKey := id
	if typ.text == "file" {
		source, _ := definition.get("path")
		result.spec.SourceResourceID, resourceKey = source.text, source.text
	}
	if typ.text == "http" {
		endpoint, _ := definition.get("url")
		result.spec.URL = endpoint.text
		result.spec.Interval = time.Hour
		if interval, present := definition.get("interval"); present {
			result.spec.Interval = time.Duration(interval.integer) * time.Second
		}
		if limit, _ := definition.get("size-limit"); limit.integer > 0 && limit.integer < result.spec.MaxBytes {
			result.spec.MaxBytes = limit.integer
		}
		if headers, present := definition.get("header"); present {
			result.spec.Header = make(map[string][]string, len(headers.fields))
			for _, header := range headers.fields {
				values := make([]string, len(header.value.items))
				for i, item := range header.value.items {
					values[i] = item.text
				}
				result.spec.Header[header.name] = values
			}
		}
	}
	data, supplied := input.Resources[resourceKey]
	if supplied {
		if int64(len(data)) > result.spec.MaxBytes {
			return preparedPolicyProvider{}, policyFailure(path + ".resource")
		}
		if kind == "proxy" {
			result.payload, err = decodeProxyProviderBytes(ctx, definition, data, path+".resource")
		} else {
			result.payload, err = decodeRuleProviderBytes(ctx, definition, data, path+".resource")
		}
		if err != nil {
			return preparedPolicyProvider{}, err
		}
		result.ready = true
	}
	if typ.text == "inline" {
		// Supplied bytes above are still validated. The source definition remains
		// authoritative for inline providers; existing resources cannot replace it.
		result.payload, _ = definition.get("payload")
		if result.payload.kind == policyNull {
			result.payload = policyValue{kind: policyList}
		}
		if kind == "proxy" && len(result.payload.items) == 0 {
			return preparedPolicyProvider{}, policyFailure(path + ".payload")
		}
		result.ready = true
	}
	if !result.ready {
		if requireComplete {
			return preparedPolicyProvider{}, policyFailure(path + ".resource")
		}
		return result, nil
	}
	if kind == "proxy" {
		document := policyValue{kind: policyObject, fields: []policyMember{{name: "proxies", value: result.payload}}}
		result.spec.Inline, err = yaml.Marshal(document.yamlNode())
		if err != nil {
			return preparedPolicyProvider{}, policyFailure(path + ".resource")
		}
	} else if result.spec.Format == "text" {
		var output strings.Builder
		for _, item := range result.payload.items {
			output.WriteString(item.text)
			output.WriteByte('\n')
		}
		result.spec.Inline = []byte(output.String())
	} else {
		result.spec.Inline = encodeRuleProviderYAML(result.payload)
	}
	if len(result.spec.Inline) > maxDocumentBytes {
		return preparedPolicyProvider{}, policyFailure(path + ".resource")
	}
	// Emit only declared file-provider behavior. Source download/cache fields
	// cannot survive a forgotten deletion when this schema grows.
	result.definition = policyValue{kind: policyObject}
	result.definition.set("type", policyValue{kind: policyString, text: "file"})
	extension := "yaml"
	if result.spec.Format == "text" {
		extension = "txt"
	}
	result.definition.set("path", policyValue{kind: policyString, text: "providers/" + id + "." + extension})
	if kind == "proxy" {
		for _, field := range []string{"filter", "exclude-filter", "exclude-type", "override", "health-check"} {
			if value, present := definition.get(field); present {
				result.definition.set(field, value)
			}
		}
	} else {
		result.definition.set("behavior", policyValue{kind: policyString, text: result.spec.Behavior})
		result.definition.set("format", policyValue{kind: policyString, text: result.spec.Format})
	}
	return result, nil
}
