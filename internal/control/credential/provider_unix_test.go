//go:build linux || darwin

package credential

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestProvider_ExactCredentialGrammar(t *testing.T) {
	token := strings.Repeat("aB", 32)
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"hex", token, true}, {"LF", token + "\n", true}, {"CRLF", token + "\r\n", false}, {"CR", token + "\r", false}, {"space", token + " ", false}, {"leadingLF", "\n" + token, false}, {"secondLF", token + "\n\n", false}, {"short", token[:63], false}, {"long", token + "a", false}, {"nonhex", strings.Repeat("z", 64), false}, {"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCredential([]byte(tc.raw))
			if tc.valid {
				if err != nil || got != token {
					t.Fatal("valid token rejected")
				}
				return
			}
			var api protocol.APIError
			if got != "" || !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
				t.Fatal("malformed credential accepted or misclassified")
			}
		})
	}
}

type ownedCredentialFixture struct {
	raw                []byte
	readErr, createErr error
	creates            int
}

func (f *ownedCredentialFixture) Read(context.Context) ([]byte, error) {
	return append([]byte(nil), f.raw...), f.readErr
}
func (f *ownedCredentialFixture) Create(_ context.Context, raw []byte) error {
	f.creates++
	f.raw = append([]byte(nil), raw...)
	f.readErr = nil
	return f.createErr
}

func TestOwnedCredential_CreatesOnlyWhenMissing(t *testing.T) {
	ctx := context.Background()
	f := &ownedCredentialFixture{readErr: os.ErrNotExist}
	first, err := loadOrCreateOwned(ctx, f)
	if err != nil || len(first) != 64 || f.creates != 1 {
		t.Fatalf("missing credential was not created: %v creates=%d", err, f.creates)
	}
	second, err := loadOrCreateOwned(ctx, f)
	if err != nil || first != second || f.creates != 1 {
		t.Fatal("existing credential was replaced")
	}
	if _, err := parseCredential(f.raw); err != nil {
		t.Fatal("created credential has invalid grammar")
	}
	// Stop/delete/start is represented at the storage boundary; no online rotation.
	f.raw = nil
	f.readErr = os.ErrNotExist
	third, err := loadOrCreateOwned(ctx, f)
	if err != nil || third == first || f.creates != 2 {
		t.Fatal("stopped instance failed to regenerate deleted credential")
	}
}

func TestOwnedCredential_PreservesCorruptionAndPublicationErrors(t *testing.T) {
	for _, f := range []*ownedCredentialFixture{{raw: []byte("corrupt")}, {readErr: os.ErrPermission}, {readErr: io.ErrUnexpectedEOF}} {
		if token, err := loadOrCreateOwned(context.Background(), f); err == nil || token != "" || f.creates != 0 {
			t.Fatal("unsafe existing credential was replaced")
		}
	}
	f := &ownedCredentialFixture{readErr: os.ErrNotExist, createErr: io.ErrShortWrite}
	if token, err := loadOrCreateOwned(context.Background(), f); token != "" || !errors.Is(err, io.ErrShortWrite) || f.creates != 1 {
		t.Fatal("publication failure was hidden or retried")
	}
}

func TestProvider_InvalidDiscoveryIsReadOnly(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "absent")
	p := NewProvider(platform.ControlLocator{Mode: platform.PrivateMode, BaseDir: missing, Credential: "relative", Endpoint: "relative", ExpectedOwner: uint32(os.Geteuid())})
	if value, err := p.Load(context.Background()); value != "" || err == nil {
		t.Fatal("invalid discovery accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("read-only client created data")
	}
}
