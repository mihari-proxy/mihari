//go:build unix

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishWorkspace_DifferentUIDAcquisitionSubstitution(t *testing.T) {
	if os.Getenv("MIHARI_ACQUIRE_ATTACK_CHILD") == "1" {
		if os.Geteuid() != 4242 {
			t.Fatal("child did not change UID")
		}
		parent, name := os.Getenv("MIHARI_ACQUIRE_PARENT"), os.Getenv("MIHARI_ACQUIRE_NAME")
		if _, err := os.ReadFile(filepath.Join(parent, "victim", "unrelated")); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("attacker can access private victim contents: %v", err)
		}
		if err := os.Rename(filepath.Join(parent, name), filepath.Join(parent, "created-moved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(parent, "victim"), filepath.Join(parent, name)); err != nil {
			t.Fatal(err)
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root solely to spawn isolated UID 4242 attacker")
	}
	parent := t.TempDir()
	if err := os.Chmod(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(parent, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(victim, "unrelated"), []byte("private victim data"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	original := publishUnixWorkspaceOpenFn
	defer func() { publishUnixWorkspaceOpenFn = original }()
	var substituted string
	publishUnixWorkspaceOpenFn = func(fd int, name string, flags int, perm uint32) (int, error) {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(executable, "-test.run=^TestPublishWorkspace_DifferentUIDAcquisitionSubstitution$", "-test.v=true")
		cmd.Env = append(os.Environ(), "MIHARI_ACQUIRE_ATTACK_CHILD=1", "MIHARI_ACQUIRE_PARENT="+parent, "MIHARI_ACQUIRE_NAME="+name)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 4242, Gid: 4242, Groups: []uint32{}}}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("distinct UID could not substitute victim: %v %s", err, output)
		}
		substituted = filepath.Join(parent, name)
		return original(fd, name, flags, perm)
	}
	w, err := d.CreateWorkspace()
	if w != nil {
		_ = w.Close()
	}
	if err == nil {
		t.Fatal("adopted different-UID substitution")
	}
	if body, err := os.ReadFile(filepath.Join(substituted, "unrelated")); err != nil || string(body) != "private victim data" {
		t.Fatalf("victim data changed: %q %v", body, err)
	}
	if st, err := os.Stat(substituted); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("victim permissions changed: %v %v", st, err)
	}
}

func TestPublishWorkspace_AcquisitionRejectsUnprovedPrivateBoundary(t *testing.T) {
	for _, scenario := range []string{"wide-mode", "untrusted-owner", "acl", "acl-error"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "untrusted-owner" && os.Geteuid() != 0 {
				t.Skip("requires isolated chown")
			}
			d, err := OpenPublishDir(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			original, acl := publishUnixWorkspaceOpenFn, publishUnixACLBoundaryFn
			defer func() { publishUnixWorkspaceOpenFn, publishUnixACLBoundaryFn = original, acl }()
			publishUnixWorkspaceOpenFn = func(fd int, name string, flags int, perm uint32) (int, error) {
				opened, err := original(fd, name, flags, perm)
				if err != nil {
					return opened, err
				}
				switch scenario {
				case "wide-mode":
					if err := unix.Fchmod(opened, 0o770); err != nil {
						t.Fatal(err)
					}
				case "untrusted-owner":
					if err := unix.Fchown(opened, 4242, 4242); err != nil {
						t.Fatal(err)
					}
				case "acl":
					publishUnixACLBoundaryFn = func(int) (bool, error) { return false, nil }
				case "acl-error":
					publishUnixACLBoundaryFn = func(int) (bool, error) { return false, unix.EIO }
				}
				return opened, nil
			}
			w, err := d.CreateWorkspace()
			if w != nil {
				_ = w.Close()
			}
			if err == nil {
				t.Fatal("unproved private directory adopted")
			}
		})
	}
}

func TestPublishWorkspace_AcquisitionRejectsSubstitutedDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0o700, 0o750} {
		t.Run(mode.String(), func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0o777); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(parent, "victim")
			if err := os.Mkdir(victim, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(victim, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(victim, "unrelated"), []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			d, err := OpenPublishDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			original := publishUnixWorkspaceOpenFn
			defer func() { publishUnixWorkspaceOpenFn = original }()
			var substituted string
			publishUnixWorkspaceOpenFn = func(fd int, name string, flags int, perm uint32) (int, error) {
				if err := unix.Renameat(fd, name, fd, "original"); err != nil {
					t.Fatal(err)
				}
				if err := unix.Renameat(fd, "victim", fd, name); err != nil {
					t.Fatal(err)
				}
				substituted = filepath.Join(parent, name)
				return original(fd, name, flags, perm)
			}
			w, err := d.CreateWorkspace()
			if w != nil {
				_ = w.Close()
			}
			if err == nil {
				t.Error("adopted substituted nonempty directory")
			}
			content, readErr := os.ReadFile(filepath.Join(substituted, "unrelated"))
			if readErr != nil || string(content) != "keep me" {
				t.Errorf("unrelated contents changed: %q %v", content, readErr)
			}
			st, err := os.Stat(substituted)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != mode {
				t.Errorf("victim mode changed to %v", st.Mode())
			}
		})
	}
}

func TestPublishWorkspace_CleanupOnlyRemovesCreatedNames(t *testing.T) {
	parent := t.TempDir()
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := w.CreateTemp("owned-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, w.name, "unowned")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Error("expected incomplete cleanup warning")
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "keep" {
		t.Errorf("unowned file removed: %q %v", content, err)
	}
}
