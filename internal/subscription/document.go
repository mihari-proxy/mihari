package subscription

import (
	"bytes"
	"errors"
	"io"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"go.yaml.in/yaml/v3"
)

const maxDocumentBytes = 16 << 20

type Document map[string]any

func ParseDocument(content []byte) (Document, error) {
	if len(content) == 0 || len(content) > maxDocumentBytes {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid subscription document size"}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document Document
	if err := decoder.Decode(&document); err != nil || document == nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid subscription YAML"}, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription must contain one YAML document"}, err)
	}
	if _, proxies := document["proxies"]; !proxies {
		if _, providers := document["proxy-providers"]; !providers {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "document is not a mihomo subscription"}
		}
	}
	if value, exists := document["proxies"]; exists {
		if _, ok := value.([]any); !ok {
			return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription proxies must be a list"}
		}
	}
	return document, nil
}
