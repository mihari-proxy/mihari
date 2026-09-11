//go:build darwin

package platform

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Use libSystem's fd APIs, as x/sys requires on Darwin. The pinned Go syscall
// package explicitly preserves syscall.syscall6 for external ABI callers.
//
//go:linkname darwinLibcCall6 syscall.syscall6
//go:uintptrescapes
func darwinLibcCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, syscall.Errno)

var libcFgetattrlistAddr, libcFilesecInitAddr, libcFilesecFreeAddr, libcFilesecSetAddr, libcFchmodxAddr uintptr

//go:cgo_import_dynamic mihari_fgetattrlist fgetattrlist "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic mihari_filesec_init filesec_init "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic mihari_filesec_free filesec_free "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic mihari_filesec_set_property filesec_set_property "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic mihari_fchmodx_np fchmodx_np "/usr/lib/libSystem.B.dylib"

func darwinFDAttributes(fd int, attrs *unix.Attrlist, buffer []byte, options uintptr) error {
	if len(buffer) == 0 {
		return unix.EINVAL
	}
	_, _, errno := darwinLibcCall6(libcFgetattrlistAddr, uintptr(fd), uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), options, 0)
	runtime.KeepAlive(attrs)
	runtime.KeepAlive(buffer)
	if errno != 0 {
		return errno
	}
	return nil
}

func darwinRemoveFDACL(fd int) error {
	// Apple's public filesec API: only FILESEC_ACL is specified. fchmodx_np
	// preserves unspecified owner/group/mode, and _FILESEC_REMOVE_ACL removes
	// the extended ACL (an empty ACL would be a different operation).
	// acl_delete_fd_np is intentionally not used: Apple's Libc returns ENOTSUP.
	filesec, _, errno := darwinLibcCall6(libcFilesecInitAddr, 0, 0, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	if filesec == 0 {
		return unix.ENOMEM
	}
	defer func() {
		// filesec_free returns void; its register return value has no error meaning.
		_, _, _ = darwinLibcCall6(libcFilesecFreeAddr, filesec, 0, 0, 0, 0, 0)
	}()
	const fileSecACL = 5
	const removeACL = 1
	_, _, errno = darwinLibcCall6(libcFilesecSetAddr, filesec, fileSecACL, removeACL, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	_, _, errno = darwinLibcCall6(libcFchmodxAddr, uintptr(fd), filesec, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
