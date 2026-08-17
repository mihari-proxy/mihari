//go:build windows

package platform

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TCP_TABLE_OWNER_PID_LISTENER from tcpmib.h.
const tcpTableOwnerPIDListener = 3

const (
	mibTCPRowOwnerPIDSize  = 24
	mibTCP6RowOwnerPIDSize = 56
)

var (
	modiphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

// LookupTCPOccupant reports the process listening on address (host:port).
// Lookup failures return ok=false.
func LookupTCPOccupant(address string) (TCPOccupant, bool) {
	ip, port, ok := parseTCPAddress(address)
	if !ok {
		return TCPOccupant{}, false
	}
	if pid, ok := occupantPID(windows.AF_INET, ip, port); ok {
		return occupantFromPID(pid)
	}
	if pid, ok := occupantPID(windows.AF_INET6, ip, port); ok {
		return occupantFromPID(pid)
	}
	return TCPOccupant{}, false
}

func occupantFromPID(pid uint32) (TCPOccupant, bool) {
	if pid == 0 {
		return TCPOccupant{}, false
	}
	return TCPOccupant{PID: int(pid), Process: processBasename(pid)}, true
}

func occupantPID(family uint32, ip net.IP, port int) (uint32, bool) {
	buf, err := extendedTCPTable(family)
	if err != nil || len(buf) < 4 {
		return 0, false
	}
	n := binary.LittleEndian.Uint32(buf[:4])
	rowSize := mibTCPRowOwnerPIDSize
	if family == windows.AF_INET6 {
		rowSize = mibTCP6RowOwnerPIDSize
	}
	offset := 4
	for i := uint32(0); i < n; i++ {
		if offset+rowSize > len(buf) {
			return 0, false
		}
		row := buf[offset : offset+rowSize]
		offset += rowSize
		local, localPort, pid, ok := parseTCPOwnerRow(family, row)
		if !ok || pid == 0 || localPort != port || !listenIPMatches(local, ip) {
			continue
		}
		return pid, true
	}
	return 0, false
}

func parseTCPOwnerRow(family uint32, row []byte) (net.IP, int, uint32, bool) {
	if family == windows.AF_INET6 {
		if len(row) < mibTCP6RowOwnerPIDSize {
			return nil, 0, 0, false
		}
		local := make(net.IP, net.IPv6len)
		copy(local, row[0:16])
		port := int(windows.Ntohs(uint16(binary.LittleEndian.Uint32(row[20:24]))))
		pid := binary.LittleEndian.Uint32(row[52:56])
		return local, port, pid, true
	}
	if len(row) < mibTCPRowOwnerPIDSize {
		return nil, 0, 0, false
	}
	addr := binary.LittleEndian.Uint32(row[4:8])
	local := net.IPv4(byte(addr), byte(addr>>8), byte(addr>>16), byte(addr>>24))
	port := int(windows.Ntohs(uint16(binary.LittleEndian.Uint32(row[8:12]))))
	pid := binary.LittleEndian.Uint32(row[20:24])
	return local, port, pid, true
}

func extendedTCPTable(family uint32) ([]byte, error) {
	if err := procGetExtendedTcpTable.Find(); err != nil {
		return nil, err
	}
	var size uint32
	r0, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if windows.Errno(r0) != windows.ERROR_INSUFFICIENT_BUFFER && windows.Errno(r0) != windows.ERROR_SUCCESS {
		return nil, windows.Errno(r0)
	}
	if size == 0 {
		return nil, nil
	}
	for range 3 {
		buf := make([]byte, size)
		r0, _, _ = procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0,
			uintptr(family),
			tcpTableOwnerPIDListener,
			0,
		)
		switch windows.Errno(r0) {
		case windows.ERROR_SUCCESS:
			return buf, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue
		default:
			return nil, windows.Errno(r0)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}

func processBasename(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}
