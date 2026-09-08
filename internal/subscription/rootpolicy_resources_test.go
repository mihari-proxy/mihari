package subscription

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestRootPolicy_ProviderResourceIdentity(t *testing.T) {
	const sid = "00000000000000000000000000000001"
	first, err := ProviderResourceID(sid, 1, "proxy", "../../private-name")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(first)
	if err != nil || len(decoded) != 32 {
		t.Fatal("provider resource is not a SHA256 identity")
	}
	for _, identity := range []struct {
		sid        string
		generation uint64
		kind, name string
	}{
		{sid, 2, "proxy", "../../private-name"},
		{sid, 1, "rule", "../../private-name"},
		{"00000000000000000000000000000002", 1, "proxy", "../../private-name"},
		{sid, 1, "proxy", "../../private-name/"},
		{sid, 1, "proxy", "[\"proxy\",\"../../private-name\"]"},
	} {
		other, err := ProviderResourceID(identity.sid, identity.generation, identity.kind, identity.name)
		if err != nil {
			t.Fatal(err)
		}
		if first == other {
			t.Fatal("distinct provider identities alias")
		}
	}
	second, err := ProviderResourceID(sid, 1, "proxy", "../../private-name")
	if err != nil || first != second {
		t.Fatal("provider identity is not stable")
	}
}

func TestRootPolicy_ProviderResourceIdentityRejectsInvalidIdentity(t *testing.T) {
	const sid = "00000000000000000000000000000001"
	for _, test := range []struct {
		label, sid        string
		generation        uint64
		kind, name, field string
	}{
		{"subscription", "secret-value", 1, "proxy", "provider", "subscription-id"},
		{"generation", sid, 0, "proxy", "provider", "generation"},
		{"kind", sid, 1, "secret-value", "provider", "providers.[name].kind"},
		{"empty", sid, 1, "proxy", "", "providers.[name]"},
		{"nul", sid, 1, "proxy", "secret-value\x00", "providers.[name]"},
		{"invalid-utf8", sid, 1, "proxy", "secret-value\xff", "providers.[name]"},
	} {
		t.Run(test.label, func(t *testing.T) {
			_, err := ProviderResourceID(test.sid, test.generation, test.kind, test.name)
			var failure PolicyError
			if !errors.As(err, &failure) || failure.Field != test.field || failure.Code != protocol.CodeDataFailure {
				t.Fatalf("wrong failure: %v", err)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("sensitive identity leaked")
			}
		})
	}
}
