//go:build linux || darwin

package credential

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

// Provider reads the current Unix credential through verified read-only discovery.
type Provider struct{ locator platform.ControlLocator }

// NewProvider does not open directories or create credentials.
func NewProvider(locator platform.ControlLocator) *Provider { return &Provider{locator: locator} }

// Load reads once, closes its complete discovery proof, and parses exact token bytes.
func (p *Provider) Load(ctx context.Context) (string, error) {
	raw, err := platform.ReadControlCredential(ctx, p.locator)
	if err != nil {
		return "", err
	}
	return parseCredential(raw)
}

func parseCredential(raw []byte) (string, error) {
	if len(raw) == 65 && raw[64] == '\n' {
		raw = raw[:64]
	}
	if len(raw) != 64 {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control credential"}
	}
	token := string(raw)
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid control credential"}
	}
	return token, nil
}

// LoadOrCreateOwned is the daemon-only credential bootstrap. The outer owner
// must retain its daemon lease through listener and worker shutdown.
func LoadOrCreateOwned(ctx context.Context, layout platform.ResolvedLayout, lease *platform.OwnedDaemonLease) (string, error) {
	var token string
	err := lease.Borrow().WithCredential(ctx, layout, func(c *platform.ControlCredentialFile) error {
		var err error
		token, err = loadOrCreateOwned(ctx, c)
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

type ownedCredentialFile interface {
	Read(context.Context) ([]byte, error)
	Create(context.Context, []byte) error
}

func loadOrCreateOwned(ctx context.Context, c ownedCredentialFile) (string, error) {
	raw, err := c.Read(ctx)
	if err == nil {
		return parseCredential(raw)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(secret[:])
	if err = c.Create(ctx, []byte(token+"\n")); err != nil {
		return "", err
	}
	return token, nil
}
