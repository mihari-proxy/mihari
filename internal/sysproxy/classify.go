package sysproxy

// IsOwned reports whether observed system proxy is enabled and matches target.
// target is the expected server string (e.g. from NormalizeServer).
func IsOwned(observed State, target string) bool {
	if !observed.Enabled {
		return false
	}
	return observed.Server == target
}

// IsForeign reports whether observed system proxy is enabled to a non-empty
// server that is not owned by the given target.
func IsForeign(observed State, target string) bool {
	if !observed.Enabled {
		return false
	}
	if observed.Server == "" {
		return false
	}
	return !IsOwned(observed, target)
}
