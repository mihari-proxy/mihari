package tundetect

import (
	"strconv"
	"strings"
)

// parseLinuxStatPPID reads the parent pid from a /proc/<pid>/stat payload.
// The comm field is parenthesis-delimited and may contain spaces.
func parseLinuxStatPPID(stat string) (int, bool) {
	end := strings.LastIndex(stat, ")")
	if end < 0 || end+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}

// parseDarwinProcessLine parses `ps -eo comm=,pid=,ppid=` output. comm may be a
// path, so pid and ppid are the last two fields.
func parseDarwinProcessLine(line string) (name string, pid, ppid int, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", 0, 0, false
	}
	ppid, err := strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return "", 0, 0, false
	}
	pid, err = strconv.Atoi(fields[len(fields)-2])
	if err != nil {
		return "", 0, 0, false
	}
	return strings.Join(fields[:len(fields)-2], " "), pid, ppid, true
}
