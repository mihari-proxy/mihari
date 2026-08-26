//go:build unix

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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

type uidInfo struct {
	os.FileInfo
	uid uint32
}

func (u uidInfo) Sys() any {
	return &syscall.Stat_t{Uid: u.uid}
}

func TestOwnChannelWriteChownsNewDirsAndFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".mihari", "mihari-channel")
	dataRoot := filepath.Dir(path)
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID, origChown, origLstat := lookupUserHome, effectiveUID, chownPath, lstatPath
	t.Cleanup(func() {
		lookupUserHome, effectiveUID, chownPath, lstatPath = origLookup, origUID, origChown, origLstat
	})
	lookupUserHome = func(string) (userHome, error) { return userHome{Home: home, UID: 42, GID: 42}, nil }
	effectiveUID = func() int { return 0 }
	lstatPath = func(name string) (os.FileInfo, error) {
		info, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		return uidInfo{FileInfo: info, uid: 0}, nil
	}
	var got []string
	chownPath = func(name string, uid, gid int) error {
		if uid != 42 || gid != 42 {
			t.Fatalf("uid=%d gid=%d", uid, gid)
		}
		got = append(got, name)
		return nil
	}
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OwnChannelWrite(path, true); err != nil {
		t.Fatal(err)
	}
	if !containsPath(got, dataRoot) || got[len(got)-1] != path {
		t.Fatalf("chowned=%v", got)
	}
}

func TestOwnChannelWriteSkipsExistingDataRoot(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".mihari", "mihari-channel")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID, origChown, origLstat := lookupUserHome, effectiveUID, chownPath, lstatPath
	t.Cleanup(func() {
		lookupUserHome, effectiveUID, chownPath, lstatPath = origLookup, origUID, origChown, origLstat
	})
	lookupUserHome = func(string) (userHome, error) { return userHome{Home: home, UID: 42, GID: 42}, nil }
	effectiveUID = func() int { return 0 }
	lstatPath = func(name string) (os.FileInfo, error) {
		info, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		return uidInfo{FileInfo: info, uid: 0}, nil
	}
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
	if err := OwnChannelWrite(path, false); err != nil {
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

func TestOwnChannelWriteDoesNotChownAboveDataRoot(t *testing.T) {
	home := t.TempDir()
	outer := t.TempDir()
	dataRoot := filepath.Join(outer, "data")
	path := filepath.Join(dataRoot, "mihari-channel")
	t.Setenv("SUDO_USER", "alice")
	origLookup, origUID, origChown, origLstat := lookupUserHome, effectiveUID, chownPath, lstatPath
	t.Cleanup(func() {
		lookupUserHome, effectiveUID, chownPath, lstatPath = origLookup, origUID, origChown, origLstat
	})
	lookupUserHome = func(string) (userHome, error) { return userHome{Home: home, UID: 42, GID: 42}, nil }
	effectiveUID = func() int { return 0 }
	lstatPath = func(name string) (os.FileInfo, error) {
		info, err := os.Lstat(name)
		if err != nil {
			return nil, err
		}
		return uidInfo{FileInfo: info, uid: 0}, nil
	}
	var got []string
	chownPath = func(name string, uid, gid int) error {
		got = append(got, name)
		return nil
	}
	if err := os.MkdirAll(dataRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OwnChannelWrite(path, true); err != nil {
		t.Fatal(err)
	}
	if containsPath(got, outer) {
		t.Fatalf("chowned ancestor outside data root: %v", got)
	}
	if !containsPath(got, dataRoot) || !containsPath(got, path) {
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
