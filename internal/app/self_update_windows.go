//go:build windows

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
	"golang.org/x/sys/windows"
)

// ApplicationUpdateClient is the authenticated daemon preparation surface.
type ApplicationUpdateClient interface {
	Status(context.Context) (protocol.Status, error)
	PrepareApplicationUpdate(context.Context, string) (protocol.ApplicationUpdatePrepared, error)
	ReleaseApplicationUpdate(context.Context, string) error
}

// ApplicationUpdateService keeps service stop/restart in the existing local owner.
type ApplicationUpdateService interface {
	Status() (service.StatusKind, error)
	Stop() error
	Start() error
}

// WindowsUpdateMaintenance joins target locking, daemon drain and related client exit.
type WindowsUpdateMaintenance struct {
	Client         ApplicationUpdateClient
	Service        ApplicationUpdateService
	ObserveTargets update.ReplacementObserver
	openTree       func(context.Context, string, *platform.WindowsProcessIdentity) (updateRuntimeTree, error)
	// ManualDaemonStopped emits the separate foreground-daemon restart instruction.
	ManualDaemonStopped func()
}

type windowsUpdateLease struct {
	locks          []io.Closer
	processes      []*platform.WindowsProcessIdentity
	signals        []windows.Handle
	client         ApplicationUpdateClient
	operation      string
	service        ApplicationUpdateService
	restoreService bool
	tree           updateRuntimeTree
}

func (l *windowsUpdateLease) Close() error {
	var err error
	if l.tree != nil {
		err = l.tree.Close()
		l.tree = nil
	}
	if l.operation != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		e := l.client.ReleaseApplicationUpdate(ctx, l.operation)
		var api protocol.APIError
		// A confirmed stopped daemon has no gate to release; a restarted daemon
		// returns idempotent success without granting the caller any new permission.
		if !errors.As(e, &api) || api.Code != protocol.CodeDaemonUnavailable {
			err = errors.Join(err, e)
		}
		cancel()
		l.operation = ""
	}
	if l.restoreService {
		state, e := l.service.Status()
		if e == nil && state == service.StatusStopped {
			e = l.service.Start()
		}
		err = errors.Join(err, e)
		l.restoreService = false
	}
	for _, h := range l.signals {
		if h != 0 {
			err = errors.Join(err, windows.CloseHandle(h))
		}
	}
	l.signals = nil
	for _, p := range l.processes {
		err = errors.Join(err, p.Close())
	}
	l.processes = nil
	for i := len(l.locks) - 1; i >= 0; i-- {
		err = errors.Join(err, l.locks[i].Close())
	}
	l.locks = nil
	return err
}

