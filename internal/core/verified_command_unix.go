//go:build linux || darwin

package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var generatedConfigName = regexp.MustCompile(`^config-[0-9a-f]{32}\.yaml$`)

// BindGeneratedConfig borrows D and owns a held read descriptor and its parent.
// expectedSHA256 must be computed by Manager from fresh generated configuration bytes.
func BindGeneratedConfig(ctx context.Context, data *platform.TrustedRoot, relative string, expectedSHA256 [32]byte) (*ConfigCapability, error) {
	root, _, owner, mode, e := data.Snapshot(ctx)
	if e != nil {
		return nil, e
	}
	if owner != 0 || mode != 0700 {
		return nil, os.ErrPermission
	}
	committed := relative == "runtime/config.yaml"
	if !committed && (!strings.HasPrefix(relative, "staging/") || !generatedConfigName.MatchString(filepath.Base(relative))) {
		return nil, os.ErrPermission
	}
	parent, name, e := openCoreParent(ctx, data, relative, false)
	if e != nil {
		return nil, e
	}
	file, id, e := parent.OpenFile(ctx, name, 0600)
	if e != nil {
		return nil, errors.Join(e, parent.Close())
	}
	b, e := readCoreFile(ctx, file, 32<<20)
	if e == nil && sha256.Sum256(b) != expectedSHA256 {
		e = dataFailure("generated configuration hash mismatch")
	}
	if e != nil {
		return nil, errors.Join(e, file.Close(), parent.Close())
	}
	return &ConfigCapability{root: root, hash: expectedSHA256, committed: committed, file: &unixVerifiedFile{parent: parent, file: file, id: id, name: name, path: filepath.Join(root, filepath.FromSlash(relative)), mode: 0600, hash: digest(b)}}, nil
}
