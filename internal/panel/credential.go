package panel

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// LoadCredential reads the Web access credential from path.
// This token authenticates browsers to the Web gateway and must never be the controller secret.
func LoadCredential(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", protocol.APIError{
			Code:    protocol.CodeDataFailure,
			Message: "invalid web credential",
		}
	}
	return token, nil
}

// LoadOrCreateCredential returns the existing Web credential or generates one once with 0600 permissions.
func LoadOrCreateCredential(path string) (string, error) {
	token, err := LoadCredential(path)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}

	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	token = hex.EncodeToString(secret[:])
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return LoadCredential(path)
	}
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return token, nil
}
