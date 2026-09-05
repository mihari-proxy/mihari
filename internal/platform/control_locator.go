package platform

import "fmt"

// ControlLocator identifies the local control endpoint and credential owner.
type ControlLocator struct {
	Mode          LayoutMode
	Endpoint      string
	Credential    string
	ExpectedOwner uint32
}

// Locator derives the Unix control locator for a resolved layout.
func (l ResolvedLayout) Locator(euid uint32) (ControlLocator, error) {
	if l.ControlEndpoint == "" || l.CredentialPath == "" {
		return ControlLocator{}, fmt.Errorf("invalid argument: incomplete control locator")
	}
	expectedOwner := euid
	switch l.Mode {
	case SystemMode:
		expectedOwner = 0
	case PrivateMode:
	case WindowsMode:
		return ControlLocator{}, fmt.Errorf("invalid argument: Windows layout does not use a Unix control locator")
	default:
		return ControlLocator{}, fmt.Errorf("invalid argument: unsupported layout mode %q", l.Mode)
	}
	return ControlLocator{
		Mode:          l.Mode,
		Endpoint:      l.ControlEndpoint,
		Credential:    l.CredentialPath,
		ExpectedOwner: expectedOwner,
	}, nil
}
