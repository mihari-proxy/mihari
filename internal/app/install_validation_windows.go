//go:build windows

package app

// InheritedValidationLease is not available without a Unix pipe extra file.
func InheritedValidationLease() (ValidationLease, error) {
	return nil, errMissingValidationPipe
}
