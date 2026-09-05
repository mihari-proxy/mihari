package platform

import (
	"encoding/binary"
	"os"
)

// POSIX ACL xattrs use little endian even on big endian hosts. Absence is
// represented by ENODATA at the fd boundary, never by a malformed empty ACL.
func trustedPOSIXACL(b []byte, owner uint32) error {
	if len(b) < 28 || (len(b)-4)%8 != 0 || binary.LittleEndian.Uint32(b) != 2 {
		return os.ErrPermission
	}
	state := uint16(1)
	mask := uint16(7)
	needsMask := false
	type entry struct {
		tag, perm uint16
		id        uint32
	}
	entries := make([]entry, 0, (len(b)-4)/8)
	seen := map[[2]uint32]bool{}
	for o := 4; o < len(b); o += 8 {
		e := entry{binary.LittleEndian.Uint16(b[o:]), binary.LittleEndian.Uint16(b[o+2:]), binary.LittleEndian.Uint32(b[o+4:])}
		if e.perm&^uint16(7) != 0 {
			return os.ErrPermission
		}
		if e.tag == 2 || e.tag == 8 {
			key := [2]uint32{uint32(e.tag), e.id}
			if e.id == 0xffffffff || seen[key] {
				return os.ErrPermission
			}
			seen[key] = true
		} else if e.id != 0xffffffff {
			return os.ErrPermission
		}
		switch e.tag {
		case 1:
			if state != 1 {
				return os.ErrPermission
			}
			state = 2
		case 2:
			if state != 2 {
				return os.ErrPermission
			}
			needsMask = true
		case 4:
			if state != 2 {
				return os.ErrPermission
			}
			state = 8
		case 8:
			if state != 8 {
				return os.ErrPermission
			}
			needsMask = true
		case 16:
			if state != 8 {
				return os.ErrPermission
			}
			state = 32
			mask = e.perm
		case 32:
			if state != 32 && (state != 8 || needsMask) {
				return os.ErrPermission
			}
			state = 0
		default:
			return os.ErrPermission
		}
		entries = append(entries, e)
	}
	if state != 0 {
		return os.ErrPermission
	}
	for _, e := range entries {
		switch e.tag {
		case 2:
			if e.id != 0 && e.id != owner && e.perm&mask&2 != 0 {
				return os.ErrPermission
			}
		case 4, 8:
			if e.perm&mask&2 != 0 {
				return os.ErrPermission
			}
		case 32:
			if e.perm&2 != 0 {
				return os.ErrPermission
			}
		}
	}
	return nil
}

// Darwin's supported amd64 and arm64 ABIs are little endian. dataoffset is
// signed and relative to the attrreference (byte 4), not the whole buffer.
func trustedDarwinACL(b []byte, strict bool) error {
	if len(b) < 12 {
		return os.ErrPermission
	}
	total := uint64(binary.LittleEndian.Uint32(b))
	if total < 12 || total > uint64(len(b)) {
		return os.ErrPermission
	}
	offset := int64(int32(binary.LittleEndian.Uint32(b[4:])))
	size := uint64(binary.LittleEndian.Uint32(b[8:]))
	if size == 0 {
		if offset != 0 && offset != 8 {
			return os.ErrPermission
		}
		return nil
	}
	start := int64(4) + offset
	if start < 12 || uint64(start) > total || size > total-uint64(start) || size < 44 {
		return os.ErrPermission
	}
	s := b[start : uint64(start)+size]
	if binary.LittleEndian.Uint32(s) != 0x012cc16d {
		return os.ErrPermission
	}
	count := binary.LittleEndian.Uint32(s[36:])
	flags := binary.LittleEndian.Uint32(s[40:])
	if flags&^uint32(0x3ffff) != 0 {
		return os.ErrPermission
	}
	if count == 0xffffffff {
		if size != 44 {
			return os.ErrPermission
		}
		return nil
	}
	if count > 128 || size != 44+24*uint64(count) || strict {
		return os.ErrPermission
	}
	const write = 1<<2 | 1<<4 | 1<<5 | 1<<6 | 1<<8 | 1<<10 | 1<<12 | 1<<13 | 1<<21 | 1<<23
	const known = uint32(0x3ffe | 1<<20 | 1<<21 | 1<<22 | 1<<23 | 1<<24)
	for o := 44; o < len(s); o += 24 {
		f := binary.LittleEndian.Uint32(s[o+16:])
		rights := binary.LittleEndian.Uint32(s[o+20:])
		kind := f & 15
		if (kind != 1 && kind != 2) || f&^uint32(0x1ff) != 0 || rights&^known != 0 {
			return os.ErrPermission
		}
		if kind == 1 && rights&write != 0 {
			return os.ErrPermission
		}
	}
	return nil
}
