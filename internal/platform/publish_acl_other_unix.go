//go:build unix && !linux && !darwin

package platform

// Unknown filesystem ACL semantics cannot prove namespace safety.
func unixACLHasNoAdditionalAuthority(int) (bool, error) { return false, nil }
