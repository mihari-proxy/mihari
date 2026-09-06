package subscription

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

func decodeProxyProviderBytes(ctx context.Context, definition policyValue, data []byte, path string) (policyValue, error) {
	if definition.provider == nil {
		return policyValue{}, policyFailure(path)
	}
	payload := listSchema(providerPayloadProxySchema(definition.provider.override, definition.provider.dialer))
	payload.check = func(v policyValue) bool { return len(v.items) > 0 }
	schema := objectSchema(map[string]*policySchema{"proxies": payload})
	schema.required = []string{"proxies"}
	document, err := decodePolicyResourceDocument(ctx, data, schema, path)
	if err != nil {
		return policyValue{}, err
	}
	result, _ := document.get("proxies")
	return result, nil
}

func decodePolicyResourceDocument(ctx context.Context, data []byte, schema *policySchema, path string) (policyValue, error) {
	if err := ctx.Err(); err != nil {
		return policyValue{}, err
	}
	if len(data) == 0 || len(data) > maxDocumentBytes {
		return policyValue{}, policyFailure(path)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document, extra yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return policyValue{}, policyFailure(path)
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return policyValue{}, policyFailure(path)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return policyValue{}, policyFailure(path)
	}
	return decodePolicyValueContext(ctx, document.Content[0], schema, path)
}

func decodeRuleProviderBytes(ctx context.Context, definition policyValue, data []byte, path string) (policyValue, error) {
	if err := ctx.Err(); err != nil {
		return policyValue{}, err
	}
	if len(data) > maxDocumentBytes || !utf8.Valid(data) {
		return policyValue{}, policyFailure(path)
	}
	format, _ := definition.get("format")
	behavior, _ := definition.get("behavior")
	payload := policyValue{kind: policyList}
	if format.text == "text" {
		for len(data) > 0 {
			if err := ctx.Err(); err != nil {
				return policyValue{}, err
			}
			line, rest, found := bytes.Cut(data, []byte{'\n'})
			data = rest
			if !found {
				data = nil
			}
			text := strings.TrimSpace(string(line))
			if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "//") {
				continue
			}
			payload.items = append(payload.items, policyValue{kind: policyString, text: text})
		}
	} else {
		schema := objectSchema(map[string]*policySchema{"payload": listSchema(stringSchema()), "rules": listSchema(stringSchema())})
		schema.check = func(v policyValue) bool {
			_, hasPayload := v.get("payload")
			_, hasRules := v.get("rules")
			return hasPayload != hasRules
		}
		document, err := decodePolicyResourceDocument(ctx, data, schema, path)
		if err != nil {
			return policyValue{}, err
		}
		var exists bool
		payload, exists = document.get("payload")
		if !exists {
			payload, _ = document.get("rules")
		}
	}
	return validateRuleProviderPayload(ctx, payload, behavior.text, path+".payload")
}

func encodeRuleProviderYAML(payload policyValue) []byte {
	if len(payload.items) == 0 {
		return []byte("payload: []\n")
	}
	var output strings.Builder
	output.WriteString("payload:\n")
	for _, item := range payload.items {
		// strconv.Quote emits a YAML-compatible double-quoted scalar while also
		// escaping Unicode line separators. No folding or multiline scalars: the
		// fixed core reparses exactly one physical line with the payload header.
		output.WriteString("  - ")
		output.WriteString(strconv.Quote(item.text))
		output.WriteByte('\n')
	}
	return []byte(output.String())
}
