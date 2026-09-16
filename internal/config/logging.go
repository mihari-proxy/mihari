package config

// CoreLoggingLevel returns the saved file logging level in mihomo's spelling.
func (s Settings) CoreLoggingLevel() string {
	level := s.EffectiveLogging().Level
	if level == "warn" {
		return "warning"
	}
	return level
}

// ActiveLoggingLevel reports whether users may explicitly select this level.
// Silent is only accepted when loading or adopting existing core state.
func ActiveLoggingLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

// ObservedLoggingLevel normalizes a supported mihomo level for persistence.
func ObservedLoggingLevel(level string) (string, bool) {
	if level == "warning" {
		level = "warn"
	}
	return level, level == "silent" || ActiveLoggingLevel(level)
}
