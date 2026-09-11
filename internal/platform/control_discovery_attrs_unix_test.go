//go:build linux || darwin

package platform

import (
	"encoding/binary"
	"errors"
	"os"
	"syscall"
	"testing"
)

func discoveryAttrsFixture() []byte {
	b := make([]byte, 112)
	put := func(o int, v uint32) { binary.LittleEndian.PutUint32(b[o:], v) }
	put(0, 112)
	put(4, discoveryDarwinCommon)
	put(20, 0x80)
	put(24, 7)
	put(28, 2)
	put(32, 0)
	put(36, 0)
	put(40, 040711)
	put(44, 24)
	put(48, 44)
	binary.LittleEndian.PutUint64(b[52:], 0x100000002)
	put(60, 19)
	put(64, 23)
	put(68, 0x012cc16d)
	put(104, 0xffffffff)
	return b
}
func TestDiscoveryDarwinAttrs_ProvesAbsentACL(t *testing.T) {
	combined := discoveryAttrsFixture()[:68]
	binary.LittleEndian.PutUint32(combined, 68)
	binary.LittleEndian.PutUint32(combined[4:], discoveryDarwinCommon&^0x400000)
	binary.LittleEndian.PutUint32(combined[48:], 0)
	absent := make([]byte, 40)
	binary.LittleEndian.PutUint32(absent, 40)
	copy(absent[4:24], combined[24:44])
	binary.LittleEndian.PutUint32(absent[24:], 16)
	copy(absent[32:], combined[52:60])
	for _, tc := range []struct {
		name     string
		queryErr error
		want     bool
	}{{"supported absent", nil, true}, {"query denied", os.ErrPermission, false}} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := loadDiscoveryDarwinAttrs(true, func(strict bool) ([]byte, error) {
				if strict {
					return absent, tc.queryErr
				}
				return combined, nil
			})
			if tc.want {
				if err != nil || a.ino != 0x100000002 {
					t.Fatalf("proven absence rejected: %+v %v", a, err)
				}
			} else if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("unproved absence accepted: %v", err)
			}
		})
	}
}
func TestDiscoveryDarwinAttrs_CombinedIdentity(t *testing.T) {
	a, err := parseDiscoveryDarwinAttrs(discoveryAttrsFixture(), true)
	if err != nil {
		t.Fatalf("valid combined attrs: %v", err)
	}
	if a.ino != 0x100000002 || a.dev != 7 || a.fsid != [2]int32{19, 23} || a.mode != 040711 {
		t.Fatalf("lost combined identity: %+v", a)
	}
}
func TestDiscoveryDarwinMount_CompleteUnique(t *testing.T) {
	want := discoveryMount{fsid: [2]int32{19, 23}, kind: "apfs", owner: 1001}
	m, err := completeDiscoveryMount(want.fsid, func(capacity int) ([]discoveryMount, int, error) { return []discoveryMount{want}, 1, nil })
	if err != nil || m != want {
		t.Fatalf("valid nonroot-owner mount refused: %+v %v", m, err)
	}
}

func TestDiscoveryDarwinAttrs_RejectsMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]byte)
	}{
		{"truncated total", func(b []byte) { binary.LittleEndian.PutUint32(b, 4097) }},
		{"missing real fsid", func(b []byte) { binary.LittleEndian.PutUint32(b[20:], 0) }},
		{"missing owner", func(b []byte) { binary.LittleEndian.PutUint32(b[4:], discoveryDarwinCommon&^0x8000) }},
		{"unknown returned attribute", func(b []byte) { binary.LittleEndian.PutUint32(b[8:], 1) }},
		{"negative ACL offset", func(b []byte) { binary.LittleEndian.PutUint32(b[44:], 0xfffffff0) }},
		{"ACL into fixed fields", func(b []byte) { binary.LittleEndian.PutUint32(b[44:], 4) }},
		{"oversized ACL", func(b []byte) { binary.LittleEndian.PutUint32(b[48:], 65535) }},
		{"attached empty ACL", func(b []byte) { binary.LittleEndian.PutUint32(b[104:], 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := discoveryAttrsFixture()
			tc.edit(b)
			if _, err := parseDiscoveryDarwinAttrs(b, true); err == nil {
				t.Fatal("unsafe combined attrs accepted")
			}
		})
	}
}
func TestDiscoveryDarwinMount_FailsIncomplete(t *testing.T) {
	id := [2]int32{19, 23}
	m := discoveryMount{fsid: id, kind: "apfs"}
	for _, name := range []string{"missing", "duplicate", "saturated", "query error", "invalid count"} {
		t.Run(name, func(t *testing.T) {
			_, err := completeDiscoveryMount(id, func(capacity int) ([]discoveryMount, int, error) {
				switch name {
				case "missing":
					return nil, 0, nil
				case "duplicate":
					return []discoveryMount{m, m}, 2, nil
				case "saturated":
					return make([]discoveryMount, capacity), capacity, nil
				case "query error":
					return nil, 0, os.ErrPermission
				default:
					return nil, -1, nil
				}
			})
			if err == nil {
				t.Fatal("unproven mount accepted")
			}
		})
	}
	calls := 0
	got, err := completeDiscoveryMount(id, func(capacity int) ([]discoveryMount, int, error) {
		calls++
		if calls == 1 {
			return make([]discoveryMount, capacity), capacity, nil
		}
		return []discoveryMount{m}, 1, nil
	})
	if err != nil || got != m || calls != 2 {
		t.Fatalf("bounded growth failed: %+v %v calls=%d", got, err, calls)
	}
}

