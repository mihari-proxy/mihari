package geoip

import (
	"errors"
	"net"
	"os"
)

// downloadFailure retains an internal cause separately from controlled copy.
type downloadFailure struct {
	message string
	cause   error
}

// Error exposes only the controlled description, not the underlying download cause.
func (e downloadFailure) Error() string { return e.message }

// Unwrap preserves the internal cause for classification and controlled diagnostics.
func (e downloadFailure) Unwrap() error { return e.cause }

// SafeFailureReason reports only classified, non-sensitive update causes.
// Unknown errors deliberately return no inferred explanation.
func SafeFailureReason(err error) string {
	var failure downloadFailure
	if errors.As(err, &failure) {
		return failure.message
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission denied while accessing local database files"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "DNS lookup failed for the download source"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "download request timed out"
		}
		return "network request to the download source failed"
	}
	return ""
}
