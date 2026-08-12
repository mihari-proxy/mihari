package protocol

// ServiceStatus reports the OS service registration state surfaced on the
// onboarding review step (design §5.3). Status mirrors service.StatusKind:
// running/stopped/unknown/not_installed. Advisory only — failures resolve to
// "unknown" and never block onboarding.
type ServiceStatus struct {
	Schema string `json:"schema"`
	Status string `json:"status"`
}
