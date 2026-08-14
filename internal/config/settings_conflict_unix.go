//go:build !windows

package config

func isSettingsConflict(error) bool { return false }
