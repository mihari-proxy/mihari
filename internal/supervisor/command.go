package supervisor

import (
	"context"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/core"
	"io"
	"os/exec"
)

type CommandStarter struct {
	// CommandFactory binds installed provenance and committed config for root mode.
	// The returned release closes capabilities after Start, including failure.
	CommandFactory func(context.Context) (core.CoreCommand, func() error, error)

	BinaryPath string
	DataDir    string
	ConfigPath string
	Stdout     io.Writer
	Stderr     io.Writer
}

func (s CommandStarter) Start() (Child, error) {
	command, release, err := s.command(context.Background())
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }() // Read-only verification handles; a started child remains owned even if descriptor cleanup reports an error.
	command.Stdout = s.Stdout
	command.Stderr = s.Stderr
	prepareChild(command)
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start mihomo: %w", err)
	}
	if err := trackChild(command); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("track mihomo child: %w", err)
	}
	return &processChild{command: command}, nil
}

func commandArguments(dataDir, configPath string) []string {
	return []string{"-d", dataDir, "-f", configPath}
}

type processChild struct{ command *exec.Cmd }

func (c *processChild) PID() int { return c.command.Process.Pid }

func (c *processChild) Wait() error {
	waitErr := c.command.Wait()
	return errors.Join(waitErr, flushCapture(c.command.Stdout), flushCapture(c.command.Stderr))
}

func flushCapture(w io.Writer) error {
	flusher, ok := w.(interface{ Flush() error })
	if !ok {
		return nil
	}
	return flusher.Flush()
}

func (c *processChild) Terminate() error { return terminateChild(c.command) }

func (c *processChild) Kill() error { return killChild(c.command) }

func (s CommandStarter) command(ctx context.Context) (*exec.Cmd, func() error, error) {
	command := exec.Command(s.BinaryPath, commandArguments(s.DataDir, s.ConfigPath)...)
	release := func() error { return nil }
	if s.CommandFactory != nil {
		verified, closeCapabilities, e := s.CommandFactory(ctx)
		if e != nil {
			return nil, nil, e
		}
		if closeCapabilities == nil {
			return nil, nil, errors.New("verified command lifetime unavailable")
		}
		release = closeCapabilities
		command = exec.Command(verified.Binary, verified.Args...)
		command.Env = append([]string(nil), verified.Env...)
		command.Dir = verified.Home
	}
	return command, release, nil
}
