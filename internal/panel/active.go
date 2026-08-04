package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/LeeShunEE/mihari/internal/config"
	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

// Active is the atomic pointer to the panel build served by the Web gateway.
type Active struct {
	Panel string `json:"panel"`
	Build string `json:"build"`
}

// LoadActive reads active.json. Missing file yields an empty Active without error.
func LoadActive(path string) (Active, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Active{}, nil
		}
		return Active{}, err
	}
	var active Active
	if err := json.Unmarshal(raw, &active); err != nil {
		return Active{}, protocol.APIError{
			Code:    protocol.CodeDataFailure,
			Message: "invalid panel active pointer",
		}
	}
	return active, nil
}

// SaveActive atomically replaces active.json after validating panel and build identity.
func SaveActive(path string, active Active) error {
	if active.Panel == "" || active.Build == "" {
		return protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "panel active pointer requires panel and build",
		}
	}
	raw, err := json.Marshal(active)
	if err != nil {
		return fmt.Errorf("encode panel active pointer: %w", err)
	}
	return config.AtomicWrite(path, append(raw, '\n'), 0o600)
}
