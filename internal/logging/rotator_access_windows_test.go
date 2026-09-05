//go:build windows

package logging

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRotator_OpenRepairsRetainedArchiveACL(t *testing.T) {
	fs, paths := openTestLogFS(t)
	archive := paths.TUILog + ".1"
	mustWriteFile(t, fs, archive, []byte("retained\n"))
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(archive, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	writer, err := OpenRotatingWriter(context.Background(), RotatorOptions{BasePath: paths.TUILog, Config: DefaultConfig(), PrivateFS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(archive)
	if err != nil || string(got) != "retained\n" {
		t.Fatalf("archive changed: %q %v", got, err)
	}
	actual, err := windows.GetNamedSecurityInfo(archive, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err = actual.DACL()
	if err != nil {
		t.Fatal(err)
	}
	admin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(admin) {
			t.Fatal("retained archive kept legacy administrator ACL")
		}
	}
}

func TestRotator_TUIStartupRepairsAllExistingLogSequences(t *testing.T) {
	fs, paths := openTestLogFS(t)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	initial := make(map[string]string)
	for _, base := range []string{paths.DaemonLog, paths.TUILog, paths.MihomoLog} {
		for _, suffix := range []string{"", ".1", ".2", ".lock"} {
			path := base + suffix
			mustWriteFile(t, fs, path, []byte("keep\n"))
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
				t.Fatal(err)
			}
			before, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if e != nil {
				t.Fatal(e)
			}
			initial[path] = before.String()
			files = append(files, path)
		}
	}
	// Unrelated files and noncanonical suffixes are outside migration's scope.
	other := filepath.Join(paths.LogDir, "notes.txt")
	mustWriteFile(t, fs, other, []byte("untouched"))
	if err := windows.SetNamedSecurityInfo(other, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	otherBefore, e := windows.GetNamedSecurityInfo(other, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	writer, err := OpenRotatingWriter(context.Background(), RotatorOptions{BasePath: paths.TUILog, Config: DefaultConfig(), PrivateFS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		actual, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		if actual.String() == initial[path] {
			t.Fatalf("startup missed legacy ACL on %s", filepath.Base(path))
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "keep\n" {
			t.Fatalf("changed content: %s %q %v", filepath.Base(path), got, err)
		}
	}
	actual, err := windows.GetNamedSecurityInfo(other, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if actual.String() != otherBefore.String() {
		t.Fatal("migration changed unrelated ACL")
	}
}
