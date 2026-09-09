//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

type replacementTrustBackend struct {
	nativeTrustedBackend
	node   trustedNode
	aclErr error
}

func (b replacementTrustBackend) stat(int) (trustedNode, error)    { return b.node, nil }
func (b replacementTrustBackend) checkACL(int, bool, uint32) error { return b.aclErr }

func TestReplacementFile_UnixExecutionTrust(t *testing.T) {
	for _, tc := range []struct {
		name             string
		uid, owner, mode uint32
		aclErr           error
		want             bool
	}{
		{"root owned", 0, 0, 0755, nil, true},
		{"root cannot execute user file", 0, 1000, 0755, nil, false},
		{"same user", 1000, 1000, 0755, nil, true},
		{"writable file", 0, 0, 0775, nil, false},
		{"setuid file", 1000, 0, 04755, nil, false},
		{"untrusted ACL", 0, 0, 0755, os.ErrPermission, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := trustedNode{uid: tc.owner, mode: unix.S_IFREG | tc.mode}
			parent := &ReadOnlySource{backend: replacementTrustBackend{node: trustedNode{mode: unix.S_IFDIR | 0755}, aclErr: tc.aclErr}, chain: []trustedLink{{}}}
			if got := replacementUnixExecutionTrust(parent, 1, node, tc.uid); got != tc.want {
				t.Fatalf("trust=%v want %v", got, tc.want)
			}
		})
	}
	parent := &ReadOnlySource{backend: replacementTrustBackend{node: trustedNode{uid: 1000, mode: unix.S_IFDIR | 0755}}, chain: []trustedLink{{}}}
	if replacementUnixExecutionTrust(parent, 1, trustedNode{mode: unix.S_IFREG | 0755}, 0) {
		t.Fatal("root trusted user-owned parent")
	}
}

func TestReplacementFile_ParentReplacementAndLinks(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "link"}[link], func(t *testing.T) {
			root := t.TempDir()
			parent := filepath.Join(root, "parent")
			path := filepath.Join(parent, "mihari")
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("binary"), 0700); err != nil {
				t.Fatal(err)
			}
			f, _, _, verify, closeParent, err := openReplacementFile(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := errors.Join(f.Close(), closeParent()); err != nil {
					t.Error(err)
				}
			})
			if err := os.Rename(parent, parent+".old"); err != nil {
				t.Fatal(err)
			}
			if link {
				err = os.Symlink(parent+".old", parent)
			} else {
				err = os.Mkdir(parent, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := verify(); err == nil {
				t.Fatal("changed parent accepted")
			}
		})
	}
}

func TestReplacementFile_UnreadableIsNotFresh(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 files")
	}
	path := filepath.Join(t.TempDir(), "unreadable")
	if err := os.WriteFile(path, []byte("binary"), 0000); err != nil {
		t.Fatal(err)
	}
	got, err := ObserveReplacementFile(context.Background(), path)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("read denied became fresh observation: %+v %v", got, err)
	}
}