func TestDiscoveryDarwinAttrs_AbsenceInconsistency(t *testing.T) {
	combined := discoveryAttrsFixture()[:68]
	binary.LittleEndian.PutUint32(combined, 68)
	binary.LittleEndian.PutUint32(combined[4:], discoveryDarwinCommon&^0x400000)
	binary.LittleEndian.PutUint32(combined[48:], 0)
	proof := make([]byte, 40)
	binary.LittleEndian.PutUint32(proof, 40)
	copy(proof[4:24], combined[24:44])
	binary.LittleEndian.PutUint32(proof[24:], 16)
	copy(proof[32:], combined[52:60])
	for _, name := range []string{"unsupported", "truncated", "bad zero offset", "changed owner", "nonempty ACL", "changed real fsid"} {
		t.Run(name, func(t *testing.T) {
			p := append([]byte(nil), proof...)
			calls := 0
			_, err := loadDiscoveryDarwinAttrs(true, func(strict bool) ([]byte, error) {
				if strict {
					switch name {
					case "unsupported":
						return nil, syscall.EINVAL
					case "truncated":
						return p[:39], nil
					case "bad zero offset":
						binary.LittleEndian.PutUint32(p[24:], 0)
					case "changed owner":
						binary.LittleEndian.PutUint32(p[12:], 1234)
					case "nonempty ACL":
						binary.LittleEndian.PutUint32(p[28:], 44)
					}
					return p, nil
				}
				calls++
				b := append([]byte(nil), combined...)
				if name == "changed real fsid" && calls == 2 {
					binary.LittleEndian.PutUint32(b[60:], 99)
				}
				return b, nil
			})
			if err == nil {
				t.Fatal("inconsistent absence proof accepted")
			}
		})
	}
}
func TestDiscoveryDarwinAttrs_TraversalACLRights(t *testing.T) {
	for _, tc := range []struct {
		name         string
		kind, rights uint32
		ok           bool
	}{{"read allow", 1, 1 << 1, true}, {"deny write", 2, 1 << 2, true}, {"target delete", 1, 1 << 4, false}, {"parent delete child", 1, 1 << 6, false}, {"write security", 1, 1 << 12, false}, {"unknown semantics", 3, 1 << 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			acl := darwinACLFixture(tc.kind, tc.rights)[12:]
			b := append(discoveryAttrsFixture()[:68], acl...)
			binary.LittleEndian.PutUint32(b, uint32(len(b)))
			binary.LittleEndian.PutUint32(b[48:], uint32(len(acl)))
			_, err := parseDiscoveryDarwinAttrs(b, false)
			if (err == nil) != tc.ok {
				t.Fatalf("ACL accepted=%v want=%v", err == nil, tc.ok)
			}
		})
	}
}

func TestDiscoveryDarwinMount_Policy(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		flags      uint64
		ok         bool
	}{{"APFS", "apfs", 0x1000, true}, {"HFS", "hfs", 0x1000, true}, {"nonlocal", "apfs", 0, false}, {"ownership ignored", "apfs", 0x201000, false}, {"unknown filesystem", "fuse", 0x1000, false}} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDarwinMountRecord(discoveryMount{kind: tc.kind, flags: tc.flags, owner: 1001})
			if (err == nil) != tc.ok {
				t.Fatalf("mount policy=%v wantaccepted=%v", err, tc.ok)
			}
		})
	}
}