// Acquire prepares only the previewed executable resources before publication.
func (c *WindowsUpdateMaintenance) Acquire(ctx context.Context, p update.PreparedUpdate) (_ io.Closer, err error) {
	lease := &windowsUpdateLease{client: c.Client, service: c.Service}
	defer func() {
		if err != nil {
			err = errors.Join(err, lease.Close())
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	targets := []string{p.TargetPath}
	for _, target := range p.Preview.Snapshot.Targets {
		targets = append(targets, target.Path)
	}
	slices.SortFunc(targets, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	directories := map[string]bool{}
	for _, target := range targets {
		dir := strings.ToLower(filepath.Clean(filepath.Dir(target)))
		if directories[dir] {
			continue
		}
		directories[dir] = true
		lock, e := platform.LockBinaryMaintenance(ctx, target)
		if e != nil {
			return nil, e
		}
		lease.locks = append(lease.locks, lock)
	}
	users, err := platform.WindowsBinaryUsers(ctx, targets)
	if err != nil {
		return nil, err
	}
	lease.processes = users
	status, statusErr := c.Client.Status(ctx)
	if statusErr != nil {
		var api protocol.APIError
		if !errors.As(statusErr, &api) || api.Code != protocol.CodeDaemonUnavailable {
			return nil, statusErr
		}
	}
	var daemon *platform.WindowsProcessIdentity
	// Validate all candidates before closing any window. An unregistered process
	// could be an older daemon on another endpoint; never kill it without drain.
	for _, process := range users {
		if statusErr == nil && process.Identity().PID == uint32(status.PID) {
			daemon = process
			continue
		}
		signal, e := process.OpenClientExitSignal()
		if e != nil {
			exited, exitErr := process.Exited(ctx)
			if exitErr == nil && exited {
				lease.signals = append(lease.signals, 0)
				continue
			}
			return nil, fmt.Errorf("close unrecognized Mihari instance %d before updating: %w", process.Identity().PID, errors.Join(e, exitErr))
		}
		lease.signals = append(lease.signals, signal)
	}
	serviceState, err := c.Service.Status()
	if err != nil {
		return nil, err
	}
	if c.ObserveTargets != nil {
		snapshot, e := c.ObserveTargets(ctx, p.TargetPath)
		if e != nil {
			return nil, e
		}
		if e = update.RecheckReplacement(p.Preview, update.ReplacementCandidate{Version: p.Version, SHA256: p.SHA256, Channel: p.Channel}, snapshot); e != nil {
			return nil, e
		}
	}
	if daemon != nil {
		if !slices.Contains(status.Capabilities, protocol.ApplicationUpdateCapability) {
			return nil, fmt.Errorf("stop the older daemon normally before updating; update preparation is unavailable")
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		lease.operation = hex.EncodeToString(nonce[:])
		prepared, e := c.Client.PrepareApplicationUpdate(ctx, lease.operation)
		if e != nil {
			return nil, e
		}
		identity := daemon.Identity()
		if prepared.PID != identity.PID || prepared.CreationFiletime != identity.CreationFiletime || prepared.SID != identity.SID || !strings.EqualFold(prepared.ImagePath, identity.ImagePath) {
			return nil, platform.ErrIdentityMismatch
		}
		openTree := c.openTree
		if openTree == nil {
			openTree = openUpdateRuntimeTree
		}
		lease.tree, err = openTree(ctx, prepared.RuntimeJob, daemon)
		if err != nil {
			return nil, err
		}
	}
	signalIndex := 0
	for _, process := range users {
		if process == daemon {
			continue
		}
		signal := lease.signals[signalIndex]
		signalIndex++
		if signal == 0 {
			continue
		}
		if err = windows.SetEvent(signal); err != nil {
			return nil, err
		}
		exitCtx, exitCancel := context.WithTimeout(ctx, time.Second)
		waitErr := process.WaitExit(exitCtx)
		exitCancel()
		if waitErr != nil {
			if err = process.Terminate(ctx); err != nil {
				return nil, err
			}
		}
	}
	if serviceState == service.StatusRunning {
		// Drain is mandatory if the installed daemon was observed alive.
		if daemon == nil {
			return nil, errors.New("running installed daemon could not be bound to the update targets")
		}
		lease.restoreService = true
		if err = c.Service.Stop(); err != nil {
			return nil, err
		}
		if err = daemon.WaitExit(ctx); err != nil {
			return nil, err
		}
	} else if daemon != nil {
		if err = lease.tree.Terminate(ctx); err != nil {
			return nil, err
		}
		if c.ManualDaemonStopped != nil {
			c.ManualDaemonStopped()
		}
	}
	if lease.tree != nil {
		if err = lease.tree.WaitExit(ctx); err != nil {
			return nil, err
		}
	}
	remaining, e := platform.WindowsBinaryUsers(ctx, targets)
	if e != nil {
		return nil, e
	}
	for _, process := range remaining {
		exited, exitErr := process.Exited(ctx)
		if !exited {
			err = errors.Join(err, fmt.Errorf("mihari instance %d started during update preparation", process.Identity().PID))
		}
		err = errors.Join(err, exitErr, process.Close())
	}
	if err != nil {
		return nil, err
	}
	return lease, nil
}

type updateRuntimeTree interface {
	Terminate(context.Context) error
	WaitExit(context.Context) error
	Close() error
}

type windowsUpdateRuntimeTree struct {
	job         *platform.WindowsRuntimeJob
	termination *platform.WindowsJobTermination
}

func openUpdateRuntimeTree(ctx context.Context, name string, daemon *platform.WindowsProcessIdentity) (updateRuntimeTree, error) {
	job, err := platform.OpenWindowsRuntimeJob(ctx, name)
	if err != nil {
		return nil, err
	}
	termination, err := job.VerifyMember(ctx, daemon)
	if err != nil {
		return nil, errors.Join(err, job.Close())
	}
	return &windowsUpdateRuntimeTree{job: job, termination: termination}, nil
}
func (t *windowsUpdateRuntimeTree) Terminate(ctx context.Context) error {
	return t.termination.Terminate(ctx, 1)
}
func (t *windowsUpdateRuntimeTree) Close() error {
	return errors.Join(t.termination.Close(), t.job.Close())
}
func (t *windowsUpdateRuntimeTree) WaitExit(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		n, err := t.job.ActiveProcesses(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
