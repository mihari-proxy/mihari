//go:build darwin

package platform

// LookupTCPOccupant is unavailable on Darwin without CGO or exec'ing lsof.
func LookupTCPOccupant(string) (TCPOccupant, bool) {
	return TCPOccupant{}, false
}

// LookupWildcardBindOccupant is unavailable on Darwin without CGO or lsof.
func LookupWildcardBindOccupant(string) (TCPOccupant, bool) {
	return TCPOccupant{}, false
}
