package ui

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// ApplySavedEndpointsMsg requests service application of committed endpoint settings.
type ApplySavedEndpointsMsg struct {
	Page   PageID
	Status protocol.OnboardingStatus
	Retry  bool
}
