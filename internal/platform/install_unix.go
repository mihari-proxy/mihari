//go:build unix

package platform

// defaultInstallRoot is a machine-local, non-download path for the service binary.
// Kept under lib/ so PATH is not polluted; the service ImagePath uses the full path.
func defaultInstallRoot() string {
	return "/usr/local/lib/mihari"
}
