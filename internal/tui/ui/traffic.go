package ui

// FormatSubscriptionTraffic renders used/total quota when known.
// Returns empty string when the provider did not publish usage.
// used = upload+download (bytes).
func FormatSubscriptionTraffic(upload, download, total int64) string {
	used := upload + download
	if used < 0 {
		used = 0
	}
	if total <= 0 && used <= 0 {
		return ""
	}
	if total <= 0 {
		return FormatBytes(used)
	}
	return FormatBytes(used) + "/" + FormatBytes(total)
}

// FormatSubscriptionLabel is "name" or "name · used/total" for status bar / labels.
func FormatSubscriptionLabel(name string, upload, download, total int64) string {
	if name == "" {
		return FormatSubscriptionTraffic(upload, download, total)
	}
	traffic := FormatSubscriptionTraffic(upload, download, total)
	if traffic == "" {
		return name
	}
	return name + " · " + traffic
}
