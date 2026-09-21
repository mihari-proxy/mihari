package main

import (
	"context"
	"errors"
)

func daemonStartupCleanup(application, core func(context.Context) error) func(context.Context) error {
	if application == nil {
		return nil
	}
	return func(ctx context.Context) error { return errors.Join(application(ctx), core(ctx)) }
}
