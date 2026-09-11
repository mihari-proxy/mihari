package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/platform"
	"path/filepath"
)

// newForegroundTransaction verifies the running executable before constructing
// the bootstrap transaction. Native callers supply the no-follow root verifier.
func newForegroundTransaction(ctx context.Context, layout platform.ResolvedLayout, executable, boot string, verify func(context.Context, string) (string, error)) (*InstallTransaction, error) {
	hash, err := verify(ctx, executable)
	if err != nil {
		return nil, err
	}
	managed := filepath.Join(layout.InstallRoot, "mihari")
	if filepath.Clean(executable) != managed {
		candidate, err := verify(ctx, managed)
		if err != nil {
			return nil, err
		}
		if hash != candidate {
			return nil, errValidationHandshake
		}
	}
	return &InstallTransaction{Private: layout.Mode == platform.PrivateMode, Artifacts: InstallArtifacts{DataAction: InstallDataCreate, Target: layout.Data.Root, DataRoot: layout.Data.Root, Install: layout.InstallRoot, Endpoint: layout.ControlEndpoint, Credential: layout.CredentialPath, CandidateHash: hash, BootID: boot}}, nil
}
