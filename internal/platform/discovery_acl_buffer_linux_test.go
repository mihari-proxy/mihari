package platform

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDiscoveryACL_AbsentUsesSmallBuffer(t *testing.T) {
	calls := 0
	err := checkDiscoveryACL(func(buf []byte) (int, error) {
		calls++
		if len(buf) > 256 {
			t.Errorf("absent ACL buffer=%d, want <=256", len(buf))
		}
		return 0, unix.ENODATA
	}, true, 0)
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestDiscoveryACL_BufferFallbackPreservesFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		n         int
		err, want error
	}{
		{"absent after resize", 0, unix.ENODATA, nil},
		{"too large", 0, unix.ERANGE, unix.ERANGE},
		{"read error", 0, unix.EIO, unix.EIO},
		{"invalid length", 65537, nil, os.ErrPermission},
		{"negative length", -1, nil, os.ErrPermission},
		{"strict present", 4, nil, os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := checkDiscoveryACL(func(buf []byte) (int, error) {
				calls++
				if calls == 1 {
					return 0, unix.ERANGE
				}
				if len(buf) != 65536 {
					t.Fatalf("fallback buffer=%d", len(buf))
				}
				return tc.n, tc.err
			}, true, 0)
			if calls != 2 || !errors.Is(err, tc.want) {
				t.Fatalf("calls=%d err=%v want=%v", calls, err, tc.want)
			}
		})
	}
}

func BenchmarkDiscoveryACL_Absent(b *testing.B) {
	read := func([]byte) (int, error) { return 0, unix.ENODATA }
	b.ReportAllocs()
	for b.Loop() {
		if err := checkDiscoveryACL(read, true, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDiscoveryACL_PreservesACLPolicy(t *testing.T) {
	// Include a valid ACL larger than the small buffer to exercise real parsing
	// after ERANGE, not just strict-mode rejection.
	large := make([]byte, 4+8*44)
	binary.LittleEndian.PutUint32(large, 2)
	entry := func(i int, tag, perm uint16, id uint32) {
		o := 4 + i*8
		binary.LittleEndian.PutUint16(large[o:], tag)
		binary.LittleEndian.PutUint16(large[o+2:], perm)
		binary.LittleEndian.PutUint32(large[o+4:], id)
	}
	entry(0, 1, 7, 0xffffffff)
	for i := 1; i <= 40; i++ {
		entry(i, 2, 5, uint32(5000+i))
	}
	entry(41, 4, 5, 0xffffffff)
	entry(42, 16, 5, 0xffffffff)
	entry(43, 32, 5, 0xffffffff)
	for _, tc := range []struct {
		name   string
		data   []byte
		strict bool
		want   error
	}{
		{"safe traversal", posixACLFixture(5, 7), false, nil},
		{"foreign write", posixACLFixture(7, 7), false, os.ErrPermission},
		{"malformed", []byte{2}, false, os.ErrPermission},
		{"strict rejects attached", posixACLFixture(5, 7), true, os.ErrPermission},
		{"large safe traversal", large, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDiscoveryACL(func(buf []byte) (int, error) {
				if len(buf) < len(tc.data) {
					return 0, unix.ERANGE
				}
				return copy(buf, tc.data), nil
			}, tc.strict, 0)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
}
