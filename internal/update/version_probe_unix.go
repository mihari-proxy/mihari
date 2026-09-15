//go:build linux || darwin

package update

import "context"

func observeUserReplacementTarget(_ context.Context, target ReplacementTarget) (ReplacementTarget, error) {
	return target, nil
}
