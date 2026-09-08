package service

import (
	"fmt"
	"strconv"
	"strings"
)

// RecordedLaunchdIdentity reconstructs a query-only group identity from a
// protected runtime generation. It never authorizes signaling a historic PID.
func RecordedLaunchdIdentity(boot string, pid int, start string) (ProcessIdentity, error) {
	seconds, micros, ok := strings.Cut(start, ".")
	if !ok || len(micros) != 6 || int64(pid) > 1<<31-1 {
		return ProcessIdentity{}, invalidServiceState("recorded launchd process identity is unknown")
	}
	s, secondsErr := strconv.ParseInt(seconds, 10, 64)
	u, microsErr := strconv.ParseUint(micros, 10, 32)
	if secondsErr != nil || microsErr != nil || s <= 0 || u >= 1000000 || strconv.FormatInt(s, 10) != seconds || fmt.Sprintf("%06d", u) != micros {
		return ProcessIdentity{}, invalidServiceState("recorded launchd process identity is unknown")
	}
	id := ProcessIdentity{BootID: boot, PID: pid, StartUnix: s, StartUsec: uint32(u)}
	group, err := darwinGroupToken(id)
	if err != nil {
		return ProcessIdentity{}, err
	}
	id.Group = group
	return id, nil
}
