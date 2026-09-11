package platform

import (
	"os"
	"syscall"
)

func sourceFileTimes(info os.FileInfo) (int64, int64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.ModTime().UnixNano(), 0
	}
	return info.ModTime().UnixNano(), st.Ctimespec.Sec*1e9 + st.Ctimespec.Nsec
}
