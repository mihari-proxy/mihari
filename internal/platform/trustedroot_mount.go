package platform

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

func parseTrustedMountID(b []byte) (uint64, error) {
	if len(b) > 8192 || bytes.IndexByte(b, 0) >= 0 {
		return 0, os.ErrPermission
	}
	var id uint64
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != "mnt_id" {
			continue
		}
		if found {
			return 0, os.ErrPermission
		}
		found = true
		value = strings.TrimSpace(value)
		if value == "" {
			return 0, os.ErrPermission
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return 0, os.ErrPermission
			}
		}
		var err error
		id, err = strconv.ParseUint(value, 10, 64)
		if err != nil || id == 0 {
			return 0, os.ErrPermission
		}
	}
	if !found {
		return 0, os.ErrPermission
	}
	return id, nil
}
