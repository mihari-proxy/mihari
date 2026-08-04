//go:build unix

package elevate

import "os"

func platformElevated() bool {
	return os.Geteuid() == 0
}
