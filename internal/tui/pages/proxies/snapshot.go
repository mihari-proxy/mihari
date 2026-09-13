package proxies

import (
	"errors"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// ObserveSnapshot keeps the last complete snapshot when a refresh fails.
func (m *Model) ObserveSnapshot(groups protocol.ProxyGroups, observed time.Time, err error) {
	if err != nil {
		m.loadError = "Proxy data is unavailable"
		var api protocol.APIError
		if errors.As(err, &api) {
			m.loadError = ui.DisplayProxyName(api.Message)
		}
		m.ensureFocusVisible()
		return
	}
	if observed.IsZero() {
		observed = time.Now()
	}
	m.lastSuccess = observed
	m.SetGroups(groups)
}
