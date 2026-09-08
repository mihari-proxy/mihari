package service

import "context"

// InspectLaunchdRuntimeProcess verifies the installed shared-group contract
// before its caller may persist the process as a runtime generation.
func InspectLaunchdRuntimeProcess(ctx context.Context, pid int) (ProcessIdentity, error) {
	id, err := (darwinProcessTree{}).Identify(ctx, pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if err := validateDarwinGroup(id); err != nil {
		return ProcessIdentity{}, err
	}
	return id, nil
}

// LaunchdRuntimeGroupEmpty passively checks a recorded group. It does not
// signal historic process IDs, and only proven absence counts as empty.
func LaunchdRuntimeGroupEmpty(ctx context.Context, id ProcessIdentity) (bool, error) {
	if err := validateDarwinGroup(id); err != nil {
		return false, err
	}
	return (darwinProcessTree{}).Empty(ctx, id.Group)
}
