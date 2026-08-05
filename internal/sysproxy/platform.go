package sysproxy

// PlatformBackend adapts package-level Get/Enable/Disable to Backend.
type PlatformBackend struct{}

// Get returns the current system proxy state from the platform backend.
func (PlatformBackend) Get() (State, error) { return Get() }

// Enable turns on the system proxy for host:port.
func (PlatformBackend) Enable(host string, port int) error { return Enable(host, port) }

// Disable turns off the system proxy via the platform backend.
func (PlatformBackend) Disable() error { return Disable() }

// Platform returns the default OS-backed Backend.
func Platform() Backend { return PlatformBackend{} }
