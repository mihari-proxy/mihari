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

// parseDarwinProcessLine parses `ps -eo pid=,ppid=,comm=` output. The command
// remainder may be a path containing repeated whitespace.
func parseDarwinProcessLine(line string) (name string, pid, ppid int, ok bool) {
	rest := strings.TrimLeft(line, " \t")
	firstEnd := strings.IndexAny(rest, " \t")
	if firstEnd < 0 {
		return "", 0, 0, false
	}
	pid, err := strconv.Atoi(rest[:firstEnd])
	if err != nil {
		return "", 0, 0, false
	}
	rest = strings.TrimLeft(rest[firstEnd:], " \t")
	secondEnd := strings.IndexAny(rest, " \t")
	if secondEnd < 0 {
		return "", 0, 0, false
	}
	ppid, err = strconv.Atoi(rest[:secondEnd])
	if err != nil {
		return "", 0, 0, false
	}
	name = strings.TrimLeft(rest[secondEnd:], " \t")
	if name == "" {
		return "", 0, 0, false
	}
	return name, pid, ppid, true
}
