package platform

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplacementFile_WindowsExecutionTrust(t *testing.T) {
	user, err := windows.StringToSid("S-1-5-21-1-2-3-1000")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sddl                 string
		elevated, volumeRoot, want bool
	}{
		{"admin controlled", "O:BAG:BAD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;GRGX;;;BU)", true, false, true},
		{"unsafe write ACL", "O:BAG:BAD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;GW;;;BU)", true, false, false},
		{"user owned elevated", "O:S-1-5-21-1-2-3-1000G:BAD:(A;;FA;;;S-1-5-21-1-2-3-1000)", true, false, false},
		{"same user", "O:S-1-5-21-1-2-3-1000G:BAD:(A;;FA;;;S-1-5-21-1-2-3-1000)", false, false, true},
		{"trusted installer owner", "O:" + windowsTrustedInstallerSID + "G:SYD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + windowsTrustedInstallerSID + ")(A;;GRGX;;;BU)", true, false, true},
		{"trusted installer owner user write", "O:" + windowsTrustedInstallerSID + "G:SYD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;GW;;;BU)", true, false, false},
		{"volume root users add subdirectory", "O:" + windowsTrustedInstallerSID + "G:SYD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;0x4;;;AU)(A;;GRGX;;;BU)", true, true, true},
		{"non-root users add subdirectory", "O:" + windowsTrustedInstallerSID + "G:SYD:(A;;FA;;;SY)(A;;FA;;;BA)(A;;0x4;;;AU)(A;;GRGX;;;BU)", true, false, false},
		{"volume root users generic write", "O:" + windowsTrustedInstallerSID + "G:SYD:(A;;FA;;;SY)(A;;GW;;;AU)", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if got := replacementWindowsExecutionTrust(sd, tc.elevated, user, tc.volumeRoot); got != tc.want {
				t.Fatalf("trust=%v want %v", got, tc.want)
			}
		})
	}
}

func TestReplacementFile_WindowsCanonicalAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Mihari Long Binary.exe")
	if err := os.WriteFile(path, []byte("candidate"), 0700); err != nil {
		t.Fatal(err)
	}
	original, err := ObserveReplacementFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	assertAlias := func(t *testing.T, alias string) {
		t.Helper()
		observed, err := ObserveReplacementFile(context.Background(), alias)
		if err != nil {
			t.Fatal(err)
		}
		if observed.Path != original.Path || observed.FileID != original.FileID || observed.SHA256 != original.SHA256 {
			t.Fatalf("same directory entry was not canonicalized: original=%+v alias=%+v", original, observed)
		}
	}
	t.Run("case", func(t *testing.T) { assertAlias(t, filepath.Join(filepath.Dir(path), "MIHARI LONG BINARY.EXE")) })
	t.Run("short name", func(t *testing.T) {
		input, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		buffer := make([]uint16, 32768)
		n, err := windows.GetShortPathName(input, &buffer[0], uint32(len(buffer)))
		if err != nil {
			t.Fatal(err)
		}
		short := windows.UTF16ToString(buffer[:n])
		if strings.EqualFold(short, path) {
			t.Skip("volume does not generate a short filename")
		}
		assertAlias(t, short)
	})
}
