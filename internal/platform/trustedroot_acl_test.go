package platform

import (
	"encoding/binary"
	"testing"
)

func posixACLFixture(perms uint16, mask uint16) []byte {
	b := make([]byte, 44)
	binary.LittleEndian.PutUint32(b, 2)
	for i, e := range [][3]uint32{{1, 7, 0xffffffff}, {2, uint32(perms), 4242}, {4, 5, 0xffffffff}, {16, uint32(mask), 0xffffffff}, {32, 5, 0xffffffff}} {
		o := 4 + i*8
		binary.LittleEndian.PutUint16(b[o:], uint16(e[0]))
		binary.LittleEndian.PutUint16(b[o+2:], uint16(e[1]))
		binary.LittleEndian.PutUint32(b[o+4:], e[2])
	}
	return b
}
func TestTrustedRoot_POSIXTraversalACL(t *testing.T) {
	for _, p := range [][2]uint16{{5, 7}, {7, 5}} {
		if err := trustedPOSIXACL(posixACLFixture(p[0], p[1]), 0); err != nil {
			t.Fatalf("read-only positive control: %v", err)
		}
	}
	if err := trustedPOSIXACL(posixACLFixture(7, 7), 0); err == nil {
		t.Fatal("accepted effective foreign write")
	}
}

func darwinACLFixture(flags, rights uint32) []byte {
	b := make([]byte, 80)
	binary.LittleEndian.PutUint32(b, 80)
	binary.LittleEndian.PutUint32(b[4:], 8)
	binary.LittleEndian.PutUint32(b[8:], 68)
	binary.LittleEndian.PutUint32(b[12:], 0x012cc16d)
	binary.LittleEndian.PutUint32(b[48:], 1)
	binary.LittleEndian.PutUint32(b[72:], flags)
	binary.LittleEndian.PutUint32(b[76:], rights)
	return b
}
func TestTrustedRoot_DarwinTraversalACL(t *testing.T) {
	for _, b := range [][]byte{darwinACLFixture(1, 1<<1), darwinACLFixture(2, 1<<2)} {
		if err := trustedDarwinACL(b, false); err != nil {
			t.Fatalf("positive traversal: %v", err)
		}
	}
	for _, bit := range []uint32{2, 4, 5, 6, 8, 10, 12, 13, 21, 23} {
		if err := trustedDarwinACL(darwinACLFixture(1, 1<<bit), false); err == nil {
			t.Fatalf("accepted ALLOW bit %d", bit)
		}
	}
}

func TestCreationParent_DarwinRejectsEveryAttachedACL(t *testing.T) {
	for _, flags := range []uint32{1, 2, 1 | 1<<8} {
		if err := trustedDarwinACL(darwinACLFixture(flags, 1<<1), true); err == nil {
			t.Fatalf("accepted attached ACL flags %x", flags)
		}
	}
	// A zero-length attribute reference is the native representation of absence.
	b := make([]byte, 12)
	binary.LittleEndian.PutUint32(b, 12)
	if err := trustedDarwinACL(b, true); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedRoot_ACLRejectsMalformed(t *testing.T) {
	for _, b := range [][]byte{{}, {2}, make([]byte, 4), posixACLFixture(5, 5)[:43]} {
		if err := trustedPOSIXACL(b, 0); err == nil {
			t.Fatal("accepted malformed POSIX ACL")
		}
	}
	for _, offset := range []uint32{0, 0xffffffff, 4096} {
		b := darwinACLFixture(1, 2)
		binary.LittleEndian.PutUint32(b[4:], offset)
		if err := trustedDarwinACL(b, false); err == nil {
			t.Fatal("accepted invalid signed attribute offset")
		}
	}
	for _, flags := range []uint32{0, 3, 0xf, 1 | 1<<30} {
		if err := trustedDarwinACL(darwinACLFixture(flags, 2), false); err == nil {
			t.Fatal("accepted unknown ACE flags")
		}
	}
	b := darwinACLFixture(1, 2)
	binary.LittleEndian.PutUint32(b[48:], 129)
	if err := trustedDarwinACL(b, false); err == nil {
		t.Fatal("accepted excessive ACE count")
	}
}
