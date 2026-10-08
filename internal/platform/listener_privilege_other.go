//go:build !linux

package platform

// ElevationRevealsListener reports that Administrator or root privileges do not name a listener this process could not already see.
func ElevationRevealsListener() bool { return false }
