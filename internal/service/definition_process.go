package service

import "context"

func lookupProcessIdentity(ctx context.Context, id ProcessIdentity, identify func(context.Context, int) (ProcessIdentity, error)) (bool, error) {
	if id.PID <= 0 {
		return false, nil
	}
	if incompleteProcessIdentity(id) {
		return false, invalidServiceState("service process identity is unknown")
	}
	got, err := identify(ctx, id.PID)
	if err != nil {
		return false, err
	}
	if got.PID == 0 {
		return false, nil
	}
	if got.BootID != id.BootID || got.StartUnix != id.StartUnix || got.StartUsec != id.StartUsec {
		// Darwin has no retained cgroup emptiness proof. A changed observation
		// must not release the service stop barrier or authorize a signal.
		return false, invalidServiceState("service process identity changed")
	}
	return true, nil
}
