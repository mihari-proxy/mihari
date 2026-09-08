package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type providerIdentity struct {
	Schema         string `json:"schema"`
	SubscriptionID string `json:"subscription_id"`
	Generation     uint64 `json:"generation"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
}

// ProviderResourceID returns the versioned identity hash used for every provider
// source kind. Names remain data and never become filesystem components.
func ProviderResourceID(subscriptionID string, generation uint64, kind, name string) (string, error) {
	if !profileIDPattern.MatchString(subscriptionID) {
		return "", PolicyError{Field: "subscription-id", Code: protocol.CodeDataFailure}
	}
	if generation == 0 {
		return "", PolicyError{Field: "generation", Code: protocol.CodeDataFailure}
	}
	if kind != "proxy" && kind != "rule" {
		return "", PolicyError{Field: "providers.[name].kind", Code: protocol.CodeDataFailure}
	}
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return "", PolicyError{Field: "providers.[name]", Code: protocol.CodeDataFailure}
	}
	encoded, err := json.Marshal(providerIdentity{
		Schema: "mihari.provider-identity/v1", SubscriptionID: subscriptionID,
		Generation: generation, Kind: kind, Name: name,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
