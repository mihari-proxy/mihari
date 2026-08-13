//go:build !windows && !darwin && !linux

package tundetect

import "context"

// detect returns an empty Detection on unsupported platforms. An unsupported
// platform must never gate TUN enable: no evidence means no conflict.
func detect(ctx context.Context) (Detection, error) {
	return Detection{}, ctx.Err()
}
