//go:build !windows

package platform

import "context"

// CleanupReplacedBinary is a no-op: Unix replacement unlinks the old image
// atomically and leaves no named .old-* copies to collect.
func CleanupReplacedBinary(context.Context, string) error { return nil }
