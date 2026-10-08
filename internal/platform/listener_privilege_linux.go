//go:build linux

package platform

// ElevationRevealsListener reports that root can name a listener an ordinary user cannot see.
func ElevationRevealsListener() bool { return true }
