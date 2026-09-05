//go:build unix_security && (linux || darwin)

package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func securityTrustedParent(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("MIHARI_SECURITY_ROOT")
	if parent == "" || !filepath.IsAbs(parent) || os.Geteuid() != 0 {
		t.Fatal("isolated root security runner required")
	}
	return parent
}

func TestTrustedRoot_RejectsOnlyChangedSymlink(t *testing.T) {
	parent := securityTrustedParent(t)
	target := filepath.Join(parent, "rootpolicy-positive")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(target); err != nil {
			t.Error(err)
		}
	})
	policy := RootPolicy{Owner: 0, Mode: 0700}
	root, err := OpenTrustedRoot(context.Background(), target, policy)
	if err != nil {
		t.Fatalf("positive control failed: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "rootpolicy-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil {
			t.Error(err)
		}
	})
	got, err := OpenTrustedRoot(context.Background(), link, policy)
	if got != nil {
		got.Close()
		t.Fatal("accepted application symlink")
	}
	if !errors.Is(err, ErrUnsafeComponent) {
		t.Fatalf("wrong rejection stage: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("target modified")
	}
}

func TestTrustedRoot_TwoUIDsPublicReadPrivateDenied(t *testing.T) {
	parent := securityTrustedParent(t)
	target := filepath.Join(parent, "rootpolicy-two-uids")
	r, err := OpenTrustedRoot(context.Background(), target, RootPolicy{Mode: 0711, AllowCreate: true})
	if err != nil {
		t.Fatalf("positive root: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
		for _, name := range []string{"public", "private"} {
			if err := os.Remove(filepath.Join(target, name)); err != nil {
				t.Error(err)
			}
		}
		if err := os.Remove(target); err != nil {
			t.Error(err)
		}
	})
	if err := r.WriteFile(context.Background(), "public", []byte("public"), 0644, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile(context.Background(), "private", []byte("private"), 0600, nil); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []uint32{4242, 4343} {
		cmd := exec.Command("/bin/sh", "-c", `test -r "$1/public" && ! test -r "$1/private"`, "mihari-permission-check", target)
		cmd.Env = []string{}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: uid, NoSetGroups: true}}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("uid %d read boundary: %v %s", uid, err, out)
		}
	}
}
