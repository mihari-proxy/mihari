package sysproxy

import (
	"context"
	"fmt"
)

// ObservationScope binds a display observation to the current selected hive.
func (PlatformBackend) ObservationScope() (string, error) {
	root, path, err := resolveInternetSettingsTarget()
	return fmt.Sprintf("%d:%s", root, path), err
}

// GetContext observes native WinINET state unless already canceled.
func (b PlatformBackend) GetContext(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	return b.Get()
}
