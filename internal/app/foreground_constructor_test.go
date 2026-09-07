package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixBootstrap_ConstructorTrustedExecutable(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "lib", "mihari", "mihari")
	pathCopy := filepath.Join(root, "bin", "mihari")
	layout := platform.ResolvedLayout{Mode: platform.PrivateMode, InstallRoot: filepath.Dir(managed), Data: platform.Paths{Root: filepath.Join(root, "private")}}
	denied := errors.New("untrusted executable ownership")
	candidate := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, executable, runningHash string
		runningErr                    error
		wantErr                       bool
	}{
		{name: "managed", executable: managed, runningHash: candidate},
		{name: "PATH-copy", executable: pathCopy, runningHash: candidate},
		{name: "untrusted-copy", executable: pathCopy, runningErr: denied, wantErr: true},
		{name: "mismatched-copy", executable: pathCopy, runningHash: strings.Repeat("b", 64), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verified := map[string]bool{}
			x, err := newForegroundTransaction(context.Background(), layout, tc.executable, testBootID, func(_ context.Context, path string) (string, error) {
				verified[path] = true
				if path == tc.executable {
					return tc.runningHash, tc.runningErr
				}
				if path != managed {
					t.Fatalf("unexpected verification: %s", path)
				}
				return candidate, nil
			})
			if tc.wantErr {
				if err == nil || x != nil {
					t.Fatalf("unauthorized executable yielded transaction: x=%v err=%v", x, err)
				}
				if tc.runningErr != nil && !errors.Is(err, tc.runningErr) {
					t.Fatalf("trusted verifier error lost: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("trusted installed executable rejected: %v", err)
			}
			if !verified[managed] || !verified[tc.executable] || x.Artifacts.Install != filepath.Dir(managed) || x.Artifacts.CandidateHash != candidate || !x.Private {
				t.Fatalf("child candidate not taken from verified managed installation: %+v checked=%v", x.Artifacts, verified)
			}
		})
	}
}
