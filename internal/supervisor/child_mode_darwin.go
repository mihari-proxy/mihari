//go:build darwin

package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func prepareChildMode(command *exec.Cmd, shared bool) error {
	if !shared {
		prepareChild(command)
		return nil
	}
	if syscall.Getpgrp() != os.Getpid() {
		return errors.New("shared service process group requires daemon leadership")
	}
	command.SysProcAttr = nil // The launchd job remains the owner of this group.
	return nil
}

func startedChild(command *exec.Cmd, shared bool) Child {
	base := &processChild{command: command}
	if !shared {
		return base
	}
	return &sharedDarwinChild{processChild: base, waitExit: platform.DarwinWaitChildExit}
}

type sharedDarwinChild struct {
	*processChild
	mu             sync.Mutex
	exited         bool
	waitExit       func(int) error
	observationErr error
}

func (c *sharedDarwinChild) Wait() error {
	// Observe exit without reaping. A PID cannot be recycled until its owner
	// reaps it; close the signal path before Cmd.Wait performs that operation.
	waitErr := c.waitExit(c.PID())
	if waitErr != nil {
		c.mu.Lock()
		c.observationErr = waitErr
		c.mu.Unlock()
		// Observation failed while we still own an unreaped child. Stop that
		// exact child before joining; never report observation failure as exit.
		if err := c.Kill(); err != nil {
			waitErr = errors.Join(waitErr, err)
			// A failed observation and signal do not release our child. Keep
			// the existing Wait owner until actual exit can be observed; the
			// launchd owns final teardown if observation cannot recover.
			for c.waitExit(c.PID()) != nil {
				_ = (realWaiter{}).Wait(context.Background(), 100*time.Millisecond)
			}
		}
	}
	c.mu.Lock()
	c.exited = true
	c.mu.Unlock()
	return errors.Join(waitErr, c.processChild.Wait())
}

func (c *sharedDarwinChild) signal(signal os.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exited {
		return nil
	}
	err := c.command.Process.Signal(signal)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (c *sharedDarwinChild) Terminate() error { return c.signal(syscall.SIGTERM) }
func (c *sharedDarwinChild) Kill() error      { return c.signal(syscall.SIGKILL) }

func (c *sharedDarwinChild) WaitDescendants(ctx context.Context) error {
	c.mu.Lock()
	observationErr := c.observationErr
	c.mu.Unlock()
	if observationErr != nil {
		return observationErr
	}
	for {
		peers, err := platform.DarwinGroupHasPeers(ctx)
		if err != nil {
			return err
		}
		if !peers {
			return nil
		}
		if err := (realWaiter{}).Wait(ctx, 25*time.Millisecond); err != nil {
			return err
		}
	}
}
