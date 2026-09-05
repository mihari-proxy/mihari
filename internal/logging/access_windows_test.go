//go:build windows

package logging

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type rotatingAccessFS struct {
	*platform.PrivateFS
	beforeRepair func(string)
	lists        int
}

func (f *rotatingAccessFS) ReadDir(path string) ([]platform.FileEntry, error) {
	f.lists++
	return f.PrivateFS.ReadDir(path)
}

func (f *rotatingAccessFS) RepairAccessChecked(path string, id platform.FileIdentity) error {
	if f.beforeRepair != nil {
		hook := f.beforeRepair
		f.beforeRepair = nil
		hook(path)
	}
	return f.PrivateFS.RepairAccessChecked(path, id)
}

func TestRepairExistingLogAccess_ConcurrentOtherSequenceRotation(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "replaced"}[replace], func(t *testing.T) {
			fs, paths := openTestLogFS(t)
			mustWriteFile(t, fs, paths.DaemonLog, []byte("old\n"))
			wrapped := &rotatingAccessFS{PrivateFS: fs, beforeRepair: func(path string) {
				if err := fs.Rename(path, path+".1"); err != nil {
					t.Fatal(err)
				}
				if replace {
					mustWriteFile(t, fs, path, []byte("new\n"))
				}
			}}
			if err := repairExistingLogAccess(context.Background(), wrapped, paths.LogDir); err != nil {
				t.Fatalf("rotation blocked startup: %v", err)
			}
			if wrapped.lists != 2 {
				t.Fatalf("expected re-enumeration, lists=%d", wrapped.lists)
			}
			entries, err := fs.ReadDir(paths.LogDir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if filepath.Ext(entry.Name) == ".1" {
					return
				}
			}
			t.Fatal("migration deleted rotated archive")
		})
	}
}
