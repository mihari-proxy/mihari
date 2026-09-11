//go:build darwin

package platform

import (
	"context"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// DarwinGroupHasPeers reports whether the calling process's launchd group has
// any other members. The caller must be its own group leader and prevent new
// child starts while using a peer-free observation to authorize maintenance.
// The query never signals processes or adopts a PID from the returned sample.
func DarwinGroupHasPeers(ctx context.Context) (bool, error) {
	return darwinGroupHasPeers(ctx, os.Getpid(), unix.Getpgrp(), darwinListGroupPIDs)
}

var libcProcListPIDsAddr uintptr

//go:cgo_import_dynamic mihari_proc_listpids proc_listpids "/usr/lib/libSystem.B.dylib"

func darwinListGroupPIDs(group int, pids *[2]int32) (int, error) {
	// PROC_PGRP_ONLY is Apple's public libproc.h selector. The kernel copies
	// this bounded sample under proc_list_lock, including zombie members.
	const procPgrpOnly = 2
	n, _, errno := darwinLibcCall6(libcProcListPIDsAddr, procPgrpOnly, uintptr(group), uintptr(unsafe.Pointer(pids)), unsafe.Sizeof(*pids), 0, 0)
	runtime.KeepAlive(pids)
	if errno != 0 {
		return 0, errno
	}
	// libproc reports syscall failure as zero. Zero is also an invalid empty
	// observation here: the calling group leader must still be present.
	return int(n), nil
}
