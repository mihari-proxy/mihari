//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestProcess_SIGTERMJoinsCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcess_SIGTERMChild$")
	cmd.Env = append(os.Environ(), "MIHARI_T18_SIGNAL_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	ready, readErr := reader.ReadString('\n')
	if readErr != nil || ready != "ready\n" {
		cancel()
		waitErr := cmd.Wait()
		t.Fatalf("child readiness=%q read=%v wait=%v stderr=%s", ready, readErr, waitErr, stderr.String())
	}
	signalErr := cmd.Process.Signal(syscall.SIGTERM)
	cleaned, cleanupErr := reader.ReadString('\n')
	waitErr := cmd.Wait()
	if signalErr != nil || cleanupErr != nil || cleaned != "cleaned\n" || waitErr != nil {
		t.Fatalf("SIGTERM skipped joined cleanup: signal=%v output=%q read=%v wait=%v stderr=%s", signalErr, cleaned, cleanupErr, waitErr, stderr.String())
	}
}

func TestProcess_SIGTERMChild(t *testing.T) {
	if os.Getenv("MIHARI_T18_SIGNAL_CHILD") != "1" {
		t.Skip("fixture child only")
	}
	code := runWithProcessContext(func(ctx context.Context) (code int) {
		workCtx, cancel := context.WithCancel(ctx)
		joined := make(chan struct{})
		go func() { defer close(joined); <-workCtx.Done() }()
		defer func() {
			cancel()
			<-joined
			if _, err := fmt.Fprintln(os.Stdout, "cleaned"); err != nil {
				code = 1
			}
		}()
		if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
			return 1
		}
		<-ctx.Done()
		return 0
	})
	os.Exit(code)
}
