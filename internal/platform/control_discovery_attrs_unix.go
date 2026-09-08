//go:build linux || darwin

package platform

import (
	"encoding/binary"
	"os"
)

const discoveryDarwinCommon = uint32(0x80000000 | 0x2 | 0x8 | 0x8000 | 0x10000 | 0x20000 | 0x400000 | 0x2000000)
const discoveryDarwinRealFSID = uint32(0x80)

// XNU mount.h: MNT_LOCAL=0x1000, MNT_IGNORE_OWNERSHIP=0x200000.
// Kept with the pure mount policy so fixtures run on both supported Unix OSes.
func checkDarwinMountRecord(m discoveryMount) error {
	if (m.kind != "apfs" && m.kind != "hfs") || m.flags&0x1000 == 0 || m.flags&0x200000 != 0 {
		return os.ErrPermission
	}
	return nil
}

type discoveryDarwinAttrs struct {
	dev, objtype, uid, gid, mode uint32
	ino                          uint64
	fsid                         [2]int32
	aclMissing                   bool
}

func parseDiscoveryDarwinAttrs(b []byte, strict bool) (discoveryDarwinAttrs, error) {
	bad := func() (discoveryDarwinAttrs, error) { return discoveryDarwinAttrs{}, os.ErrPermission }
	if len(b) < 68 {
		return bad()
	}
	u := func(o int) uint32 { return binary.LittleEndian.Uint32(b[o:]) }
	total := uint64(u(0))
	if total < 68 || total > uint64(len(b)) {
		return bad()
	}
	mask := u(4)
	missing := mask&0x400000 == 0
	if mask|0x400000 != discoveryDarwinCommon || u(8) != 0 || u(12) != 0 || u(16) != 0 || u(20) != discoveryDarwinRealFSID {
		return bad()
	}
	start := int64(44) + int64(int32(u(44)))
	size := uint64(u(48))
	if start != 68 || size != total-68 {
		return bad()
	}
	if missing && size != 0 || !missing && size == 0 {
		return bad()
	}
	acl := make([]byte, 12+size)
	binary.LittleEndian.PutUint32(acl, uint32(len(acl)))
	binary.LittleEndian.PutUint32(acl[4:], 8)
	binary.LittleEndian.PutUint32(acl[8:], uint32(size))
	copy(acl[12:], b[68:total])
	if err := trustedDarwinACL(acl, strict); err != nil {
		return bad()
	}
	return discoveryDarwinAttrs{dev: u(24), objtype: u(28), uid: u(32), gid: u(36), mode: u(40), ino: binary.LittleEndian.Uint64(b[52:]), fsid: [2]int32{int32(u(60)), int32(u(64))}, aclMissing: missing}, nil
}
func loadDiscoveryDarwinAttrs(strict bool, query func(bool) ([]byte, error)) (discoveryDarwinAttrs, error) {
	b, err := query(false)
	if err != nil {
		return discoveryDarwinAttrs{}, err
	}
	a, err := parseDiscoveryDarwinAttrs(b, strict)
	if err != nil || !a.aclMissing {
		return a, err
	}
	proof, err := query(true)
	if err != nil {
		return discoveryDarwinAttrs{}, err
	}
	// Strict non-returned identity+ACL request: supported NULL is exactly a
	// zero-length reference. Unsupported va_acl fails in XNU before packing.
	if len(proof) < 40 || binary.LittleEndian.Uint32(proof) != 40 || binary.LittleEndian.Uint32(proof[24:]) != 16 || binary.LittleEndian.Uint32(proof[28:]) != 0 {
		return discoveryDarwinAttrs{}, os.ErrPermission
	}
	u := func(o int) uint32 { return binary.LittleEndian.Uint32(proof[o:]) }
	if u(4) != a.dev || u(8) != a.objtype || u(12) != a.uid || u(16) != a.gid || u(20) != a.mode || binary.LittleEndian.Uint64(proof[32:]) != a.ino {
		return discoveryDarwinAttrs{}, ErrIdentityMismatch
	}
	after, err := query(false)
	if err != nil {
		return discoveryDarwinAttrs{}, err
	}
	checked, err := parseDiscoveryDarwinAttrs(after, strict)
	if err != nil {
		return discoveryDarwinAttrs{}, err
	}
	if checked != a {
		return discoveryDarwinAttrs{}, ErrIdentityMismatch
	}
	return a, nil
}
func completeDiscoveryMount(fsid [2]int32, query func(int) ([]discoveryMount, int, error)) (discoveryMount, error) {
	for capacity := 32; capacity <= 4096; capacity *= 2 {
		entries, n, err := query(capacity)
		if err != nil {
			return discoveryMount{}, err
		}
		if n < 0 || n > capacity || len(entries) < n {
			return discoveryMount{}, os.ErrPermission
		}
		if n == capacity {
			continue
		}
		var found discoveryMount
		matches := 0
		for _, m := range entries[:n] {
			if m.fsid == fsid {
				found = m
				matches++
			}
		}
		if matches != 1 {
			return discoveryMount{}, os.ErrPermission
		}
		return found, nil
	}
	return discoveryMount{}, os.ErrPermission
}
