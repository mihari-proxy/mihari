//go:build linux

package platform

import (
	"bufio"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
)

const tcpListenState = "0A"

// LookupTCPOccupant reports the process listening on address (host:port).
// Lookup failures return ok=false.
func LookupTCPOccupant(address string) (TCPOccupant, bool) {
	ip, port, ok := parseTCPAddress(address)
	if !ok {
		return TCPOccupant{}, false
	}
	inode, ok := listenInode("/proc/net/tcp", ip, port)
	if !ok {
		inode, ok = listenInode("/proc/net/tcp6", ip, port)
	}
	if !ok {
		return TCPOccupant{}, false
	}
	return occupantBySocketInode(inode)
}

func listenInode(path string, query net.IP, port int) (uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, false
	}
	for scanner.Scan() {
		inode, ok := matchProcNetListen(scanner.Text(), query, port)
		if ok {
			return inode, true
		}
	}
	return 0, false
}

func matchProcNetListen(line string, query net.IP, port int) (uint64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 10 || fields[3] != tcpListenState {
		return 0, false
	}
	local, ok := parseProcNetLocal(fields[1])
	if !ok {
		return 0, false
	}
	if local.port != port || !listenIPMatches(local.ip, query) {
		return 0, false
	}
	inode, err := strconv.ParseUint(fields[9], 10, 64)
	if err != nil || inode == 0 {
		return 0, false
	}
	return inode, true
}

type procNetLocal struct {
	ip   net.IP
	port int
}

func parseProcNetLocal(field string) (procNetLocal, bool) {
	host, portHex, ok := strings.Cut(field, ":")
	if !ok {
		return procNetLocal{}, false
	}
	ip := parseProcNetIP(host)
	if ip == nil {
		return procNetLocal{}, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil || port == 0 {
		return procNetLocal{}, false
	}
	return procNetLocal{ip: ip, port: int(port)}, true
}

func parseProcNetIP(s string) net.IP {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	switch len(b) {
	case 4:
		return net.IPv4(b[3], b[2], b[1], b[0])
	case 16:
		ip := make(net.IP, net.IPv6len)
		for i := 0; i < 16; i += 4 {
			ip[i], ip[i+1], ip[i+2], ip[i+3] = b[i+3], b[i+2], b[i+1], b[i]
		}
		return ip
	default:
		return nil
	}
}

func occupantBySocketInode(inode uint64) (TCPOccupant, bool) {
	want := "socket:[" + strconv.FormatUint(inode, 10) + "]"
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return TCPOccupant{}, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if !pidHasSocket(entry.Name(), want) {
			continue
		}
		return TCPOccupant{PID: pid, Process: readComm(entry.Name())}, true
	}
	return TCPOccupant{}, false
}

func pidHasSocket(pid, want string) bool {
	fds, err := os.ReadDir("/proc/" + pid + "/fd")
	if err != nil {
		return false
	}
	for _, fd := range fds {
		target, err := os.Readlink("/proc/" + pid + "/fd/" + fd.Name())
		if err != nil {
			continue
		}
		if target == want {
			return true
		}
	}
	return false
}

func readComm(pid string) string {
	data, err := os.ReadFile("/proc/" + pid + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
