package tundetect

import "context"

// PlatformBackend adapts the package-level detect function to Backend.
type PlatformBackend struct{}

// Detect returns the system observation from the platform backend.
func (PlatformBackend) Detect(ctx context.Context) (Detection, error) { return detect(ctx) }

// Platform returns the default OS-backed Backend.
func Platform() Backend { return PlatformBackend{} }
