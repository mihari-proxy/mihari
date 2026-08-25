//go:build unix

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelPathUsesSudoUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MIHARI_DATA", "")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID := lookupUserHome, effectiveUID
	t.Cleanup(func() { lookupUserHome, effectiveUID = origLookup, origUID })
	lookupUserHome = func(username string) (userHome, error) {
		if username != "alice" {
			t.Fatalf("username=%q", username)
		}
		return userHome{Home: home, UID: 42, GID: 42}, nil
	}
	effectiveUID = func() int { return 0 }
	got, err := ChannelPath()
	if err != nil || got != filepath.Join(home, ".mihari", "mihari-channel") {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if strings.Contains(got, string(filepath.Separator)+"home"+string(filepath.Separator)+"alice") && home != filepath.Join("/home", "alice") {
		t.Fatal("raw SUDO_USER joined under /home")
	}
	if DefaultDataRoot() == filepath.Join(home, ".mihari") {
		t.Fatal("DefaultDataRoot unexpectedly followed SUDO_USER")
	}
}

func TestChannelPathLookupFailureDoesNotFallbackRoot(t *testing.T) {
	t.Setenv("MIHARI_DATA", "")
	t.Setenv("SUDO_USER", "missing")
	origLookup, origUID := lookupUserHome, effectiveUID
	t.Cleanup(func() { lookupUserHome, effectiveUID = origLookup, origUID })
	lookupUserHome = func(string) (userHome, error) { return userHome{}, errors.New("no such user") }
	effectiveUID = func() int { return 0 }
	if _, err := ChannelPath(); err == nil {
		t.Fatal("expected error")
	}
}

func TestChannelPathRejectsEmptyHome(t *testing.T) {
	t.Setenv("MIHARI_DATA", "")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID := lookupUserHome, effectiveUID
	t.Cleanup(func() { lookupUserHome, effectiveUID = origLookup, origUID })
	lookupUserHome = func(string) (userHome, error) { return userHome{Home: "", UID: 42, GID: 42}, nil }
	effectiveUID = func() int { return 0 }
	if _, err := ChannelPath(); err == nil {
		t.Fatal("expected error")
	}
}

func TestChannelPathRejectsNonAbsoluteHome(t *testing.T) {
	t.Setenv("MIHARI_DATA", "")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID := lookupUserHome, effectiveUID
	t.Cleanup(func() { lookupUserHome, effectiveUID = origLookup, origUID })
	lookupUserHome = func(string) (userHome, error) {
		return userHome{Home: "alice", UID: 42, GID: 42}, nil
	}
	effectiveUID = func() int { return 0 }
	if _, err := ChannelPath(); err == nil {
		t.Fatal("expected error")
	}
}

func TestChannelPathHonorsMihariData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MIHARI_DATA", root)
	t.Setenv("SUDO_USER", "alice")
	origUID := effectiveUID
	t.Cleanup(func() { effectiveUID = origUID })
	effectiveUID = func() int { return 0 }
	got, err := ChannelPath()
	if err != nil || got != filepath.Join(root, "mihari-channel") {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestOwnChannelWriteChownsNewDirsAndFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".mihari", "mihari-channel")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID, origChown := lookupUserHome, effectiveUID, chownPath
	t.Cleanup(func() { lookupUserHome, effectiveUID, chownPath = origLookup, origUID, origChown })
	lookupUserHome = func(string) (userHome, error) { return userHome{Home: home, UID: 42, GID: 42}, nil }
	effectiveUID = func() int { return 0 }
	var got []string
	chownPath = func(name string, uid, gid int) error {
		if uid != 42 || gid != 42 {
			t.Fatalf("uid=%d gid=%d", uid, gid)
		}
		got = append(got, name)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OwnChannelWrite(path); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1] != path {
		t.Fatalf("chowned=%v", got)
	}
}

func TestOwnChannelWriteSkipsExistingDataRoot(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".mihari", "mihari-channel")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID, origChown := lookupUserHome, effectiveUID, chownPath
	t.Cleanup(func() { lookupUserHome, effectiveUID, chownPath = origLookup, origUID, origChown })
	lookupUserHome = func(string) (userHome, error) { return userHome{Home: home, UID: 42, GID: 42}, nil }
	effectiveUID = func() int { return 0 }
	var got []string
	chownPath = func(name string, uid, gid int) error {
		got = append(got, name)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OwnChannelWrite(path); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(home, ".mihari")
	if containsPath(got, dataRoot) {
		t.Fatalf("existing data root chowned: %v", got)
	}
	if len(got) == 0 || got[len(got)-1] != path {
		t.Fatalf("chowned=%v", got)
	}
}

func containsPath(got []string, want string) bool {
	for _, name := range got {
		if name == want {
			return true
		}
	}
	return false
}
