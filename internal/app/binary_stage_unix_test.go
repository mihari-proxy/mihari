//go:build linux || darwin

package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestNativeBinaryStageCleanup_CrashAndIdentityMismatch(t *testing.T) {
	for _, scenario := range []string{"verified", "published", "changed-bytes", "changed-inode", "missing-marker", "foreign-entry", "changed-stage"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, root := nativeInstallFixture(t)
			for _, name := range []string{"mihari", "update.lock"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			parent, err := platform.OpenTrustedRoot(ctx, root, platform.RootPolicy{Owner: 0, Mode: 0700})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := parent.Close(); err != nil {
					t.Error(err)
				}
			})
			name := ".mihari-update-" + testTxnID
			stage, err := parent.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.WriteFile(ctx, "candidate", []byte("candidate"), 0755, nil); err != nil {
				t.Fatal(err)
			}
			file, candidateID, err := stage.OpenFile(ctx, "candidate", 0755)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			_, stageID, _, _, err := stage.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			marker, err := json.Marshal(binaryStageMarker{Schema: binaryStageSchema, StageIdentity: stageID, CandidateIdentity: candidateID.Key(), SHA256: sha256Hex("candidate")})
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "missing-marker" {
				if err := stage.WriteFile(ctx, "identity.json", marker, 0600, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, name)
			switch scenario {
			case "published":
				err = os.Remove(filepath.Join(path, "candidate"))
			case "changed-bytes":
				err = os.WriteFile(filepath.Join(path, "candidate"), []byte("changed"), 0755)
			case "changed-inode":
				err = os.Rename(filepath.Join(path, "candidate"), filepath.Join(root, "held-original"))
				if err == nil {
					err = os.WriteFile(filepath.Join(path, "candidate"), []byte("candidate"), 0755)
				}
			case "foreign-entry":
				err = os.WriteFile(filepath.Join(path, "foreign"), []byte("retain"), 0600)
			case "changed-stage":
				err = os.Rename(path, path+"-held")
				if err == nil {
					err = os.Mkdir(path, 0700)
				}
				if err == nil {
					err = os.WriteFile(filepath.Join(path, "identity.json"), marker, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanupUnixBinaryStages(ctx, parent); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(path)
			wantGone := scenario == "verified" || scenario == "published"
			if errors.Is(err, os.ErrNotExist) != wantGone {
				t.Fatalf("stage removed=%v, want %v (err=%v)", errors.Is(err, os.ErrNotExist), wantGone, err)
			}
			for _, name := range []string{"mihari", "update.lock"} {
				raw, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(raw) != name {
					t.Fatalf("unrelated %s changed: %q, %v", name, raw, err)
				}
			}
		})
	}
}
