//go:build linux

package logging

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExport_UnprovedWorkspaceAcquisitionReportsWarning(t *testing.T) {
	fs, paths := openExportTestFS(t)
	parent := t.TempDir()
	// An inherited access ACL is conservatively unproved at acquisition.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range []struct {
		tag, perm uint16
		id        uint32
	}{{1, 7, 0xffffffff}, {2, 7, 4242}, {4, 0, 0xffffffff}, {16, 7, 0xffffffff}, {32, 0, 0xffffffff}} {
		offset := 4 + i*8
		binary.LittleEndian.PutUint16(acl[offset:], entry.tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], entry.perm)
		binary.LittleEndian.PutUint32(acl[offset+4:], entry.id)
	}
	if err := unix.Setxattr(parent, "system.posix_acl_default", acl, 0); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTSUP) {
			t.Skip("filesystem does not support POSIX ACL xattrs")
		}
		t.Fatal(err)
	}
	out := filepath.Join(parent, "result.zip")
	warnings := 0
	_, err := Export(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs, Redactor: NewRedactor(), OnWarning: func(error) { warnings++ }})
	if err == nil {
		t.Fatal("unproved workspace accepted")
	}
	if warnings != 1 {
		t.Fatalf("acquisition cleanup warning count=%d want1", warnings)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed export published: %v", err)
	}
}
