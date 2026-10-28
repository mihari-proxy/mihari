package subscription

import (
	"github.com/mihari-proxy/mihari/internal/config"
	"go.yaml.in/yaml/v3"
	"reflect"
	"testing"
)

func TestGenerateProxyAuth_MergesUsersAndReplacesBypass(t *testing.T) {
	s := testSettings()
	if err := yaml.Unmarshal([]byte(`proxy-auth:
  mode: enabled
  users:
    - username: alice
      password: new
    - username: charlie
      password: added
  bypass-mode: custom
  local-bypass: true
  bypass-prefixes: [192.0.2.0/24]
`), &s); err != nil {
		t.Fatal(err)
	}
	base := Document{"authentication": []any{"alice:old", "bob:keep"}, "skip-auth-prefixes": []any{"198.51.100.0/24"}}
	content, err := Generate(base, nil, s)
	if err != nil {
		t.Fatal(err)
	}
	var got Document
	if err := yaml.Unmarshal(content, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["authentication"], []any{"alice:new", "bob:keep", "charlie:added"}) {
		t.Fatalf("wrong effective users: %#v", got["authentication"])
	}
	if !reflect.DeepEqual(got["skip-auth-prefixes"], []any{"127.0.0.0/8", "::1/128", "192.0.2.0/24"}) {
		t.Fatalf("wrong bypass: %#v", got["skip-auth-prefixes"])
	}
	if !reflect.DeepEqual(base["authentication"], []any{"alice:old", "bob:keep"}) {
		t.Fatal("source mutated")
	}
}

func TestGenerateProxyAuth_EnabledWithoutUsersFails(t *testing.T) {
	s := testSettings()
	if err := yaml.Unmarshal([]byte("proxy-auth: {mode: enabled}"), &s); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(Document{}, nil, s); err == nil {
		t.Fatal("enabled authentication without users was accepted")
	}
}

func TestGenerateProxyAuth_FollowDisabledAndRequired(t *testing.T) {
	for _, test := range []struct {
		mode, bypass string
		want         []any
		prefixes     []any
	}{
		{"follow", "follow", []any{"alice:old"}, []any{"192.0.2.0/24"}},
		{"disabled", "required", []any{}, []any{"192.0.2.0/24"}},
		{"enabled", "required", []any{"alice:old", "bob:added"}, []any{}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			s := testSettings()
			s.ProxyAuth = &config.ProxyAuthSettings{Mode: test.mode, BypassMode: test.bypass, Users: []config.ProxyAuthCredential{{Username: "bob", Password: "added"}}}
			raw, err := Generate(Document{"authentication": []any{"alice:old"}, "skip-auth-prefixes": []any{"192.0.2.0/24"}}, nil, s)
			if err != nil {
				t.Fatal(err)
			}
			var got Document
			if err := yaml.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got["authentication"], test.want) || !reflect.DeepEqual(got["skip-auth-prefixes"], test.prefixes) {
				t.Fatalf("unexpected auth=%v bypass=%v", got["authentication"], got["skip-auth-prefixes"])
			}
		})
	}
}

func TestGenerateProxyAuth_FollowPreservesNativeCredentialSemantics(t *testing.T) {
	base := Document{"authentication": []any{"empty-password:", ":empty-user", "colon:pass:with:colons", "ignored-without-colon"}}
	raw, err := Generate(base, nil, testSettings())
	if err != nil {
		t.Fatal(err)
	}
	var got Document
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got["authentication"], base["authentication"]) {
		t.Fatal("follow changed subscription authentication")
	}
}
