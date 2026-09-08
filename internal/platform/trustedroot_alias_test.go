package platform

import "testing"

func TestTrustedRoot_DarwinOnlySystemAliases(t *testing.T) {
	for _, name := range []string{"var", "tmp", "etc"} {
		for _, target := range []string{"private/" + name, "/private/" + name} {
			if !trustedDarwinAlias(name, target) {
				t.Fatalf("positive OS alias %s -> %s rejected", name, target)
			}
		}
	}
	for _, pair := range [][2]string{{"Library", "/private/Library"}, {"usr", "/private/usr"}, {"var", "/tmp"}, {"var", "private/../private/var"}, {"data", "/private/var"}} {
		if trustedDarwinAlias(pair[0], pair[1]) {
			t.Fatalf("accepted application/redirected alias %v", pair)
		}
	}
}
