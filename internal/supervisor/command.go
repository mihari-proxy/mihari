package supervisor

import (
	"fmt"
	"io"
	"os/exec"
)

type CommandStarter struct {
	BinaryPath string
	DataDir    string
	ConfigPath string
	Stdout     io.Writer
	Stderr     io.Writer
}

func (s CommandStarter) Start() (Child, error) {
	command := exec.Command(s.BinaryPath, commandArguments(s.DataDir, s.ConfigPath)...)
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

func (c *processChild) Wait() error { return c.command.Wait() }

func (c *processChild) Terminate() error { return terminateChild(c.command) }

func (c *processChild) Kill() error { return killChild(c.command) }
