package service

import (
	"context"
	"errors"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ManagedDaemonPID identifies the installed service without inspecting other processes.
func (m *Manager) ManagedDaemonPID(ctx context.Context) (pid int, returnErr error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, manager.Disconnect()) }()
	installed, err := manager.OpenService(serviceName)
	if err != nil {
		if isServiceDoesNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, installed.Close()) }()
	return managedDaemonPID(ctx, installed.Query)
}

func managedDaemonPID(ctx context.Context, query func() (svc.Status, error)) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	status, err := query()
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if status.State != svc.Running {
		return 0, nil
	}
	return int(status.ProcessId), nil
}
