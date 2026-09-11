package main

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/cli"
	"io"

	"os/signal"
	"strings"
)

// executeWithAssembly defers native identity and filesystem inspection until a
// command actually requires process dependencies. Cobra still owns parsing.
func executeWithAssembly(ctx context.Context, args []string, stdout, stderr io.Writer, pure cli.Dependencies, assemble func(context.Context) (cli.Dependencies, error)) int {
	if pureInvocation(args) || validationInvocation(args) {
		return cli.Execute(ctx, args, stdout, stderr, pure)
	}
	dependencies, err := assemble(ctx)
	if err != nil {
		dependencies.SetupError = err
	}
	return cli.Execute(ctx, args, stdout, stderr, dependencies)
}

func pureInvocation(args []string) bool {
	words := []string{}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
		if arg == "--json" || strings.HasPrefix(arg, "--json=") {
			continue
		}
		words = append(words, arg)
	}
	return len(words) > 0 && words[0] == "help" || len(words) == 2 && words[0] == "self" && words[1] == "version"
}

func validationInvocation(args []string) bool {
	for _, arg := range args {
		if arg == "--install-validation" || strings.HasPrefix(arg, "--install-validation=") {
			return true
		}
	}
	return false
}

func runWithProcessContext(run func(context.Context) int) int {
	ctx, stop := signal.NotifyContext(context.Background(), processSignals()...)
	defer stop()
	return run(ctx)
}
